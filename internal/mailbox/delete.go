package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

type DeleteOptions struct {
	Remote         bool
	ConfirmUnknown bool
	ConfirmFailed  bool
	ConfirmLocal   bool
	Now            time.Time
}

type deleteAction int

const (
	deleteRefuse deleteAction = iota
	deleteConflict
	deleteProvider
	deleteLocal
	deleteAlreadyRemote
)

type deletePlan struct {
	Action deleteAction
	Origin string
	Err    *apperr.Error
}

func planDelete(mailbox domain.Mailbox, opts DeleteOptions) deletePlan {
	if !opts.Remote {
		switch strings.ToLower(strings.TrimSpace(mailbox.RemoteDeleteStatus)) {
		case domain.DeleteUnknown:
			if !opts.ConfirmUnknown {
				return deletePlan{Action: deleteConflict, Err: apperr.New(409, "remote_delete_unknown", "上次远端删除在进程退出前未确认结果，请先核对 iCloud 远端状态后再清理本地记录", true)}
			}
		case domain.DeletePending:
			return deletePlan{Action: deleteConflict, Err: apperr.New(409, "remote_delete_in_progress", "该邮箱已有远端删除操作进行中，请稍后重试", true)}
		case domain.DeleteFailed:
			if !opts.ConfirmFailed {
				return deletePlan{Action: deleteConflict, Err: apperr.New(409, "remote_delete_failed", "上次远端删除失败，请先重试远端删除，或确认已核对 iCloud 后再清理本地记录", true)}
			}
		case "", domain.DeleteSucceeded:
			if mailbox.RemoteDeleteStatus == "" && !opts.ConfirmLocal {
				return deletePlan{Action: deleteConflict, Err: apperr.New(409, "remote_delete_required", "删除邮箱必须先删除远端 Hide My Email，不能只清本地记录", false)}
			}
		}
		return deletePlan{Action: deleteLocal}
	}

	if mailbox.RemoteDeleteStatus == domain.DeleteSucceeded {
		return deletePlan{Action: deleteAlreadyRemote}
	}
	if mailbox.RemoteDeleteStatus == domain.DeletePending {
		return deletePlan{Action: deleteConflict, Err: apperr.New(409, "remote_delete_in_progress", "该邮箱已有远端删除操作进行中，请稍后重试", true)}
	}
	if mailbox.RemoteDeleteStatus == domain.DeleteUnknown && !opts.ConfirmUnknown {
		return deletePlan{Action: deleteConflict, Err: apperr.New(409, "remote_delete_unknown", "上次远端删除在进程退出前未确认结果，请先核对 iCloud 远端状态后再重试", true)}
	}
	origin, err := domain.ProviderOrigin(mailbox.RemoteOrigin)
	if err != nil {
		return deletePlan{Action: deleteRefuse, Err: apperr.New(400, "remote_origin_unknown", "未知远端来源，已拒绝访问 Apple", false)}
	}
	if strings.TrimSpace(mailbox.RemoteAnonymousID) == "" {
		return deletePlan{Action: deleteRefuse, Err: apperr.New(409, "remote_anonymous_id_missing", "缺少远端匿名 ID，已拒绝调用 Apple，本地记录仍保留", false)}
	}
	return deletePlan{Action: deleteProvider, Origin: origin}
}

