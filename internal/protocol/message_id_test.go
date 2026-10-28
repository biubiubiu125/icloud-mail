package protocol

import (
	"testing"
	"time"
)

func TestParseIMAPSavesMessageID(t *testing.T) {
	raw := []byte("From: sender@example.com\r\nTo: alias@icloud.com\r\nSubject: code\r\nMessage-ID:\r\n <Folded-42@Example.com>\r\nDate: Mon, 19 Oct 2026 12:00:00 +0000\r\n\r\nbody\r\n")
	message, _, ok := parseICloudIMAPMessage(iCloudIMAPFetchedMessage{UID: "9", Raw: raw})
	if !ok {
		t.Fatal("imap message was not parsed")
	}
	if message.MessageID != "<Folded-42@Example.com>" {
		t.Fatalf("message id = %q", message.MessageID)
	}
	if message.RemoteID != "imap:9" {
		t.Fatalf("remote id = %q", message.RemoteID)
	}
}

func TestParseIMAPWithoutDateDoesNotInventNow(t *testing.T) {
	before := time.Now()
	raw := []byte("From: sender@example.com\r\nTo: alias@icloud.com\r\nSubject: OpenAI code 333444\r\nMessage-ID: <otp@example.com>\r\n\r\nbody\r\n")
	message, _, ok := parseICloudIMAPMessage(iCloudIMAPFetchedMessage{UID: "4", Raw: raw})
	if !ok {
		t.Fatal("imap message was not parsed")
	}
	if !message.ReceivedAt.IsZero() || !message.ReceivedAt.Before(before) {
		t.Fatalf("received_at = %s", message.ReceivedAt)
	}
}

func TestMessageReceivedAtDoesNotInventNow(t *testing.T) {
	if got := messageReceivedAt(time.Time{}); !got.IsZero() {
		t.Fatalf("missing = %s", got)
	}
	known := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if !messageReceivedAt(known).Equal(known) {
		t.Fatalf("known = %s", messageReceivedAt(known))
	}
}

func TestPlanIMAPFetchKeepsCursorOnOldestWindow(t *testing.T) {
	fetch, cursor := planIMAPFetch([]int{40, 1, 2, 3}, 20)
	if cursor != "40" || !containsInt(fetch, 1) || !containsInt(fetch, 40) {
		t.Fatalf("small cursor = %q fetch=%v", cursor, fetch)
	}
	var uids []int
	for n := 1; n <= 40; n++ {
		uids = append(uids, n)
	}
	fetch, cursor = planIMAPFetch(uids, 20)
	if cursor != "20" {
		t.Fatalf("cursor jumped to %q", cursor)
	}
	if !containsInt(fetch, 1) || !containsInt(fetch, 20) || !containsInt(fetch, 40) || containsInt(fetch, 21) == false {
		t.Fatalf("fetch = %v", fetch)
	}
	if _, cursor = planIMAPFetch(nil, 20); cursor != "" {
		t.Fatalf("empty cursor = %q", cursor)
	}
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestIMAPKeepsMailOlderThanLastSyncOverlap(t *testing.T) {
	syncedAt := time.Date(2026, 10, 19, 12, 10, 0, 0, time.UTC)
	mailDate := syncedAt.Add(-3 * time.Minute)
	raw := []byte("From: sender@example.com\r\nTo: alias@icloud.com\r\nSubject: OpenAI code 333444\r\nDate: " + mailDate.Format(time.RFC1123Z) + "\r\nMessage-ID: <otp@example.com>\r\n\r\ncode 333444\r\n")
	got := iCloudIMAPMessagesByMailbox(
		[]iCloudIMAPFetchedMessage{{UID: "51", Raw: raw}},
		[]Mailbox{{ID: "mb1", Email: "alias@icloud.com", LastSyncAt: syncedAt, LastSyncUID: "50"}},
		time.Time{},
		"OpenAI",
	)
	if len(got["mb1"]) != 1 {
		t.Fatalf("dated mail kept = %d, want 1", len(got["mb1"]))
	}

	undated := []byte("From: sender@example.com\r\nTo: alias@icloud.com\r\nSubject: OpenAI code 555666\r\nMessage-ID: <nodate@example.com>\r\n\r\ncode 555666\r\n")
	got = iCloudIMAPMessagesByMailbox(
		[]iCloudIMAPFetchedMessage{{UID: "52", Raw: undated}},
		[]Mailbox{{ID: "mb1", Email: "alias@icloud.com", LastSyncAt: syncedAt}},
		time.Time{},
		"OpenAI",
	)
	if len(got["mb1"]) != 1 || !got["mb1"][0].ReceivedAt.IsZero() {
		t.Fatalf("undated kept = %d received=%s", len(got["mb1"]), receivedAtOrEmpty(got["mb1"]))
	}
}

func receivedAtOrEmpty(items []ICloudSyncedMessage) string {
	if len(items) == 0 {
		return "missing"
	}
	return items[0].ReceivedAt.Format(time.RFC3339Nano)
}

func TestWebMessageIDPrefersRFCHeader(t *testing.T) {
	header := "From: sender@example.com\r\nMessage-ID:\r\n <folded-42@example.com>\r\nSubject: 测试"
	if got := webMessageID(header, "internal-123"); got != "<folded-42@example.com>" {
		t.Fatalf("web message id = %q", got)
	}
	if got := webMessageID("", "message-42@example.com"); got != "message-42@example.com" {
		t.Fatalf("fallback message id = %q", got)
	}
}
