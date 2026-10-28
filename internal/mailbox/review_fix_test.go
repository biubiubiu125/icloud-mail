package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestRemoteListDoesNotClaimAnotherOrigin(t *testing.T) {
	if remoteListOwns(domain.OriginAppleAccount, domain.OriginICloudWeb) {
		t.Fatal("web list claimed an Apple Account mailbox")
	}
	if !remoteListOwns("", domain.OriginICloudWeb) {
		t.Fatal("empty origin was not treated as iCloud Web")
	}
	if remoteListOwns("", domain.OriginAppleAccount) {
		t.Fatal("empty origin was treated as Apple Account")
	}
	if !remoteListOwns("icloud_web", domain.OriginICloudWeb) {
		t.Fatal("origin comparison was case sensitive")
	}
}

func TestSyncWebRequestsWiderAliasWindow(t *testing.T) {
	provider := &webWindowProvider{}
	service := &Service{Provider: provider}
	err := service.syncWeb(context.Background(), protocol.ICloudSession{}, domain.Mailbox{ID: "box", Email: "alias@icloud.com"}, "OpenAI")
	if err == nil {
		t.Fatal("expected provider error after recording the window")
	}
	if provider.maxThreads < 50 {
		t.Fatalf("maxThreads = %d", provider.maxThreads)
	}
}

type webWindowProvider struct {
	maxThreads int
}

func (p *webWindowProvider) Delete(context.Context, protocol.ICloudSession, string, string, string) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, nil
}
func (p *webWindowProvider) Create(context.Context, protocol.ICloudSession, string, string, string, string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return protocol.ICloudRemoteMailbox{}, protocol.ICloudSession{}, nil
}
func (p *webWindowProvider) List(context.Context, protocol.ICloudSession, string, string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return nil, protocol.ICloudSession{}, nil
}
func (p *webWindowProvider) SyncIMAP(context.Context, protocol.LoginState, []protocol.Mailbox, time.Time, string, int) (map[string][]protocol.ICloudSyncedMessage, string, error) {
	return nil, "", nil
}
func (p *webWindowProvider) SyncWeb(_ context.Context, _ protocol.ICloudSession, _ protocol.Mailbox, _ time.Time, _ string, maxThreads int) ([]protocol.ICloudSyncedMessage, error) {
	p.maxThreads = maxThreads
	return nil, errors.New("stop")
}
func (p *webWindowProvider) CheckIMAP(context.Context, string, string, string) error { return nil }

func TestCreateHaltSurvivesProcessRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "halt.json")
	first := &Service{haltPath: path}
	first.recordCreateHalt("acc-1", domain.OriginICloudWeb, "persist failed", "anon-1", "a@icloud.com")
	if !first.createHalted("acc-1") {
		t.Fatal("halt was not remembered in memory")
	}
	second := &Service{haltPath: path}
	if err := second.restoreCreateHalts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !second.createHalted("acc-1") {
		t.Fatal("restart lost the create halt")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "acc-1") {
		t.Fatalf("halt file = %s err=%v", raw, err)
	}
}

func TestHaltReplaceKeepsPreviousFileUntilRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "halt.json")
	if err := os.WriteFile(path, []byte("old-halt"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := haltFileBeforeReplace
	haltFileBeforeReplace = func(dest string) {
		raw, err := os.ReadFile(dest)
		if err != nil || string(raw) != "old-halt" {
			t.Errorf("halt file was removed before replace: %q err=%v", raw, err)
		}
	}
	t.Cleanup(func() { haltFileBeforeReplace = previous })
	service := &Service{haltPath: path}
	if err := service.saveHaltFileUnlocked([]createHaltRecord{{AccountID: "acc-1"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "acc-1") {
		t.Fatalf("halt file = %s err=%v", raw, err)
	}
}

func TestClearReconciliationReleasesHalt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "halt.json")
	service := &Service{haltPath: path}
	service.recordCreateHalt("acc-1", domain.OriginICloudWeb, "persist failed", "anon-1", "a@icloud.com")
	service.releaseCreateHalt("acc-1")
	if service.createHalted("acc-1") {
		t.Fatal("memory halt remained after clear")
	}
	if _, err := os.Stat(path); err == nil {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "acc-1") {
			t.Fatalf("halt file still contains account: %s", raw)
		}
	}
}

func TestScheduledCreateFailureIsLogged(t *testing.T) {
	var buf strings.Builder
	service := &Service{logger: slog.New(slog.NewTextHandler(&buf, nil))}
	service.noteScheduledCreate("acc-1", errors.New("apple rejected create"))
	if service.createHalted("acc-1") {
		t.Fatal("ordinary create error halted the account")
	}
	if !strings.Contains(buf.String(), "apple rejected create") {
		t.Fatalf("log = %s", buf.String())
	}
	service.noteScheduledCreate("acc-1", apperr.New(500, "mailbox_create_reconciliation_persist_failed", "创建结果未落库，且待核对标记没有写入，请停止继续创建", true))
	if !service.createHalted("acc-1") {
		t.Fatal("persist failure did not halt")
	}
}

func TestSavedProxyRejectsPathAndQuery(t *testing.T) {
	if validateProxy("http://127.0.0.1:8080/path") == nil {
		t.Fatal("proxy path was accepted")
	}
	if validateProxy("socks5://127.0.0.1:1080?x=1") == nil {
		t.Fatal("proxy query was accepted")
	}
	if err := validateProxy("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteStopsWhenSettingsCannotLoad(t *testing.T) {
	key, err := deleteAPIKey(errors.New("db down"), "secret")
	if err == nil || key != "" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	key, err = deleteAPIKey(nil, "secret")
	if err != nil || key != "secret" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}
