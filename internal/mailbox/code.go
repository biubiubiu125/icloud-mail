package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

type CodeOptions struct {
	Keyword    string
	After      time.Time
	Wait       time.Duration
	Cache      bool
	AllowStale bool
}

func (s *Service) Code(ctx context.Context, email, token string, opts CodeOptions) (domain.CodeResult, error) {
	if err := s.requirePublicAPI(ctx); err != nil {
		return domain.CodeResult{}, err
	}
	settings, err := s.DB.GetSettings(ctx)
	if err != nil {
		return domain.CodeResult{}, err
	}
	mailbox, ok, err := s.DB.GetMailboxByEmail(ctx, email)
	if err != nil {
		return domain.CodeResult{}, err
	}
	if !ok || !mailbox.APIActive || !domain.TokenMatches(token, mailbox.APIToken) {
		return domain.CodeResult{}, apperr.New(404, "mailbox_not_found", "邮箱不存在或密钥不正确", false)
	}
	if strings.TrimSpace(opts.Keyword) == "" {
		opts.Keyword = settings.DefaultKeyword
	}
	if opts.Wait < 0 {
		opts.Wait = 0
	}
	if opts.Wait > domain.MaxCodeWait {
		opts.Wait = domain.MaxCodeWait
	}
	deadline := s.now().Add(opts.Wait)
	synced := false
	source := "cache"
	var syncErr error
	for {
		if !opts.Cache && !synced {
			var channel string
			channel, syncErr = s.syncMailbox(ctx, mailbox, opts.Keyword, settings.WebCodeSyncEnabled)
			if syncErr == nil && channel != "" {
				source = channel
			}
			synced = true
		}
		result, err := s.finishAuthorizedCode(ctx, mailbox.ID, token, opts, source, syncErr)
		if err != nil || result.Code != "" {
			return result, err
		}
		if opts.Cache || s.now().After(deadline) {
			break
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.CodeResult{}, ctx.Err()
		case <-timer.C:
		}
		channel, syncErr := s.syncMailbox(ctx, mailbox, opts.Keyword, settings.WebCodeSyncEnabled)
		if syncErr == nil && channel != "" {
			source = channel
		}
		result, err = s.finishAuthorizedCode(ctx, mailbox.ID, token, opts, source, syncErr)
		if err != nil || result.Code != "" {
			return result, err
		}
	}
	if syncErr != nil {
		return domain.CodeResult{}, syncErr
	}
	return domain.CodeResult{}, apperr.New(404, "code_not_found", "还没有匹配的验证码", true)
}

func (s *Service) finishAuthorizedCode(ctx context.Context, mailboxID, token string, opts CodeOptions, source string, syncErr error) (domain.CodeResult, error) {
	current, err := s.codeStillAuthorized(ctx, mailboxID, token)
	if err != nil {
		return domain.CodeResult{}, err
	}
	cached, ok, err := s.cachedCode(ctx, current, opts)
	if err != nil {
		return domain.CodeResult{}, err
	}
	if syncErr != nil {
		if ok && (!cached.StaleCache || opts.AllowStale) {
			return finishCode(cached, current, "cache"), nil
		}
		return domain.CodeResult{}, syncErr
	}
	if ok {
		return finishCode(cached, current, source), nil
	}
	return domain.CodeResult{}, nil
}

func (s *Service) codeStillAuthorized(ctx context.Context, id, token string) (domain.Mailbox, error) {
	current, ok, err := s.DB.GetMailbox(ctx, id)
	if err != nil {
		return domain.Mailbox{}, err
	}
	if !ok || !current.APIActive || !domain.TokenMatches(token, current.APIToken) {
		return domain.Mailbox{}, apperr.New(404, "mailbox_not_found", "邮箱不存在或密钥不正确", false)
	}
	return current, nil
}

func finishCode(result domain.CodeResult, mailbox domain.Mailbox, fallback string) domain.CodeResult {
	result.Email = mailbox.Email
	if result.Source == "" {
		if fallback == "" {
			fallback = "cache"
		}
		result.Source = fallback
	}
	return result
}

