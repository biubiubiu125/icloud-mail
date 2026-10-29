package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

const (
	loginChannelWeb   = "web"
	loginChannelApple = "apple"
)

type pendingOwner struct {
	accountID      string
	appleID        string
	password       string
	updatePassword bool
	channel        string
	expiresAt      time.Time
}

type LoginResult struct {
	PendingID string `json:"pending_id,omitempty"`
	Needs2FA  bool   `json:"needs_2fa"`
	Message   string `json:"message,omitempty"`
	AppleID   string `json:"apple_id,omitempty"`
	Saved     bool   `json:"saved"`
}

func (s *Service) StartWebLogin(ctx context.Context, accountID, appleID, password, twoFactor string) (LoginResult, error) {
	unlock := s.lockAccount(accountID)
	defer unlock()
	sessionProxy, account, err := s.loginContext(ctx, accountID, appleID, password)
	if err != nil {
		return LoginResult{}, err
	}
	started, err := s.Auth.StartICloudWebLogin(ctx, account.AppleID, passwordOrSaved(password, account.ApplePassword), "", "", twoFactor, sessionProxy, s.Pending)
	if err != nil {
		return LoginResult{}, err
	}
	return s.finishStart(ctx, account, password, started, loginChannelWeb)
}

func (s *Service) FinishWebLogin(ctx context.Context, accountID, pendingID, code string) (LoginResult, error) {
	unlock := s.lockAccount(accountID)
	defer unlock()
	if err := s.pendingOwnedByChannel(pendingID, accountID, loginChannelWeb); err != nil {
		return LoginResult{}, err
	}
	session, err := s.Auth.FinishICloudWebLogin(ctx, pendingID, code, s.Pending)
	if err != nil {
		return LoginResult{}, err
	}
	return s.finishPending(ctx, accountID, pendingID, session, "已保存 iCloud Web 登录态")
}

func (s *Service) StartAppleLogin(ctx context.Context, accountID, appleID, password, twoFactor string) (LoginResult, error) {
	unlock := s.lockAccount(accountID)
	defer unlock()
	sessionProxy, account, err := s.loginContext(ctx, accountID, appleID, password)
	if err != nil {
		return LoginResult{}, err
	}
	started, err := s.Auth.StartAppleAccountLogin(ctx, account.AppleID, passwordOrSaved(password, account.ApplePassword), twoFactor, sessionProxy, s.Pending)
	if err != nil {
		return LoginResult{}, err
	}
	return s.finishStart(ctx, account, password, started, loginChannelApple)
}

func (s *Service) FinishAppleLogin(ctx context.Context, accountID, pendingID, code string) (LoginResult, error) {
	unlock := s.lockAccount(accountID)
	defer unlock()
	if err := s.pendingOwnedByChannel(pendingID, accountID, loginChannelApple); err != nil {
		return LoginResult{}, err
	}
	session, err := s.Auth.FinishAppleAccountLogin(ctx, pendingID, code, s.Pending)
	if err != nil {
		return LoginResult{}, err
	}
	return s.finishPending(ctx, accountID, pendingID, session, "已保存 Apple Account 登录态")
}

