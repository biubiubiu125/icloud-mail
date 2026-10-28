package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

var newMailboxToken = func() (string, error) { return protocol.RandomToken(24) }
var newMailboxID = func() (string, error) { return protocol.RandomID() }

func (s *Service) CreateMailbox(ctx context.Context, accountID, label, note, channel string) (domain.Mailbox, error) {
	if s.createHalted(accountID) {
		return domain.Mailbox{}, createPersistFailed()
	}
	unlock := s.lockAccount(accountID)
	defer unlock()
	if s.createHalted(accountID) {
		return domain.Mailbox{}, createPersistFailed()
	}
	account, ok, err := s.DB.GetAccount(ctx, accountID)
	if err != nil {
		return domain.Mailbox{}, err
	}
	if !ok {
		return domain.Mailbox{}, apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if account.ReconciliationRequired {
		if _, err := s.reconcileLocked(ctx, accountID); err != nil {
			return domain.Mailbox{}, err
		}
	}
	session, _, err := s.DB.LoadSession(ctx, accountID)
	if err != nil {
		return domain.Mailbox{}, err
	}
	settings, err := s.DB.GetSettings(ctx)
	if err != nil {
		return domain.Mailbox{}, err
	}
	channel = normalizeChannel(channel, session)
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	remote, updated, err := s.Provider.Create(opCtx, session, channel, label, note, settings.AppleAccountAPIKey)
	origin, originErr := acceptCreateOrigin(remote.Origin, channel)
	if originErr != nil {
		raw := strings.TrimSpace(remote.Origin)
		if markErr := s.stopCreate(ctx, accountID, raw, "远端返回了未知来源，已停止继续创建", remote.AnonymousID, remote.Email); markErr != nil {
			return domain.Mailbox{}, markErr
		}
		if err != nil {
			return domain.Mailbox{}, err
		}
		return domain.Mailbox{}, apperr.New(400, "remote_origin_unknown", "未知远端来源，已拒绝访问 Apple", false)
	}
	if err != nil {
		_, _ = s.DB.SaveSession(ctx, accountID, updated)
		if protocol.UncertainProviderResult(err) || protocol.IsCode(err, "icloud_mailbox_anonymous_id_missing") {
			stopOrigin := reconciliationOrigin(channel, remote.Origin, err)
			if markErr := s.stopCreate(ctx, accountID, stopOrigin, protocol.PublicMessage(err), remote.AnonymousID, remote.Email); markErr != nil {
				return domain.Mailbox{}, markErr
			}
		}
		return domain.Mailbox{}, err
	}
	if _, err := s.DB.SaveSession(ctx, accountID, updated); err != nil {
		if markErr := s.stopCreate(ctx, accountID, origin, "远端隐私邮箱已创建，但登录态写入失败", remote.AnonymousID, remote.Email); markErr != nil {
			return domain.Mailbox{}, markErr
		}
		return domain.Mailbox{}, apperr.New(500, "icloud_session_persist_after_mailbox_create", "远端隐私邮箱已创建，但登录态写入失败", true)
	}
	if strings.TrimSpace(remote.AnonymousID) == "" {
		if markErr := s.stopCreate(ctx, accountID, origin, "远端已创建但没有匿名 ID", remote.AnonymousID, remote.Email); markErr != nil {
			return domain.Mailbox{}, markErr
		}
		return domain.Mailbox{}, apperr.New(502, "icloud_mailbox_anonymous_id_missing", "远端已创建邮箱但未返回匿名 ID，已停止继续创建", true)
	}
	token, err := newMailboxToken()
	if err != nil {
		return domain.Mailbox{}, abortUntrackedCreate(err, func() error {
			return s.stopCreate(ctx, accountID, origin, "远端已创建但本地密钥生成失败", remote.AnonymousID, remote.Email)
		})
	}
	id, err := newMailboxID()
	if err != nil {
		return domain.Mailbox{}, abortUntrackedCreate(err, func() error {
			return s.stopCreate(ctx, accountID, origin, "远端已创建但本地记录生成失败", remote.AnonymousID, remote.Email)
		})
	}
	now := s.now()
	item := domain.Mailbox{
		ID:                id,
		AccountID:         accountID,
		Email:             domain.NormalizeEmail(remote.Email),
		Label:             firstNonEmpty(remote.Label, label),
		Note:              firstNonEmpty(remote.Note, note),
		Status:            domain.StatusAvailable,
		APIToken:          token,
		APIActive:         true,
		ICloudActive:      true,
		RemoteAnonymousID: remote.AnonymousID,
		RemoteOrigin:      origin,
		CreatedAt:         now,
	}
	if item.Email == "" {
		if markErr := s.stopCreate(ctx, accountID, origin, "远端已创建但没有邮箱地址", remote.AnonymousID, remote.Email); markErr != nil {
			return domain.Mailbox{}, markErr
		}
		return domain.Mailbox{}, apperr.New(502, "icloud_mailbox_email_missing", "远端已创建邮箱但未返回地址，已停止继续创建", true)
	}
	if err := s.DB.InsertMailbox(ctx, item); err != nil {
		lookupCtx, lookupCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		tracked, lookupErr := s.remoteAnonymousIDTracked(lookupCtx, item.RemoteAnonymousID)
		lookupCancel()
		if lookupErr != nil || tracked {
			if lookupErr != nil {
				if markErr := s.stopCreate(ctx, accountID, origin, "无法确认远端匿名 ID 是否已在本地，已停止删除和继续创建", item.RemoteAnonymousID, item.Email); markErr != nil {
					return domain.Mailbox{}, markErr
				}
			}
			return domain.Mailbox{}, err
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := s.Provider.Delete(cleanupCtx, updated, origin, item.RemoteAnonymousID, settings.AppleAccountAPIKey); cleanupErr != nil {
			if markErr := s.stopCreate(ctx, accountID, origin, "本地写入失败且远端回滚失败", item.RemoteAnonymousID, item.Email); markErr != nil {
				return domain.Mailbox{}, markErr
			}
			return domain.Mailbox{}, apperr.New(500, "mailbox_create_persist_and_cleanup_failed", "本地写入失败，远端回滚也失败，已停止继续创建", true)
		}
		return domain.Mailbox{}, err
	}
	_ = s.DB.AddEvent(ctx, domain.Event{At: now, Level: "info", Action: "mailbox_created", AccountID: accountID, MailboxID: item.ID, Message: "已创建远端隐私邮箱"})
	return item, nil
}

func (s *Service) reconcileLocked(ctx context.Context, accountID string) (int, error) {
	account, ok, err := s.DB.GetAccount(ctx, accountID)
	if err != nil || !ok {
		return 0, apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if !account.ReconciliationRequired {
		return 0, nil
	}
	session, _, err := s.DB.LoadSession(ctx, accountID)
	if err != nil {
		return 0, err
	}
	settings, err := s.DB.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	origin, originErr := knownListOrigin(account.ReconciliationOrigin)
	if originErr != nil {
		return 0, apperr.New(409, "mailbox_create_reconciliation_required", "创建结果待核对，未知远端来源不能同步", true)
	}
	remotes, updated, err := s.Provider.List(ctx, session, origin, settings.AppleAccountAPIKey)
	if err != nil {
		return 0, apperr.New(409, "mailbox_create_reconciliation_required", "创建结果待核对，请先成功同步远端列表", true)
	}
	if _, err := s.DB.SaveSession(ctx, accountID, updated); err != nil {
		return 0, err
	}
	n, err := s.importRemotes(ctx, accountID, origin, remotes)
	if err != nil {
		return 0, err
	}
	if err := s.clearReconciliationIfSeen(ctx, account, remotes); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Service) clearReconciliationIfSeen(ctx context.Context, account domain.Account, remotes []protocol.ICloudRemoteMailbox) error {
	if !reconciliationTargetFound(account, remotes) {
		return apperr.New(409, "mailbox_create_reconciliation_required", "创建结果待核对，远端列表里还没有对应邮箱", true)
	}
	return s.DB.ClearReconciliation(ctx, account.ID)
}

func reconciliationTargetFound(account domain.Account, remotes []protocol.ICloudRemoteMailbox) bool {
	id := strings.TrimSpace(account.ReconciliationAnonymousID)
	email := domain.NormalizeEmail(account.ReconciliationEmail)
	if id == "" && email == "" {
		return true
	}
	for _, remote := range remotes {
		if id != "" && strings.TrimSpace(remote.AnonymousID) == id {
			return true
		}
		if email != "" && domain.NormalizeEmail(remote.Email) == email {
			return true
		}
	}
	return false
}

func normalizeChannel(channel string, session protocol.ICloudSession) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "apple_account", domain.OriginAppleAccount:
		return "apple_account"
	case "icloud_web", domain.OriginICloudWeb:
		return "icloud_web"
	default:
		if protocolHasKind(session, protocol.LoginStateAppleAccount) {
			return "apple_account"
		}
		return "icloud_web"
	}
}

func reconciliationOrigin(channel, remoteOrigin string, err error) string {
	origin := strings.ToUpper(strings.TrimSpace(remoteOrigin))
	if origin == domain.OriginAppleAccount || origin == domain.OriginICloudWeb {
		return origin
	}
	switch protocol.CodeOf(err) {
	case "apple_account_create_uncertain", "apple_account_create_empty":
		return domain.OriginAppleAccount
	case "icloud_create_uncertain":
		return domain.OriginICloudWeb
	}
	if normalizeChannel(channel, protocol.ICloudSession{}) == "apple_account" {
		return domain.OriginAppleAccount
	}
	return domain.OriginICloudWeb
}

func firstOrigin(remoteOrigin, channel string) string {
	if strings.TrimSpace(remoteOrigin) != "" {
		return remoteOrigin
	}
	if normalizeChannel(channel, protocol.ICloudSession{}) == "apple_account" {
		return domain.OriginAppleAccount
	}
	return domain.OriginICloudWeb
}

func acceptCreateOrigin(remoteOrigin, channel string) (string, error) {
	return domain.ProviderOrigin(firstOrigin(remoteOrigin, channel))
}

func knownListOrigin(origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return domain.OriginICloudWeb, nil
	}
	return domain.ProviderOrigin(origin)
}