func (s *Service) cachedCode(ctx context.Context, mailbox domain.Mailbox, opts CodeOptions) (domain.CodeResult, bool, error) {
	floor := time.Time{}
	if mailbox.CodeNotBefore != nil {
		floor = *mailbox.CodeNotBefore
	}
	items, err := s.DB.ListMessages(ctx, mailbox.ID, laterTime(opts.After, floor), 50)
	if err != nil {
		return domain.CodeResult{}, false, err
	}
	result, ok, _ := codeFromMessages(items, opts.Keyword, opts.After, floor, domain.CodeFreshWindow, s.now(), opts.AllowStale)
	return result, ok, nil
}

func laterTime(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.After(a)) {
		return b
	}
	return a
}

func (s *Service) syncMailbox(ctx context.Context, mailbox domain.Mailbox, keyword string, allowWeb bool) (string, error) {
	if mailbox.RemoteDeleteStatus == domain.DeletePending {
		return "", apperr.New(409, "remote_delete_in_progress", "邮箱正在远端删除，已停止取信", true)
	}
	unlock := s.lockAccount(mailbox.AccountID)
	defer unlock()
	current, ok, err := s.DB.GetMailbox(ctx, mailbox.ID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", apperr.New(404, "mailbox_not_found", "邮箱不存在或密钥不正确", false)
	}
	if current.RemoteDeleteStatus == domain.DeletePending {
		return "", apperr.New(409, "remote_delete_in_progress", "邮箱正在远端删除，已停止取信", true)
	}
	mailbox = current
	session, _, err := s.DB.LoadSession(ctx, mailbox.AccountID)
	if err != nil {
		return "", err
	}
	state, ok := protocol.IMAPLoginState(session)
	if !ok {
		if !allowWeb {
			return "", apperr.New(409, "imap_session_missing", "未保存 IMAP 取码登录，且 Web 取码通道已关闭", true)
		}
		return "web", s.syncWeb(ctx, session, mailbox, keyword)
	}
	protoBox := protocol.Mailbox{ID: mailbox.ID, Email: mailbox.Email}
	if mailbox.LastSyncAt != nil {
		protoBox.LastSyncAt = *mailbox.LastSyncAt
	}
	protoBox.LastSyncUID = mailbox.LastSyncUID
	found, cursor, err := s.Provider.SyncIMAP(ctx, state, []protocol.Mailbox{protoBox}, time.Time{}, keyword, 50)
	if err != nil {
		return "", err
	}
	return "imap", s.storeSynced(ctx, mailbox, found[mailbox.ID], "imap", cursor)
}

func (s *Service) syncWeb(ctx context.Context, session protocol.ICloudSession, mailbox domain.Mailbox, keyword string) error {
	protoBox := protocol.Mailbox{ID: mailbox.ID, Email: mailbox.Email}
	items, err := s.Provider.SyncWeb(ctx, session, protoBox, time.Time{}, keyword, 50)
	if err != nil {
		return err
	}
	return s.storeSynced(ctx, mailbox, items, "web", "")
}

func (s *Service) storeSynced(ctx context.Context, mailbox domain.Mailbox, items []protocol.ICloudSyncedMessage, source, imapCursor string) error {
	now := s.now()
	stored := make([]domain.Message, 0, len(items))
	messageUIDs := make([]string, 0, len(items))
	for _, item := range items {
		message, ok := toStoredMessage(mailbox.ID, item)
		if !ok {
			continue
		}
		message.Source = source
		stored = append(stored, message)
		messageUIDs = append(messageUIDs, item.UID)
	}
	latestUID := nextSyncUID(mailbox.LastSyncUID, source, imapCursor, messageUIDs)
	if err := s.DB.UpsertMessages(ctx, mailbox.ID, stored); err != nil {
		return err
	}
	return s.DB.TouchSync(ctx, mailbox.ID, latestUID, now)
}

