package mailbox

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

// PersistContext stays usable after the caller disconnects. Create marks and
// remote-delete status writes must not be canceled by the request that failed.
func PersistContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
}

var persistContext = PersistContext

func persistCall(parent context.Context, fn func(context.Context) error) error {
	ctx, cancel := persistContext(parent)
	defer cancel()
	return fn(ctx)
}

func remoteDeleteMark(callErr error) (string, string) {
	if callErr == nil {
		return domain.DeleteSucceeded, ""
	}
	status := domain.DeleteFailed
	if protocol.UncertainProviderResult(callErr) {
		status = domain.DeleteUnknown
	}
	return status, protocol.PublicMessage(callErr)
}

func abortUntrackedCreate(cause error, mark func() error) error {
	if cause == nil {
		return nil
	}
	if markErr := mark(); markErr != nil {
		return markErr
	}
	return cause
}

func (s *Service) haltCreate(accountID string) {
	s.recordCreateHalt(accountID, "", "", "", "")
}

func (s *Service) createHalted(accountID string) bool {
	if s == nil {
		return false
	}
	_, ok := s.haltedAccounts.Load(accountID)
	return ok
}

func createPersistFailed() error {
	return apperr.New(500, "mailbox_create_reconciliation_persist_failed", "创建结果未落库，且待核对标记没有写入，请停止继续创建", true)
}

func logBackground(logger *slog.Logger, task, action string, err error) {
	if logger == nil || err == nil {
		return
	}
	logger.Error("background task failed", "task", task, "action", action, "error", backgroundErrText(err))
}

func (s *Service) logBackground(task, action string, err error) {
	if s == nil {
		return
	}
	logBackground(s.logger, task, action, err)
}

func backgroundErrText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "password=") || strings.Contains(lower, "postgres://") || strings.Contains(lower, "postgresql://") {
		return "database error"
	}
	return text
}
