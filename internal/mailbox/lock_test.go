package mailbox

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

type lockProvider struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *lockProvider) CheckIMAP(context.Context, string, string, string) error {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		<-p.release
	}
	return nil
}

func (p *lockProvider) Delete(context.Context, protocol.ICloudSession, string, string, string) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}
func (p *lockProvider) Create(context.Context, protocol.ICloudSession, string, string, string, string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return protocol.ICloudRemoteMailbox{}, protocol.ICloudSession{}, errors.New("unused")
}
func (p *lockProvider) List(context.Context, protocol.ICloudSession, string, string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return nil, protocol.ICloudSession{}, errors.New("unused")
}
func (p *lockProvider) SyncIMAP(context.Context, protocol.LoginState, []protocol.Mailbox, time.Time, string, int) (map[string][]protocol.ICloudSyncedMessage, string, error) {
	return nil, "", errors.New("unused")
}
func (p *lockProvider) SyncWeb(context.Context, protocol.ICloudSession, protocol.Mailbox, time.Time, string, int) ([]protocol.ICloudSyncedMessage, error) {
	return nil, errors.New("unused")
}

type lockAuth struct {
	svc *Service
	id  string
}

func (a lockAuth) StartICloudWebLogin(context.Context, string, string, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	value, ok := a.svc.locks.Load(a.id)
	if !ok {
		return protocol.LoginStart{}, errors.New("lock missing")
	}
	mu := value.(*sync.Mutex)
	if mu.TryLock() {
		mu.Unlock()
		return protocol.LoginStart{}, errors.New("account lock was not held")
	}
	return protocol.LoginStart{Needs2FA: true, PendingID: "pending-1", Message: "held"}, nil
}

func (lockAuth) FinishICloudWebLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}
func (lockAuth) StartAppleAccountLogin(context.Context, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{}, errors.New("unused")
}
func (lockAuth) FinishAppleAccountLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}

func TestLoginAndIMAPHoldAccountLock(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	account := domain.Account{ID: "acc-lock-" + now.Format("20060102150405.000000000"), AppleID: "owner@icloud.com", ApplePassword: "secret-pass", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	provider := &lockProvider{entered: make(chan struct{}), release: make(chan struct{})}
	svc := &Service{DB: db, Provider: provider, Pending: protocol.NewPendingLogins(), Now: func() time.Time { return now }}
	svc.Auth = lockAuth{svc: svc, id: account.ID}

	if _, err := svc.StartWebLogin(ctx, account.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- svc.SaveIMAP(ctx, account.ID, "owner@icloud.com", "abcd-efgh-ijkl-mnop")
	}()
	select {
	case <-provider.entered:
	case err := <-firstDone:
		t.Fatalf("first IMAP finished before entering provider: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first IMAP did not enter the provider")
	}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- svc.SaveIMAP(ctx, account.ID, "owner@icloud.com", "abcd-efgh-ijkl-mnop")
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second IMAP overlapped the account lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 while locked", provider.calls.Load())
	}
	close(provider.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second IMAP did not finish after the lock was released")
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls.Load())
	}
}