func (s *Service) DeleteMailbox(ctx context.Context, id string, opts DeleteOptions) error {
	if opts.Now.IsZero() {
		opts.Now = s.now()
	}
	mailbox, ok, err := s.DB.GetMailbox(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	plan := planDelete(mailbox, opts)
	if plan.Err != nil {
		return plan.Err
	}
	unlock := s.lockAccount(mailbox.AccountID)
	defer unlock()
	current, ok, err := s.DB.GetMailbox(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	plan = planDelete(current, opts)
	if plan.Err != nil {
		return plan.Err
	}
	switch plan.Action {
	case deleteLocal, deleteAlreadyRemote:
		if err := s.DB.DeleteMailbox(ctx, id); err != nil {
			return err
		}
		_ = s.DB.AddEvent(ctx, domain.Event{At: opts.Now, Level: "info", Action: "mailbox_deleted_local", AccountID: current.AccountID, MailboxID: id, Message: "已清理本地邮箱记录"})
		return nil
	case deleteProvider:
		if err := s.DB.BeginRemoteDelete(ctx, id, opts.Now); err != nil {
			return err
		}
		session, account, err := s.DB.LoadSession(ctx, current.AccountID)
		if err != nil {
			_ = s.markRemoteDelete(ctx, id, domain.DeleteFailed, "读取登录态失败")
			return err
		}
		settings, settingsErr := s.DB.GetSettings(ctx)
		apiKey, keyErr := deleteAPIKey(settingsErr, settings.AppleAccountAPIKey)
		if keyErr != nil {
			_ = s.markRemoteDelete(ctx, id, domain.DeleteFailed, "读取设置失败，已停止远端删除")
			return keyErr
		}
		updated, callErr := s.Provider.Delete(ctx, session, plan.Origin, current.RemoteAnonymousID, apiKey)
		if callErr != nil {
			status, message := remoteDeleteMark(callErr)
			markErr := s.markRemoteDelete(ctx, id, status, message)
			_ = s.addDeleteEvent(ctx, domain.Event{At: s.now(), Level: "error", Action: "mailbox_remote_delete_" + status, AccountID: current.AccountID, MailboxID: id, Message: message})
			if markErr != nil {
				return markErr
			}
			return callErr
		}
		return persistCall(ctx, func(persistCtx context.Context) error {
			if _, saveErr := s.DB.SaveSession(persistCtx, account.ID, updated); saveErr != nil {
				_ = s.DB.AddEvent(persistCtx, domain.Event{At: s.now(), Level: "warn", Action: "session_persist_after_delete", AccountID: account.ID, MailboxID: id, Message: "远端已删除，但刷新后的登录态写入失败"})
			}
			if err := s.markRemoteDeleteRetry(persistCtx, id, domain.DeleteSucceeded, ""); err != nil {
				_ = s.DB.AddEvent(persistCtx, domain.Event{At: s.now(), Level: "warn", Action: "mailbox_remote_delete_mark_failed", AccountID: current.AccountID, MailboxID: id, Message: "远端已删除，但成功状态写入失败，仍继续清理本地记录"})
			}
			if err := s.DB.DeleteMailbox(persistCtx, id); err != nil {
				_ = s.markRemoteDeleteRetry(persistCtx, id, domain.DeleteUnknown, "远端已删除，但本地记录清理失败，请核对后重试")
				return apperr.New(500, "mailbox_delete_persist_failed", "远端已删除，但本地记录删除失败，请重试本地清理", true)
			}
			_ = s.DB.AddEvent(persistCtx, domain.Event{At: s.now(), Level: "info", Action: "mailbox_remote_deleted", AccountID: current.AccountID, MailboxID: id, Message: "已删除远端 Hide My Email 并清理本地记录"})
			return nil
		})
	default:
		return apperr.New(500, "delete_plan_invalid", "删除计划无效", false)
	}
}

func (s *Service) markRemoteDelete(parent context.Context, id, status, message string) error {
	return persistCall(parent, func(ctx context.Context) error {
		return s.DB.MarkRemoteDelete(ctx, id, status, message, s.now())
	})
}

func (s *Service) markRemoteDeleteRetry(parent context.Context, id, status, message string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.markRemoteDelete(parent, id, status, message)
		if err == nil {
			return nil
		}
	}
	return err
}

func (s *Service) addDeleteEvent(parent context.Context, event domain.Event) error {
	return persistCall(parent, func(ctx context.Context) error {
		return s.DB.AddEvent(ctx, event)
	})
}