func (s *Service) stopCreate(ctx context.Context, accountID, origin, message, anonymousID, email string) error {
	id, mail, err := reconciliationMarkTarget(anonymousID, email)
	if err != nil {
		s.recordCreateHalt(accountID, origin, message, anonymousID, email)
		s.logBackground("create", "reconciliation_mark", err)
		return createPersistFailed()
	}
	markCtx, cancel := persistContext(ctx)
	defer cancel()
	if err := s.DB.MarkReconciliation(markCtx, accountID, origin, message, id, mail, s.now()); err != nil {
		s.recordCreateHalt(accountID, origin, message, id, mail)
		s.logBackground("create", "reconciliation_mark", err)
		return createPersistFailed()
	}
	_ = s.DB.AddEvent(markCtx, domain.Event{At: s.now(), Level: "error", Action: "mailbox_create_uncertain", AccountID: accountID, Message: message})
	return nil
}

func reconciliationMarkTarget(anonymousID, email string) (string, string, error) {
	id := strings.TrimSpace(anonymousID)
	mail := strings.TrimSpace(email)
	if id != "" || mail != "" {
		return id, mail, nil
	}
	token, err := protocol.RandomID()
	if err != nil {
		return "", "", err
	}
	return "unresolved:" + token, "", nil
}

func (s *Service) ClearReconciliation(ctx context.Context, accountID string, confirm bool) error {
	if !confirm {
		return apperr.New(400, "reconciliation_confirm_required", "清除待核对必须明确确认", false)
	}
	unlock := s.lockAccount(accountID)
	defer unlock()
	account, ok, err := s.DB.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if account.ReconciliationRequired {
		if err := s.DB.ClearReconciliation(ctx, accountID); err != nil {
			return err
		}
	}
	s.releaseCreateHalt(accountID)
	return nil
}

func (s *Service) remoteAnonymousIDTracked(ctx context.Context, anonymousID string) (bool, error) {
	items, err := s.DB.ListMailboxes(ctx, "", "")
	if err != nil {
		return false, err
	}
	return remoteIDTracked(items, anonymousID), nil
}

func remoteIDTracked(items []domain.Mailbox, anonymousID string) bool {
	anonymousID = strings.TrimSpace(anonymousID)
	if anonymousID == "" {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item.RemoteAnonymousID) == anonymousID {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
