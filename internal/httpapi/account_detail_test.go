package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biubiubiu125/icloud-mail/internal/config"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func TestAccountListOmitsSecrets(t *testing.T) {
	item := domain.Account{
		ID: "acc-1", Label: "主号", AppleID: "owner@icloud.com",
		ApplePassword: "apple-secret", ProxyURL: "socks5://user:proxy-secret@1.2.3.4:1080",
		HasPassword: true, WebLoginSaved: true,
	}
	listed := accountListItemFrom(item)
	raw, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "apple-secret") || strings.Contains(text, "proxy-secret") || strings.Contains(text, "proxy_url") || strings.Contains(text, "1.2.3.4") {
		t.Fatalf("list leaked secrets: %s", text)
	}
	if !listed.HasPassword || !listed.HasProxy || listed.AppleID != "owner@icloud.com" {
		t.Fatalf("list flags = %#v", listed)
	}
}

func TestAccountDetailShowsSavedSecrets(t *testing.T) {
	item := domain.Account{
		ID: "acc-1", Label: "主号", AppleID: "owner@icloud.com",
		ApplePassword: "apple-secret", ProxyURL: "socks5://user:p%40ss@1.2.3.4:1080",
		HasPassword: true, IMAPSaved: true,
	}
	detail, err := accountDetailFrom(item)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Password != "apple-secret" || detail.ProxyScheme != "socks5" || detail.ProxyHost != "1.2.3.4" || detail.ProxyPort != "1080" || detail.ProxyUsername != "user" || detail.ProxyPassword != "p@ss" {
		t.Fatalf("detail = %#v", detail)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "imap_app_password") || strings.Contains(text, "session") || strings.Contains(text, "proxy_url") {
		t.Fatalf("detail leaked hidden fields: %s", text)
	}
}

func TestAccountPatchClearsPasswordWithoutTouchingLogin(t *testing.T) {
	cleared, err := accountPatchPlan(accountPatch{Label: "主号", AppleID: "owner@icloud.com", UpdatePassword: true, UpdateProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cleared.WritePassword || cleared.Password != "" || !cleared.WriteProxy || cleared.ProxyURL != "" {
		t.Fatalf("clear plan = %#v", cleared)
	}
	kept, err := accountPatchPlan(accountPatch{Label: "主号", AppleID: "owner@icloud.com"})
	if err != nil {
		t.Fatal(err)
	}
	if kept.WritePassword || kept.WriteProxy {
		t.Fatalf("omitted fields were written: %#v", kept)
	}
	changed, err := accountPatchPlan(accountPatch{
		AppleID: "next@icloud.com", Password: "new-secret", UpdatePassword: true,
		UpdateProxy: true, ProxyScheme: "socks5", ProxyHost: "1.2.3.4", ProxyPort: "1080",
		ProxyUsername: "user", ProxyPassword: "p@ss",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.WritePassword || changed.Password != "new-secret" || changed.ProxyURL == "" || !strings.Contains(changed.ProxyURL, "1.2.3.4:1080") {
		t.Fatalf("change plan = %#v", changed)
	}
	if _, err := accountPatchPlan(accountPatch{AppleID: "  "}); err == nil {
		t.Fatal("empty apple id was accepted")
	}
	if _, err := accountPatchPlan(accountPatch{AppleID: "owner@icloud.com", UpdateProxy: true, ProxyHost: "1.2.3.4"}); err == nil {
		t.Fatal("partial proxy was accepted")
	}
}

func TestAccountDetailRequiresAdmin(t *testing.T) {
	server := New(config.Config{}, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminPageRevealsSecretsOnlyInDetail(t *testing.T) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "/api/accounts/${id}") || !strings.Contains(page, "update_password") || !strings.Contains(page, "proxy_host") {
		t.Fatal("detail editor is missing")
	}
	login := page[strings.Index(page, "const start"):strings.Index(page, "card.querySelector(\".web-start\")")]
	if strings.Contains(login, "proxy_") || strings.Contains(login, "saved-password") {
		t.Fatal("login request uses saved credentials or proxy")
	}
}
