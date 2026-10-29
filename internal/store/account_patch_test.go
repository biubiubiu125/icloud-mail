package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestSaveAccountPatchWritesProfileAndProxyTogether(t *testing.T) {
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
	id := "acc-patch-" + now.Format("150405.000000000")
	account := domain.Account{
		ID: id, Label: "旧名", AppleID: "old-" + id + "@icloud.com", ApplePassword: "old-pass",
		ProxyURL: "http://old.example:8080", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveSession(ctx, id, protocol.ICloudSession{
		AppleID: account.AppleID,
		Cookies: []protocol.SessionCookie{{Name: "OLD", Value: "1", Domain: "icloud.com"}},
		LoginStates: []protocol.LoginState{{
			Kind:    protocol.LoginStateICloudWeb,
			Cookies: []protocol.SessionCookie{{Name: "OLD", Value: "1", Domain: "icloud.com"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	account.Label = "新名"
	account.AppleID = "new-" + id + "@icloud.com"
	account.ApplePassword = "new-pass"
	account.UpdatedAt = now.Add(time.Minute)
	if err := db.SaveAccountPatch(ctx, account, true, true, "socks5://new.example:1080"); err != nil {
		t.Fatal(err)
	}
	saved, ok, err := db.GetAccount(ctx, id)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if saved.Label != "新名" || saved.AppleID != account.AppleID || saved.ApplePassword != "new-pass" || saved.ProxyURL != "socks5://new.example:1080" {
		t.Fatalf("saved = %#v", saved)
	}
	loaded, _, err := db.LoadSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cookies) != 0 || len(loaded.LoginStates) != 0 {
		t.Fatalf("session survived apple id change: %#v", loaded)
	}
	if loaded.ProxyURL != "socks5://new.example:1080" {
		t.Fatalf("session proxy = %q", loaded.ProxyURL)
	}
	account.Label = "只改名"
	account.UpdatedAt = now.Add(2 * time.Minute)
	if err := db.SaveAccountPatch(ctx, account, false, false, "http://ignored.example:1"); err != nil {
		t.Fatal(err)
	}
	kept, _, err := db.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Label != "只改名" || kept.ApplePassword != "new-pass" || kept.ProxyURL != "socks5://new.example:1080" {
		t.Fatalf("unchanged fields were overwritten: %#v", kept)
	}
}
