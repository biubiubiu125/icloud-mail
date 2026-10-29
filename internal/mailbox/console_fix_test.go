package mailbox

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

func TestCreateMailboxRejectsWhileAccountIsBusy(t *testing.T) {
	svc := &Service{}
	unlock := svc.lockAccount("acc-busy")
	done := make(chan error, 1)
	go func() {
		_, err := svc.CreateMailbox(context.Background(), "acc-busy", "", "", "")
		done <- err
	}()
	select {
	case err := <-done:
		unlock()
		var app *apperr.Error
		if !errors.As(err, &app) || app.Code != "mailbox_create_in_progress" || app.HTTP != 409 {
			t.Fatalf("second create = %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("second create waited on the account lock")
	}
}

type partialListProvider struct {
	remotes []protocol.ICloudRemoteMailbox
}

func (partialListProvider) CheckIMAP(context.Context, string, string, string) error { return nil }
func (partialListProvider) Delete(context.Context, protocol.ICloudSession, string, string, string) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}
func (partialListProvider) Create(context.Context, protocol.ICloudSession, string, string, string, string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return protocol.ICloudRemoteMailbox{}, protocol.ICloudSession{}, errors.New("unused")
}
func (p partialListProvider) List(context.Context, protocol.ICloudSession, string, string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return p.remotes, protocol.ICloudSession{
		Cookies:     []protocol.SessionCookie{{Name: "X", Value: "1", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{Kind: protocol.LoginStateICloudWeb, Cookies: []protocol.SessionCookie{{Name: "X", Value: "1", Domain: "icloud.com"}}}},
	}, nil
}
func (partialListProvider) SyncIMAP(context.Context, protocol.LoginState, []protocol.Mailbox, time.Time, string, int) (map[string][]protocol.ICloudSyncedMessage, string, error) {
	return nil, "", errors.New("unused")
}
func (partialListProvider) SyncWeb(context.Context, protocol.ICloudSession, protocol.Mailbox, time.Time, string, int) ([]protocol.ICloudSyncedMessage, error) {
	return nil, errors.New("unused")
}

func TestSyncKeepsImportedCountWhenLaterInsertFails(t *testing.T) {
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
	suffix := now.Format("150405.000000000")
	owner := domain.Account{ID: "acc-sync-" + suffix, AppleID: "owner-" + suffix + "@icloud.com", CreatedAt: now, UpdatedAt: now}
	other := domain.Account{ID: "acc-other-" + suffix, AppleID: "other-" + suffix + "@icloud.com", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAccount(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAccount(ctx, other); err != nil {
		t.Fatal(err)
	}
	session := protocol.ICloudSession{
		AppleID: owner.AppleID,
		Cookies: []protocol.SessionCookie{{Name: "X", Value: "1", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{
			Kind:    protocol.LoginStateICloudWeb,
			Cookies: []protocol.SessionCookie{{Name: "X", Value: "1", Domain: "icloud.com"}},
		}},
	}
	if _, err := db.SaveSession(ctx, owner.ID, session); err != nil {
		t.Fatal(err)
	}
	conflict := "conflict-" + suffix + "@icloud.com"
	if err := db.InsertMailbox(ctx, domain.Mailbox{
		ID: "box-conflict-" + suffix, AccountID: other.ID, Email: conflict, Status: domain.StatusAvailable,
		APIToken: "token-" + suffix, APIActive: true, ICloudActive: true, RemoteOrigin: domain.OriginICloudWeb, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fresh := "fresh-" + suffix + "@icloud.com"
	svc := &Service{DB: db, Provider: partialListProvider{remotes: []protocol.ICloudRemoteMailbox{
		{Email: fresh, AnonymousID: "anon-fresh", Origin: domain.OriginICloudWeb, IsActive: true},
		{Email: conflict, AnonymousID: "anon-conflict", Origin: domain.OriginICloudWeb, IsActive: true},
	}}, Now: func() time.Time { return now }}
	n, err := svc.SyncAccountMailboxes(ctx, owner.ID)
	if err == nil {
		t.Fatal("conflicted import returned success")
	}
	if n != 1 {
		t.Fatalf("imported = %d, want 1 with the error", n)
	}
	rows, err := db.ListMailboxes(ctx, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Email != fresh {
		t.Fatalf("stored mailboxes = %#v", rows)
	}

	if err := db.MarkReconciliation(ctx, owner.ID, domain.OriginICloudWeb, "待核对", "anon-fresh", fresh, now); err != nil {
		t.Fatal(err)
	}
	again := "again-" + suffix + "@icloud.com"
	svc.Provider = partialListProvider{remotes: []protocol.ICloudRemoteMailbox{
		{Email: again, AnonymousID: "anon-again", Origin: domain.OriginICloudWeb, IsActive: true},
		{Email: conflict, AnonymousID: "anon-conflict", Origin: domain.OriginICloudWeb, IsActive: true},
	}}
	reconciled, err := svc.reconcileLocked(ctx, owner.ID)
	if err == nil {
		t.Fatal("reconcile ignored the conflict")
	}
	if reconciled != 1 {
		t.Fatalf("reconcile imported = %d, want 1", reconciled)
	}
}
