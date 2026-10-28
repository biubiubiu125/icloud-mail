package mailbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

func TestTwoFactorStartDoesNotWriteAccount(t *testing.T) {
	svc := &Service{Now: func() time.Time { return time.Unix(100, 0).UTC() }}
	account := domain.Account{ID: "acc-1", AppleID: "new@icloud.com", ApplePassword: "old-secret"}
	result, err := svc.finishStart(context.Background(), account, "new-secret", protocol.LoginStart{
		Needs2FA:  true,
		PendingID: "pending-1",
		AppleID:   "new@icloud.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Needs2FA || result.Saved {
		t.Fatalf("result=%+v", result)
	}
	value, ok := svc.pendingOwners.Load("pending-1")
	if !ok {
		t.Fatal("pending owner missing")
	}
	owner, ok := value.(pendingOwner)
	if !ok || owner.accountID != "acc-1" || owner.appleID != "new@icloud.com" || owner.password != "new-secret" || !owner.updatePassword {
		t.Fatalf("pending=%+v", value)
	}
}

func TestLoginMismatchDoesNotClearSavedSession(t *testing.T) {
	db, account := openFollowupAccount(t)
	ctx := context.Background()
	keep := protocol.ICloudSession{
		AppleID: account.AppleID,
		Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{
			Kind:    protocol.LoginStateICloudWeb,
			Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes", Domain: "icloud.com"}},
		}},
	}
	if _, err := db.SaveSession(ctx, account.ID, keep); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		DB:      db,
		Auth:    mismatchAuth{},
		Pending: protocol.NewPendingLogins(),
		Now:     func() time.Time { return time.Unix(200, 0).UTC() },
	}
	_, err := svc.StartWebLogin(ctx, account.ID, "new@icloud.com", "new-secret", "")
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "session_account_mismatch" {
		t.Fatalf("err=%v", err)
	}
	saved, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AppleID != account.AppleID || saved.ApplePassword != account.ApplePassword {
		t.Fatalf("account changed before login saved: id=%s password=%s", saved.AppleID, saved.ApplePassword)
	}
	loaded, _, err := db.LoadSession(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionHasCookie(loaded, "KEEP") || sessionHasCookie(loaded, "NEW") {
		t.Fatalf("session=%+v", loaded)
	}
}

