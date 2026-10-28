package mailbox

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

type loginClient interface {
	StartICloudWebLogin(ctx context.Context, appleID, password, defaultHost, clientID, twoFactorMethod, proxyURL string, pending *protocol.PendingLogins) (protocol.LoginStart, error)
	FinishICloudWebLogin(ctx context.Context, pendingID, code string, pending *protocol.PendingLogins) (protocol.ICloudSession, error)
	StartAppleAccountLogin(ctx context.Context, appleID, password, twoFactorMethod, proxyURL string, pending *protocol.PendingLogins) (protocol.LoginStart, error)
	FinishAppleAccountLogin(ctx context.Context, pendingID, code string, pending *protocol.PendingLogins) (protocol.ICloudSession, error)
}

type Service struct {
	DB             *store.Store
	Provider       Provider
	Auth           loginClient
	Pending        *protocol.PendingLogins
	Now            func() time.Time
	logger         *slog.Logger
	expirer        backgroundExpirer
	catalog        backgroundCatalog
	locks          sync.Map
	pendingOwners  sync.Map
	haltedAccounts sync.Map
	haltPath       string
	haltFileMu     sync.Mutex
}

func NewService(db *store.Store, provider Provider) *Service {
	if provider == nil {
		provider = NewProvider()
	}
	return &Service{
		DB:       db,
		Provider: provider,
		Auth:     protocol.NewAppleAuthClient(),
		Pending:  protocol.NewPendingLogins(),
		Now:      time.Now,
		haltPath: defaultHaltPath(),
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) lockAccount(id string) func() {
	value, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) PublicBaseURL(ctx context.Context, fallback string) string {
	settings, err := s.DB.GetSettings(ctx)
	if err == nil && strings.TrimSpace(settings.PublicBaseURL) != "" {
		return strings.TrimRight(settings.PublicBaseURL, "/")
	}
	return strings.TrimRight(strings.TrimSpace(fallback), "/")
}

func JoinAPIURL(base, email, token string) string {
	path := "/api/v1/mailboxes/" + email + "/code?key=" + token
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return path
	}
	return base + path
}

func (s *Service) APIURL(base, email, token string) string {
	return JoinAPIURL(base, email, token)
}

func (s *Service) Claim(ctx context.Context, in domain.ClaimInput) (domain.ClaimResult, error) {
	settings, err := s.DB.GetSettings(ctx)
	if err != nil {
		return domain.ClaimResult{}, err
	}
	if !settings.PublicAPIEnabled {
		return domain.ClaimResult{}, apperr.New(403, "public_api_disabled", "公开接码接口已关闭", false)
	}
	if in.TTL <= 0 {
		in.TTL = domain.LeaseTTL(settings.LeaseTTLMinutes)
	}
	in.Now = s.now()
	result, err := s.DB.Claim(ctx, in)
	if err != nil {
		return domain.ClaimResult{}, err
	}
	if result.Created {
		_ = s.DB.AddEvent(ctx, domain.Event{At: in.Now, Level: "info", Action: "mailbox_claimed", AccountID: result.Mailbox.AccountID, MailboxID: result.Mailbox.ID, Message: "邮箱已领取"})
	}
	return result, nil
}

func (s *Service) requirePublicAPI(ctx context.Context) error {
	settings, err := s.DB.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.PublicAPIEnabled {
		return apperr.New(403, "public_api_disabled", "公开接码接口已关闭", false)
	}
	return nil
}

func (s *Service) Lookup(ctx context.Context, emails []string) ([]domain.Mailbox, []string, error) {
	if err := s.requirePublicAPI(ctx); err != nil {
		return nil, nil, err
	}
	if len(emails) == 0 || len(emails) > 500 {
		return nil, nil, apperr.New(400, "lookup_size_invalid", "一次查询需要 1 到 500 个邮箱", false)
	}
	var found []domain.Mailbox
	var missing []string
	seen := map[string]struct{}{}
	for _, email := range emails {
		email = domain.NormalizeEmail(email)
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		item, ok, err := s.DB.GetMailboxByEmail(ctx, email)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			missing = append(missing, email)
			continue
		}
		item.APIToken = ""
		found = append(found, item)
	}
	if found == nil {
		found = []domain.Mailbox{}
	}
	if missing == nil {
		missing = []string{}
	}
	return found, missing, nil
}

func (s *Service) CommitLease(ctx context.Context, id, mailboxKey string) (domain.Lease, error) {
	if err := s.requirePublicAPI(ctx); err != nil {
		return domain.Lease{}, err
	}
	return s.DB.CommitLease(ctx, id, s.now(), mailboxKey)
}

func (s *Service) ReleaseLease(ctx context.Context, id string, returnToPool bool, mailboxKey string) (domain.Lease, error) {
	if err := s.requirePublicAPI(ctx); err != nil {
		return domain.Lease{}, err
	}
	return s.DB.ReleaseLease(ctx, id, s.now(), returnToPool, mailboxKey)
}

func (s *Service) RenewLease(ctx context.Context, id string, minutes int, mailboxKey string) (domain.Lease, error) {
	if err := s.requirePublicAPI(ctx); err != nil {
		return domain.Lease{}, err
	}
	settingsMinutes := domain.DefaultLeaseTTLMinutes
	if minutes <= 0 {
		settings, err := s.DB.GetSettings(ctx)
		if err != nil {
			return domain.Lease{}, err
		}
		settingsMinutes = settings.LeaseTTLMinutes
	}
	return s.DB.RenewLease(ctx, id, s.now(), domain.ResolveLeaseTTL(minutes, settingsMinutes), mailboxKey)
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	unlock := s.lockAccount(id)
	defer unlock()
	return s.DB.DeleteAccount(ctx, id)
}

func (s *Service) UpdateProxy(ctx context.Context, accountID, proxyURL string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		if _, err := protocolProxyOK(proxyURL); err != nil {
			return apperr.New(400, "proxy_invalid", "代理地址无效，只支持 http、https、socks5 和 socks5h", false)
		}
	}
	if err := s.DB.UpdateProxy(ctx, accountID, proxyURL, s.now()); err != nil {
		return err
	}
	_ = s.DB.AddEvent(ctx, domain.Event{At: s.now(), Level: "info", Action: "account_proxy_updated", AccountID: accountID, Message: proxyEvent(proxyURL)})
	return nil
}

func proxyEvent(proxyURL string) string {
	if strings.TrimSpace(proxyURL) == "" {
		return "账号代理已清空"
	}
	return "账号代理已更新"
}

func protocolProxyOK(proxyURL string) (bool, error) {
	return true, validateProxy(proxyURL)
}
