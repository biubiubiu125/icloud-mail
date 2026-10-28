package mailbox

import (
	"testing"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestReconciliationTargetFound(t *testing.T) {
	account := domain.Account{ReconciliationAnonymousID: "anon-1", ReconciliationEmail: "Alias@icloud.com"}
	missing := []protocol.ICloudRemoteMailbox{{AnonymousID: "other", Email: "other@icloud.com"}}
	if reconciliationTargetFound(account, missing) {
		t.Fatal("missing target was treated as found")
	}
	byID := []protocol.ICloudRemoteMailbox{{AnonymousID: "anon-1", Email: "different@icloud.com"}}
	if !reconciliationTargetFound(account, byID) {
		t.Fatal("anonymous id match was missed")
	}
	byEmail := []protocol.ICloudRemoteMailbox{{AnonymousID: "other", Email: "alias@icloud.com"}}
	if !reconciliationTargetFound(account, byEmail) {
		t.Fatal("email match was missed")
	}
	emptyTarget := domain.Account{}
	if !reconciliationTargetFound(emptyTarget, nil) {
		t.Fatal("successful list with no target must be clearable")
	}
}

func TestTokenMatches(t *testing.T) {
	if domain.TokenMatches("", "secret") || domain.TokenMatches("secret", "") || domain.TokenMatches("", "") {
		t.Fatal("empty mailbox key matched")
	}
	if !domain.TokenMatches("secret", "secret") {
		t.Fatal("equal mailbox key did not match")
	}
	if domain.TokenMatches("secret", "Secret") {
		t.Fatal("different mailbox key matched")
	}
}