func (s *Service) SyncAccountMailboxes(ctx context.Context, accountID string) (int, error) {
	unlock := s.lockAccount(accountID)
	defer unlock()
	session, account, err := s.DB.LoadSession(ctx, accountID)
	if err != nil {
		return 0, err
	}
	settings, err := s.DB.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	origins := []string{}
	if account.AppleAccountLoginSaved || protocolHasKind(session, protocol.LoginStateAppleAccount) {
		origins = append(origins, domain.OriginAppleAccount)
	}
	if account.WebLoginSaved || protocolHasWeb(session) {
		origins = append(origins, domain.OriginICloudWeb)
	}
	checkOrigin := ""
	if account.ReconciliationRequired {
		var originErr error
		checkOrigin, originErr = knownListOrigin(account.ReconciliationOrigin)
		if originErr != nil {
			return 0, apperr.New(409, "mailbox_create_reconciliation_required", "创建结果待核对，未知远端来源不能同步", true)
		}
		origins = []string{checkOrigin}
	}
	if len(origins) == 0 {
		return 0, apperr.New(409, "icloud_session_missing", "未保存可用于同步的登录态", true)
	}
	imported := 0
	var checked []protocol.ICloudRemoteMailbox
	checkedList := false
	for _, origin := range origins {
		remotes, updated, err := s.Provider.List(ctx, session, origin, settings.AppleAccountAPIKey)
		if err != nil {
			if account.ReconciliationRequired {
				return imported, apperr.New(409, "mailbox_create_reconciliation_required", "创建结果待核对，自动同步失败", true)
			}
			return imported, err
		}
		session = updated
		if _, err := s.DB.SaveSession(ctx, accountID, updated); err != nil {
			return imported, err
		}
		n, err := s.importRemotes(ctx, accountID, origin, remotes)
		imported += n
		if err != nil {
			return imported, err
		}
		if account.ReconciliationRequired && origin == checkOrigin {
			checked = remotes
			checkedList = true
		}
	}
	if account.ReconciliationRequired {
		if !checkedList {
			return imported, apperr.New(409, "mailbox_create_reconciliation_required", "创建结果待核对，请先成功同步远端列表", true)
		}
		if err := s.clearReconciliationIfSeen(ctx, account, checked); err != nil {
			return imported, err
		}
	}
	return imported, nil
}

func (s *Service) importRemotes(ctx context.Context, accountID, origin string, remotes []protocol.ICloudRemoteMailbox) (int, error) {
	existing, err := s.DB.ListMailboxes(ctx, accountID, "")
	if err != nil {
		return 0, err
	}
	byEmail := map[string]domain.Mailbox{}
	for _, item := range existing {
		byEmail[domain.NormalizeEmail(item.Email)] = item
	}
	seen := map[string]struct{}{}
	imported := 0
	now := s.now()
	for _, remote := range remotes {
		email := domain.NormalizeEmail(remote.Email)
		if email == "" {
			continue
		}
		seen[email] = struct{}{}
		if current, ok := byEmail[email]; ok {
			if !remoteListOwns(current.RemoteOrigin, origin) {
				continue
			}
			if err := s.DB.MarkRemotePresent(ctx, current.ID, remote.AnonymousID, origin, remote.IsActive); err != nil {
				return imported, err
			}
			continue
		}
		token, err := protocol.RandomToken(24)
		if err != nil {
			return imported, err
		}
		id, err := protocol.RandomID()
		if err != nil {
			return imported, err
		}
		item := domain.Mailbox{
			ID:                id,
			AccountID:         accountID,
			Email:             email,
			Label:             remote.Label,
			Note:              remote.Note,
			Status:            domain.StatusAvailable,
			APIToken:          token,
			APIActive:         true,
			ICloudActive:      remote.IsActive,
			RemoteAnonymousID: remote.AnonymousID,
			RemoteOrigin:      origin,
			CreatedAt:         now,
		}
		if err := s.DB.InsertMailbox(ctx, item); err != nil {
			return imported, err
		}
		imported++
	}
	for email, current := range byEmail {
		if _, ok := seen[email]; ok {
			continue
		}
		if remoteListOwns(current.RemoteOrigin, origin) {
			if err := s.DB.MarkRemoteMissing(ctx, current.ID, now); err != nil {
				return imported, err
			}
		}
	}
	return imported, nil
}

func protocolHasKind(session protocol.ICloudSession, kind string) bool {
	for _, state := range session.LoginStates {
		if state.Kind == kind {
			return true
		}
	}
	return false
}

func protocolHasWeb(session protocol.ICloudSession) bool {
	return protocolHasKind(session, protocol.LoginStateICloudWeb) || (len(session.LoginStates) == 0 && len(session.Cookies) > 0)
}
