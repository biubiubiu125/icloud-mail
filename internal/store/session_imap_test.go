package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestSessionIMAPEmailUsesSavedAddress(t *testing.T) {
	saved := []byte(`{"apple_id":"owner@icloud.com","login_states":[{"kind":"icloud_imap","imap_email":"Hide@icloud.com","imap_app_password":"abcd-efgh-ijkl-mnop"}]}`)
	if got := sessionIMAPEmail(saved); got != "hide@icloud.com" {
		t.Fatalf("saved email = %q", got)
	}
	if got := sessionIMAPEmail([]byte(`{"login_states":[{"kind":"icloud_imap","imap_email":"hide@icloud.com"}]}`)); got != "" {
		t.Fatalf("passwordless login returned %q", got)
	}
	fallback := []byte(`{"apple_id":"Owner@icloud.com","login_states":[{"kind":"icloud_imap","imap_app_password":"abcd-efgh"}]}`)
	if got := sessionIMAPEmail(fallback); got != "owner@icloud.com" {
		t.Fatalf("empty stored email = %q", got)
	}
	if got := sessionIMAPEmail([]byte(`{}`)); got != "" {
		t.Fatalf("empty session returned %q", got)
	}
	if got := sessionIMAPEmail([]byte(`{"login_states":[{"kind":"icloud_web","cookies":[{"name":"a","value":"b"}]}]}`)); got != "" {
		t.Fatalf("web login returned %q", got)
	}
}

func TestGetAccountReturnsSavedIMAPEmail(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := "acc-imap-" + now.Format("150405.000000000")
	account := domain.Account{
		ID: id, AppleID: "owner-" + id + "@icloud.com", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveSession(ctx, id, protocol.ICloudSession{
		AppleID: account.AppleID,
		LoginStates: []protocol.LoginState{{
			Kind: protocol.LoginStateICloudIMAP, IMAPEmail: "Hide-" + id + "@icloud.com", IMAPAppPassword: "abcd-efgh-ijkl-mnop",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := db.GetAccount(ctx, id)
	if err != nil || !ok {
		t.Fatalf("loaded=%v err=%v", ok, err)
	}
	if loaded.IMAPEmail != "hide-"+id+"@icloud.com" {
		t.Fatalf("stored imap email = %q", loaded.IMAPEmail)
	}
	if loaded.ApplePassword != "" && loaded.IMAPEmail == loaded.ApplePassword {
		t.Fatal("imap email echoed the apple password")
	}
}
