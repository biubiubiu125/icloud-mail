package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/config"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func TestSettingsPayloadUsesSameBaseAsExport(t *testing.T) {
	server := New(config.Config{PublicBaseURL: "https://env.example/"}, nil, nil, nil)
	empty := server.settingsPayload(domain.Settings{})
	if empty["effective_public_base_url"] != "https://env.example" {
		t.Fatalf("fallback payload = %#v", empty)
	}
	stored := server.settingsPayload(domain.Settings{PublicBaseURL: "https://settings.example/"})
	if stored["effective_public_base_url"] != "https://settings.example" {
		t.Fatalf("stored payload = %#v", stored)
	}
	source, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "settingsPayload(") {
		t.Fatal("settings handler does not return the effective base")
	}
}

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
	if !strings.Contains(page, "two_factor_method") || !strings.Contains(page, "api_active") || !strings.Contains(page, "邮箱池") {
		t.Fatal("console controls are missing")
	}
	for _, forbidden := range []string{"/manage", "/api/update", "bulk-delete", "bulk-status", "runtime/export", "remote-clean", "cdn."} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("forbidden UI entry %q", forbidden)
		}
	}
	start := strings.Index(page, "const start")
	web := strings.Index(page, "card.querySelector(\".web-start\")")
	if start < 0 || web < 0 || start > web {
		t.Fatal("login start markers missing")
	}
	login := page[start:web]
	if strings.Contains(login, "proxy_") || strings.Contains(login, "saved-password") {
		t.Fatal("login request uses saved credentials or proxy")
	}
	if strings.Contains(login, "apple_id") || !strings.Contains(login, "two_factor_method") {
		t.Fatal("login request must send only the login password and two factor method")
	}
}

func TestAdminPageGuardsReviewedFlows(t *testing.T) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, `id="save-settings" type="button" disabled`) || !strings.Contains(page, "settingsReady") {
		t.Fatal("settings can be saved before the current values load")
	}
	if !strings.Contains(page, "effective_public_base_url") {
		t.Fatal("copied API URL does not use the server effective base")
	}
	if !strings.Contains(page, "data-local-cleanup") || !strings.Contains(page, "dataset.localCleanup") {
		t.Fatal("already deleted remote mailbox still confirms a remote delete")
	}
	if !strings.Contains(page, "pending.channel") || !strings.Contains(page, "loginChannel") {
		t.Fatal("verification submit does not stay on the login channel")
	}
	if !strings.Contains(page, "function withBusy") {
		t.Fatal("repeated clicks are not blocked while a request is running")
	}
	exportAt := strings.Index(page, `$("export").onclick`)
	if exportAt < 0 || !strings.Contains(page[exportAt:], "\u5bfc\u51fa\u5931\u8d25\uff1a") {
		t.Fatal("export failure is not reported")
	}
	if !strings.Contains(page, `refreshAfterWrite("已导入 "`) {
		t.Fatal("sync result is cleared by the following refresh")
	}
}

func TestAdminPageEnablesMatchingLoginSubmit(t *testing.T) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	start := strings.Index(page, "function applyPendingChannel")
	end := strings.Index(page, "function noteAccount")
	if start < 0 || end < start {
		t.Fatal("channel helper missing")
	}
	fn := page[start:end]
	if !strings.Contains(fn, "button.dataset.busy === \"1\"") || !strings.Contains(fn, "button.dataset.hold === \"1\"") {
		t.Fatal("in-flight or held submit can be re-enabled by a channel switch")
	}
	if !strings.Contains(fn, "button.disabled = channel !== \"\" && !match") {
		t.Fatal("switching login channel leaves the new submit button disabled")
	}
}

func TestAdminPageKeepsWriteResultWhenRefreshFails(t *testing.T) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "function refreshAfterWrite") || !strings.Contains(page, "dataset.hold") || !strings.Contains(page, "hold = await action() === true") {
		t.Fatal("a successful write can be retried when the following refresh fails")
	}
	loadAll := page[strings.Index(page, "async function loadAll"):strings.Index(page, "function renderOverview")]
	if !strings.Contains(loadAll, "releaseHeldButtons()") {
		t.Fatal("held buttons stay disabled after a later successful refresh")
	}
	for _, marker := range []string{
		`$("create-account").onclick`,
		`$("create-mailbox").onclick`,
		`card.querySelector(".create-box").onclick`,
		`button.classList.contains("toggle-api")`,
		`button.classList.contains("save-mailbox")`,
		`button.classList.contains("delete-box")`,
	} {
		at := strings.Index(page, marker)
		if at < 0 {
			t.Fatalf("missing handler %s", marker)
		}
		window := page[at:]
		if next := strings.Index(window[len(marker):], "onclick"); next >= 0 {
			window = window[:len(marker)+next]
		}
		if !strings.Contains(window, "refreshAfterWrite(") {
			t.Fatalf("%s still treats refresh failure as a failed write", marker)
		}
	}
	if strings.Contains(page, `say("已创建", true)`) || strings.Contains(page, `noteAccount(id, "已创建", true)`) {
		t.Fatal("create success is announced before refresh and can be overwritten")
	}
	if !strings.Contains(page, "err.data = data") || !strings.Contains(page, "但同步未完成") {
		t.Fatal("partial sync hides imported mailboxes")
	}
}