func TestFinishedLoginCommitsIdentityWithSession(t *testing.T) {
	db, account := openFollowupAccount(t)
	ctx := context.Background()
	keep := protocol.ICloudSession{
		AppleID: account.AppleID,
		Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{
			Kind:    protocol.LoginStateICloudWeb,
			Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes", Domain: "icloud.com"}},
		}},
	}
	if _, err := db.SaveSession(ctx, account.ID, keep); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		DB:      db,
		Auth:    twoFactorAuth{accountID: account.ID},
		Pending: protocol.NewPendingLogins(),
		Now:     func() time.Time { return time.Unix(300, 0).UTC() },
	}
	started, err := svc.StartWebLogin(ctx, account.ID, "new@icloud.com", "new-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	if !started.Needs2FA {
		t.Fatalf("start=%+v", started)
	}
	mid, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mid.AppleID != account.AppleID || mid.ApplePassword != account.ApplePassword {
		t.Fatalf("2FA start wrote account: id=%s", mid.AppleID)
	}
	loaded, _, err := db.LoadSession(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionHasCookie(loaded, "KEEP") {
		t.Fatal("2FA start cleared the saved session")
	}
	finished, err := svc.FinishWebLogin(ctx, account.ID, started.PendingID, "123456")
	if err != nil {
		t.Fatal(err)
	}
	if !finished.Saved {
		t.Fatalf("finish=%+v", finished)
	}
	after, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AppleID != "new@icloud.com" || after.ApplePassword != "new-secret" {
		t.Fatalf("committed identity id=%s password=%s", after.AppleID, after.ApplePassword)
	}
	loaded, _, err = db.LoadSession(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionHasCookie(loaded, "NEW") || len(loaded.Cookies) == 0 && len(loaded.LoginStates) == 0 {
		t.Fatalf("new session missing: %+v", loaded)
	}
}

func TestCreateDoesNotDeleteTrackedRemoteID(t *testing.T) {
	db, account := openFollowupAccount(t)
	ctx := context.Background()
	email := "tracked-" + account.ID + "@icloud.com"
	now := time.Now().UTC()
	if err := db.InsertMailbox(ctx, domain.Mailbox{
		ID: "box-" + account.ID, AccountID: account.ID, Email: email, Status: domain.StatusAvailable,
		APIToken: "token-" + account.ID, APIActive: true, ICloudActive: true,
		RemoteAnonymousID: "live-anon", RemoteOrigin: domain.OriginICloudWeb, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	provider := &rollbackProvider{remote: protocol.ICloudRemoteMailbox{
		AnonymousID: "live-anon", Email: email, Origin: domain.OriginICloudWeb,
	}}
	svc := &Service{DB: db, Provider: provider, Now: func() time.Time { return now }}
	_, err := svc.CreateMailbox(ctx, account.ID, "", "", "")
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "mailbox_exists" {
		t.Fatalf("err=%v", err)
	}
	if len(provider.deletes) != 0 {
		t.Fatalf("deleted tracked remote: %v", provider.deletes)
	}
	saved, ok, err := db.GetMailboxByEmail(ctx, email)
	if err != nil || !ok || saved.RemoteAnonymousID != "live-anon" {
		t.Fatalf("mailbox ok=%v id=%s err=%v", ok, saved.RemoteAnonymousID, err)
	}

	fresh := "fresh-" + account.ID + "@icloud.com"
	provider.remote = protocol.ICloudRemoteMailbox{AnonymousID: "new-anon", Email: email, Origin: domain.OriginICloudWeb}
	provider.deletes = nil
	_, err = svc.CreateMailbox(ctx, account.ID, "", "", "")
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "mailbox_exists" {
		t.Fatalf("fresh rollback err=%v", err)
	}
	if len(provider.deletes) != 1 || provider.deletes[0] != "new-anon" {
		t.Fatalf("new remote rollback=%v", provider.deletes)
	}
	if _, ok, err = db.GetMailboxByEmail(ctx, fresh); err != nil || ok {
		t.Fatalf("unexpected fresh mailbox ok=%v err=%v", ok, err)
	}
}

func TestWatchOnceLogsStoreErrors(t *testing.T) {
	var buf bytes.Buffer
	svc := &Service{
		logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		catalog: failWatchSettings{},
		Now:     func() time.Time { return time.Unix(0, 0).UTC() },
	}
	if got := svc.watchOnce(context.Background()); got != 30*time.Second {
		t.Fatalf("settings wait=%s", got)
	}
	if !strings.Contains(buf.String(), "watch settings down") {
		t.Fatalf("settings log=%s", buf.String())
	}
	buf.Reset()
	svc.catalog = failWatchList{}
	if got := svc.watchOnce(context.Background()); got != 60*time.Second {
		t.Fatalf("list wait=%s", got)
	}
	if !strings.Contains(buf.String(), "watch list down") {
		t.Fatalf("list log=%s", buf.String())
	}
	buf.Reset()
	svc.catalog = failSchedule{}
	if got := svc.scheduleOnce(context.Background()); got != time.Minute {
		t.Fatalf("schedule wait=%s", got)
	}
	if !strings.Contains(buf.String(), "schedule settings down") || strings.Contains(buf.String(), "postgres://") {
		t.Fatalf("schedule log=%s", buf.String())
	}
}

type mismatchAuth struct{}

func (mismatchAuth) StartICloudWebLogin(context.Context, string, string, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{
		Needs2FA: false,
		AppleID:  "new@icloud.com",
		Session: protocol.ICloudSession{
			AccountID: "other-account",
			AppleID:   "new@icloud.com",
			Cookies:   []protocol.SessionCookie{{Name: "NEW", Value: "no", Domain: "icloud.com"}},
		},
	}, nil
}
func (mismatchAuth) FinishICloudWebLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}
func (mismatchAuth) StartAppleAccountLogin(context.Context, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{}, errors.New("unused")
}
func (mismatchAuth) FinishAppleAccountLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}

type twoFactorAuth struct {
	accountID string
}

func (a twoFactorAuth) StartICloudWebLogin(context.Context, string, string, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{Needs2FA: true, PendingID: "pending-" + a.accountID, AppleID: "new@icloud.com", Message: "need code"}, nil
}
func (twoFactorAuth) FinishICloudWebLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{
		AppleID: "new@icloud.com",
		Cookies: []protocol.SessionCookie{{Name: "NEW", Value: "yes", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{
			Kind:    protocol.LoginStateICloudWeb,
			Cookies: []protocol.SessionCookie{{Name: "NEW", Value: "yes", Domain: "icloud.com"}},
		}},
	}, nil
}
func (twoFactorAuth) StartAppleAccountLogin(context.Context, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{}, errors.New("unused")
}
func (twoFactorAuth) FinishAppleAccountLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}

