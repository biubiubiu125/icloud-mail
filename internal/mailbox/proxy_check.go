package mailbox

import (
	"strings"

	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func validateProxy(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, "://") {
		return errBadProxy
	}
	if _, err := protocol.CheckProxyURL(raw); err != nil {
		return errBadProxy
	}
	return nil
}

type proxyError struct{}

func (proxyError) Error() string { return "invalid proxy" }

var errBadProxy = proxyError{}