func TestSyncFailureBodyKeepsImportedCount(t *testing.T) {
	status, body := syncFailureBody(3, apperr.New(409, "icloud_session_missing", "未保存可用于同步的登录态", true))
	if status != 409 || body["success"] != false || body["imported"] != 3 || body["error"] == "" {
		t.Fatalf("partial sync body = %#v status %d", body, status)
	}
	status, body = syncFailureBody(0, apperr.New(409, "icloud_session_missing", "未保存可用于同步的登录态", true))
	if status != 409 {
		t.Fatalf("status = %d", status)
	}
	if _, ok := body["imported"]; ok {
		t.Fatal("zero import should not claim rows were written")
	}
}

func TestAdminPageKeepsCreateLockedAcrossRefresh(t *testing.T) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if strings.Contains(page, `value="disabled"`) {
		t.Fatal("disabled status filter never matches a real mailbox")
	}
	if !strings.Contains(page, `value="off"`) {
		t.Fatal("api off filter is missing")
	}
	if !strings.Contains(page, "const createLocks") || !strings.Contains(page, "function applyCreateLocks") || !strings.Contains(page, "function releaseCreateHolds") {
		t.Fatal("create lock does not survive a list refresh")
	}
	loadAll := page[strings.Index(page, "async function loadAll"):strings.Index(page, "function renderOverview")]
	if !strings.Contains(loadAll, "releaseCreateHolds()") || !strings.Contains(loadAll, "applyCreateLocks()") {
		t.Fatal("refresh replaces a busy create button with an enabled one")
	}
	for _, marker := range []string{`$("create-mailbox").onclick`, `card.querySelector(".create-box").onclick`} {
		at := strings.Index(page, marker)
		if at < 0 {
			t.Fatalf("missing %s", marker)
		}
		window := page[at:]
		if next := strings.Index(window[len(marker):], "onclick"); next >= 0 {
			window = window[:len(marker)+next]
		}
		if !strings.Contains(window, "createLocks[accountID]") || !strings.Contains(window, `createLocks[accountID] = "busy"`) || !strings.Contains(window, `createLocks[accountID] = "hold"`) {
			t.Fatalf("%s can create again while another create is in flight", marker)
		}
	}
	finishAt := strings.Index(page, "const finish = async")
	if finishAt < 0 {
		t.Fatal("finish helper missing")
	}
	finish := page[finishAt:]
	if next := strings.Index(finish[len("const finish = async"):], "card.querySelector(\".web-finish\")"); next >= 0 {
		finish = finish[:len("const finish = async")+next]
	}
	if !strings.Contains(finish, "const submitted = pending.id") || !strings.Contains(finish, "pending_id:submitted") || !strings.Contains(finish, "pendingFor(id).id !== submitted") {
		t.Fatal("finishing an older login clears a newer verification challenge")
	}
}

func TestAccountSaveDoesNotSplitProxy(t *testing.T) {
	data, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, "func (s *Server) handleUpdateAccount")
	end := strings.Index(source, "func (s *Server) handleUpdateProxy")
	if start < 0 || end < start {
		t.Fatal("account save handler missing")
	}
	fn := source[start:end]
	if strings.Contains(fn, ".UpdateAccount(") || strings.Contains(fn, ".UpdateProxy(") {
		t.Fatal("account profile and proxy are still separate writes")
	}
	if !strings.Contains(fn, "SaveAccountPatch(") {
		t.Fatal("account save does not use one patch")
	}
}

func TestAccountDetailShowsSavedIMAPEmail(t *testing.T) {
	item := domain.Account{
		ID: "acc-1", AppleID: "owner@icloud.com", IMAPEmail: "hide@icloud.com",
	}
	detail, err := accountDetailFrom(item)
	if err != nil {
		t.Fatal(err)
	}
	if detail.IMAPEmail != "hide@icloud.com" {
		t.Fatalf("detail imap email = %q", detail.IMAPEmail)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "\"imap_email\":\"hide@icloud.com\"") {
		t.Fatalf("detail json = %s", text)
	}
	if strings.Contains(text, "imap_app_password") || strings.Contains(text, "app-secret") {
		t.Fatalf("detail leaked the app password: %s", text)
	}
	listed, err := json.Marshal(accountListItemFrom(item))
	if err != nil {
		t.Fatal(err)
	}
	listText := string(listed)
	if strings.Contains(listText, "hide@icloud.com") || strings.Contains(listText, "imap_email") {
		t.Fatalf("list leaked the saved imap email: %s", listText)
	}
	accountRaw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(accountRaw), "hide@icloud.com") || strings.Contains(string(accountRaw), "imap_email") {
		t.Fatalf("shared account json leaked the imap email: %s", accountRaw)
	}
}

func TestAdminPagePrefillsSavedIMAPEmail(t *testing.T) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if strings.Contains(page, `class="imap-email" value="${esc(item.apple_id)}"`) {
		t.Fatal("imap email input still prefills only the apple id")
	}
	if !strings.Contains(page, `class="imap-email" value="${esc(item.imap_email || item.apple_id)}"`) {
		t.Fatal("imap email input does not prefer the saved address")
	}
}
