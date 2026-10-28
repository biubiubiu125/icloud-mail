package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestIMAPSearchIgnoresStaleSessionCursor(t *testing.T) {
	stale := LoginState{IMAPLastSyncUID: "100"}
	cmd := imapSearchCommand(stale, []Mailbox{{LastSyncUID: "15"}}, time.Time{}, 0)
	if cmd != "UID SEARCH UID 16:*" {
		t.Fatalf("mailbox cursor ignored: %s", cmd)
	}
	cmd = imapSearchCommand(stale, []Mailbox{{Email: "alias@icloud.com"}}, time.Time{}, 0)
	if strings.Contains(cmd, "UID 101") || strings.HasPrefix(cmd, "UID SEARCH UID") {
		t.Fatalf("stale session cursor still starts the search: %s", cmd)
	}
}

func TestIMAPSearchResetsWhenUIDValidityChanges(t *testing.T) {
	cmd := imapSearchCommand(LoginState{}, []Mailbox{{LastSyncUID: "uv:9:20"}}, time.Time{}, 9)
	if cmd != "UID SEARCH UID 21:*" {
		t.Fatalf("same validity = %s", cmd)
	}
	cmd = imapSearchCommand(LoginState{}, []Mailbox{{LastSyncUID: "uv:9:20"}}, time.Time{}, 10)
	if cmd != "UID SEARCH UID 1:*" {
		t.Fatalf("changed validity = %s", cmd)
	}
	cmd = imapSearchCommand(LoginState{}, []Mailbox{{LastSyncUID: "15"}}, time.Time{}, 9)
	if cmd != "UID SEARCH UID 1:*" {
		t.Fatalf("legacy cursor with a newly seen validity = %s", cmd)
	}
	if got := imapValidityResetCursor([]Mailbox{{LastSyncUID: "15"}}, 9, 40); got != "uv:9:40" {
		t.Fatalf("empty reset cursor = %s", got)
	}
	if got := imapValidityResetCursor([]Mailbox{{LastSyncUID: "uv:9:15"}}, 9, 40); got != "" {
		t.Fatalf("same validity empty search changed cursor = %s", got)
	}
}

func TestIMAPSelectReadsUIDValidity(t *testing.T) {
	lines := []string{"* OK [UIDVALIDITY 3857529045] UIDs valid", "* 1 EXISTS"}
	if got := imapSelectUIDValidity(lines); got != 3857529045 {
		t.Fatalf("validity = %d", got)
	}
}