func (s *Service) SaveIMAP(ctx context.Context, accountID, email, appPassword string) error {
	unlock := s.lockAccount(accountID)
	defer unlock()
	account, ok, err := s.DB.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	email = domain.NormalizeEmail(email)
	appPassword = strings.TrimSpace(appPassword)
	if email == "" || appPassword == "" {
		return apperr.New(400, "imap_login_missing", "请输入 iCloud 邮箱和 App 专用密码", false)
	}
	session, _, err := s.DB.LoadSession(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.Provider.CheckIMAP(ctx, email, appPassword, account.ProxyURL); err != nil {
		return err
	}
	session = protocol.WithIMAPLoginState(session, protocol.LoginState{
		IMAPEmail:       email,
		IMAPAppPassword: appPassword,
	})
	session.AppleID = firstNonEmpty(session.AppleID, account.AppleID, email)
	if _, err := s.DB.SaveSession(ctx, accountID, session); err != nil {
		return err
	}
	_ = s.DB.AddEvent(ctx, domain.Event{At: s.now(), Level: "info", Action: "imap_saved", AccountID: accountID, Message: "已保存 IMAP 取码登录"})
	return nil
}

func (s *Service) loginContext(ctx context.Context, accountID, appleID, password string) (string, domain.Account, error) {
	account, ok, err := s.DB.GetAccount(ctx, accountID)
	if err != nil {
		return "", domain.Account{}, err
	}
	if !ok {
		return "", domain.Account{}, apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if strings.TrimSpace(appleID) != "" {
		account.AppleID = strings.TrimSpace(appleID)
	}
	if account.AppleID == "" {
		return "", domain.Account{}, apperr.New(400, "apple_id_missing", "请先填写 Apple ID", false)
	}
	if passwordOrSaved(password, account.ApplePassword) == "" {
		return "", domain.Account{}, apperr.New(400, "apple_password_missing", "请输入 Apple 密码", false)
	}
	return account.ProxyURL, account, nil
}

func (s *Service) finishStart(ctx context.Context, account domain.Account, password string, started protocol.LoginStart, channel string) (LoginResult, error) {
	updatePassword := strings.TrimSpace(password) != ""
	if started.Needs2FA {
		s.bindLoginPending(started.PendingID, pendingOwner{
			accountID:      account.ID,
			appleID:        account.AppleID,
			password:       password,
			updatePassword: updatePassword,
			channel:        channel,
		})
		return LoginResult{PendingID: started.PendingID, Needs2FA: true, Message: started.Message, AppleID: started.AppleID}, nil
	}
	if updatePassword {
		account.ApplePassword = password
	}
	return s.commitSavedLogin(ctx, account, updatePassword, started.Session, started.Message)
}

func (s *Service) bindPending(pendingID, accountID string) {
	s.bindLoginPending(pendingID, pendingOwner{accountID: accountID})
}

func (s *Service) bindLoginPending(pendingID string, owner pendingOwner) {
	pendingID = strings.TrimSpace(pendingID)
	if pendingID == "" || owner.accountID == "" {
		return
	}
	owner.appleID = strings.TrimSpace(owner.appleID)
	owner.expiresAt = s.now().Add(10 * time.Minute)
	s.pendingOwners.Store(pendingID, owner)
}

func (s *Service) finishPending(ctx context.Context, accountID, pendingID string, session protocol.ICloudSession, message string) (LoginResult, error) {
	owner, err := s.pendingOwnerFor(pendingID, accountID)
	if err != nil {
		return LoginResult{}, err
	}
	account := domain.Account{ID: accountID, AppleID: owner.appleID, ApplePassword: owner.password}
	result, err := s.commitSavedLogin(ctx, account, owner.updatePassword, session, message)
	if err != nil {
		return LoginResult{}, err
	}
	s.pendingOwners.Delete(strings.TrimSpace(pendingID))
	return result, nil
}

func (s *Service) pendingOwnedBy(pendingID, accountID string) error {
	_, err := s.pendingOwnerFor(pendingID, accountID)
	return err
}

func (s *Service) pendingOwnedByChannel(pendingID, accountID, channel string) error {
	owner, err := s.pendingOwnerFor(pendingID, accountID)
	if err != nil {
		return err
	}
	if owner.channel == "" || owner.channel != channel {
		return apperr.New(409, "login_pending_channel_mismatch", "这个验证码属于另一条登录，请点对应的提交按钮", true)
	}
	return nil
}

func (s *Service) pendingOwnerFor(pendingID, accountID string) (pendingOwner, error) {
	pendingID = strings.TrimSpace(pendingID)
	if pendingID == "" {
		return pendingOwner{}, apperr.New(400, "login_pending_missing", "缺少登录挑战", false)
	}
	value, ok := s.pendingOwners.Load(pendingID)
	if !ok {
		return pendingOwner{}, apperr.New(409, "login_pending_account_mismatch", "登录挑战不属于这个账号", true)
	}
	owner, ok := value.(pendingOwner)
	if !ok || owner.accountID != accountID || !owner.expiresAt.After(s.now()) {
		if ok && !owner.expiresAt.After(s.now()) {
			s.pendingOwners.Delete(pendingID)
			return pendingOwner{}, apperr.New(409, "login_pending_expired", "登录挑战已过期，请重新发起", true)
		}
		return pendingOwner{}, apperr.New(409, "login_pending_account_mismatch", "登录挑战不属于这个账号", true)
	}
	return owner, nil
}

func (s *Service) commitSavedLogin(ctx context.Context, account domain.Account, updatePassword bool, session protocol.ICloudSession, message string) (LoginResult, error) {
	account.UpdatedAt = s.now()
	if _, err := s.DB.CommitLogin(ctx, account, updatePassword, session); err != nil {
		return LoginResult{}, err
	}
	_ = s.DB.AddEvent(ctx, domain.Event{At: s.now(), Level: "info", Action: "login_saved", AccountID: account.ID, Message: "登录态已保存"})
	return LoginResult{Saved: true, Message: message, AppleID: firstNonEmpty(session.AppleID, account.AppleID)}, nil
}

func passwordOrSaved(given, saved string) string {
	if strings.TrimSpace(given) != "" {
		return given
	}
	return saved
}
