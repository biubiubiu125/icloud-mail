package mailbox

import (
	"context"
	"testing"
)

func TestRestoreContextSurvivesParentCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, stop := RestoreContext(parent)
	defer stop()
	if err := ctx.Err(); err != nil {
		t.Fatalf("restore context canceled: %v", err)
	}
}
