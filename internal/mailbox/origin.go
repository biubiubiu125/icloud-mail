package mailbox

import (
	"strings"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func remoteListOwns(storedOrigin, listOrigin string) bool {
	stored := strings.ToUpper(strings.TrimSpace(storedOrigin))
	list := strings.ToUpper(strings.TrimSpace(listOrigin))
	if stored == "" {
		stored = domain.OriginICloudWeb
	}
	return stored != "" && stored == list
}
