package protocol

import (
	"testing"
	"time"
)

func TestBodyOnlyVerificationIsKept(t *testing.T) {
	items := []ICloudSyncedMessage{{
		Subject: "hello",
		Body:    "your code is 123456",
	}}
	kept := keepVerificationMessages(items, "OpenAI")
	if len(kept) != 1 {
		t.Fatalf("kept = %d", len(kept))
	}
}

func TestPreviewMissStillNeedsBody(t *testing.T) {
	thread := mailThread{Subject: "hello", Preview: "no keyword here"}
	if !threadNeedsBodyFetch(thread, time.Time{}) {
		t.Fatal("preview miss skipped the body fetch")
	}
}
