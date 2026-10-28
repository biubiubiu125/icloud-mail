package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestLoginIdentityChangeDropsOtherKinds(t *testing.T) {
	existing := protocol.ICloudSession{
		AppleID: "old@icloud.com",
		Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{
			{Kind: protocol.LoginStateICloudWeb, Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes"}}},
			{Kind: protocol.LoginStateICloudIMAP, IMAPEmail: "old@icloud.com", IMAPAppPassword: "old-app"},
			{Kind: protocol.LoginStateAppleAccount, APIKey: "old-key"},
		},
	}
	base := sessionBaseForLogin(existing, "old@icloud.com", "new@icloud.com")
	if len(base.LoginStates) != 0 || len(base.Cookies) != 0 || base.AppleID != "" {
		t.Fatalf("old kinds kept after identity change: %#v", base)
	}
	same := sessionBaseForLogin(existing, "Old@icloud.com", " old@icloud.com ")
	if len(same.LoginStates) != 3 {
		t.Fatal("case or space change dropped saved logins")
	}
	if sessionBaseForLogin(existing, "old@icloud.com", "").AppleID != "old@icloud.com" {
		t.Fatal("empty next apple id dropped the saved session")
	}
}

func TestCommitLoginDropsOldKindsWhenAppleIDChanges(t *testing.T) {
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
	account := domain.Account{
		ID:        "acc-login-id-" + now.Format("150405.000000"),
		AppleID:   "old@icloud.com",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, account.ID)
	}()
	if _, err := db.SaveSession(ctx, account.ID, protocol.ICloudSession{
		AppleID: "old@icloud.com",
		Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{
			{Kind: protocol.LoginStateICloudWeb, Cookies: []protocol.SessionCookie{{Name: "KEEP", Value: "yes"}}},
			{Kind: protocol.LoginStateICloudIMAP, IMAPEmail: "old@icloud.com", IMAPAppPassword: "old-app"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	account.AppleID = "new@icloud.com"
	account.ApplePassword = "new-secret"
	saved, err := db.CommitLogin(ctx, account, true, protocol.ICloudSession{
		AppleID: "new@icloud.com",
		Cookies: []protocol.SessionCookie{{Name: "NEW", Value: "yes", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{
			Kind:    protocol.LoginStateICloudWeb,
			Cookies: []protocol.SessionCookie{{Name: "NEW", Value: "yes", Domain: "icloud.com"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.AppleID != "new@icloud.com" || len(saved.LoginStates) != 1 || saved.LoginStates[0].Kind != protocol.LoginStateICloudWeb {
		t.Fatalf("committed session = %#v", saved)
	}
	for _, state := range saved.LoginStates {
		if state.Kind == protocol.LoginStateICloudIMAP {
			t.Fatal("old IMAP survived an Apple ID change")
		}
	}
	loaded, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AppleID != "new@icloud.com" || loaded.ApplePassword != "new-secret" {
		t.Fatalf("account = %#v", loaded)
	}
}