type rollbackProvider struct {
	remote  protocol.ICloudRemoteMailbox
	deletes []string
}

func (p *rollbackProvider) CheckIMAP(context.Context, string, string, string) error { return nil }
func (p *rollbackProvider) Delete(_ context.Context, _ protocol.ICloudSession, _, anonymousID, _ string) (protocol.ICloudSession, error) {
	p.deletes = append(p.deletes, anonymousID)
	return protocol.ICloudSession{}, nil
}
func (p *rollbackProvider) Create(context.Context, protocol.ICloudSession, string, string, string, string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return p.remote, protocol.ICloudSession{}, nil
}
func (p *rollbackProvider) List(context.Context, protocol.ICloudSession, string, string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return nil, protocol.ICloudSession{}, errors.New("unused")
}
func (p *rollbackProvider) SyncIMAP(context.Context, protocol.LoginState, []protocol.Mailbox, time.Time, string, int) (map[string][]protocol.ICloudSyncedMessage, string, error) {
	return nil, "", errors.New("unused")
}
func (p *rollbackProvider) SyncWeb(context.Context, protocol.ICloudSession, protocol.Mailbox, time.Time, string, int) ([]protocol.ICloudSyncedMessage, error) {
	return nil, errors.New("unused")
}

type failWatchSettings struct{}

func (failWatchSettings) GetSettings(context.Context) (domain.Settings, error) {
	return domain.Settings{}, errors.New("watch settings down")
}
func (failWatchSettings) ListMailboxes(context.Context, string, string) ([]domain.Mailbox, error) {
	return nil, errors.New("should not list")
}
func (failWatchSettings) ListAccounts(context.Context) ([]domain.Account, error) {
	return nil, errors.New("unused")
}

type failSchedule struct{}

func (failSchedule) GetSettings(context.Context) (domain.Settings, error) {
	return domain.Settings{}, errors.New("schedule settings down")
}
func (failSchedule) ListMailboxes(context.Context, string, string) ([]domain.Mailbox, error) {
	return nil, errors.New("unused")
}
func (failSchedule) ListAccounts(context.Context) ([]domain.Account, error) {
	return nil, errors.New("unused")
}

type failWatchList struct{}

func (failWatchList) GetSettings(context.Context) (domain.Settings, error) {
	return domain.Settings{WatcherEnabled: true, WatcherIntervalSeconds: 60}, nil
}
func (failWatchList) ListMailboxes(context.Context, string, string) ([]domain.Mailbox, error) {
	return nil, errors.New("watch list down")
}
func (failWatchList) ListAccounts(context.Context) ([]domain.Account, error) {
	return nil, errors.New("unused")
}

func sessionHasCookie(session protocol.ICloudSession, name string) bool {
	for _, cookie := range session.Cookies {
		if cookie.Name == name {
			return true
		}
	}
	for _, state := range session.LoginStates {
		for _, cookie := range state.Cookies {
			if cookie.Name == name {
				return true
			}
		}
	}
	return false
}

func openFollowupAccount(t *testing.T) (*store.Store, domain.Account) {
	t.Helper()
	dsn, err := followupDSN(os.Getenv("ICLOUD_MAIL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	account := domain.Account{
		ID:            "acc-follow-" + now.Format("20060102150405.000000000"),
		AppleID:       "old@icloud.com",
		ApplePassword: "old-secret",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	return db, account
}

func followupDSN(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	admin := *parsed
	admin.Path = "/postgres"
	target := *parsed
	target.Path = "/icloud_mail_followup"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'icloud_mail_followup')`).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		if _, err := conn.Exec(ctx, `CREATE DATABASE icloud_mail_followup`); err != nil {
			return "", err
		}
	}
	return target.String(), nil
}
