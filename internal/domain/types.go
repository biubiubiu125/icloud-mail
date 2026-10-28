package domain

import "time"

const (
	StatusAvailable = "available"
	StatusUsed      = "used"
	StatusDisabled  = "disabled"

	LeaseHeld      = "held"
	LeaseCommitted = "committed"
	LeaseReleased  = "released"
	LeaseExpired   = "expired"

	DeletePending   = "pending"
	DeleteFailed    = "failed"
	DeleteUnknown   = "unknown"
	DeleteSucceeded = "succeeded"

	OriginICloudWeb    = "ICLOUD_WEB"
	OriginAppleAccount = "APPLE_ACCOUNT"

	DefaultLeaseTTLMinutes = 30
	MaxLeaseTTLMinutes     = 10080
	DefaultKeyword         = "OpenAI"
	CodeFreshWindow        = 5 * time.Minute
	MaxCodeWait            = 30 * time.Second
)

type Account struct {
	ID                        string     `json:"id"`
	Label                     string     `json:"label"`
	AppleID                   string     `json:"apple_id"`
	ApplePassword             string     `json:"-"`
	ProxyURL                  string     `json:"proxy_url"`
	HasPassword               bool       `json:"has_password"`
	WebLoginSaved             bool       `json:"web_login_saved"`
	AppleAccountLoginSaved    bool       `json:"apple_account_login_saved"`
	IMAPSaved                 bool       `json:"imap_saved"`
	ReconciliationRequired    bool       `json:"reconciliation_required"`
	ReconciliationAt          *time.Time `json:"reconciliation_at,omitempty"`
	ReconciliationError       string     `json:"reconciliation_error,omitempty"`
	ReconciliationOrigin      string     `json:"reconciliation_origin,omitempty"`
	ReconciliationAnonymousID string     `json:"reconciliation_anonymous_id,omitempty"`
	ReconciliationEmail       string     `json:"reconciliation_email,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

type Mailbox struct {
	ID                 string     `json:"id"`
	AccountID          string     `json:"account_id"`
	Email              string     `json:"email"`
	Label              string     `json:"label"`
	Note               string     `json:"note"`
	Status             string     `json:"status"`
	APIToken           string     `json:"api_token,omitempty"`
	APIActive          bool       `json:"api_active"`
	ICloudActive       bool       `json:"icloud_active"`
	RemoteAnonymousID  string     `json:"remote_anonymous_id"`
	RemoteOrigin       string     `json:"remote_origin"`
	RemoteMissingAt    *time.Time `json:"remote_missing_at,omitempty"`
	RemoteDeleteStatus string     `json:"remote_delete_status"`
	RemoteDeleteError  string     `json:"remote_delete_error,omitempty"`
	RemoteDeleteAt     *time.Time `json:"remote_delete_at,omitempty"`
	APIExportedAt      *time.Time `json:"api_exported_at,omitempty"`
	LastSyncAt         *time.Time `json:"last_sync_at,omitempty"`
	LastSyncUID        string     `json:"last_sync_uid,omitempty"`
	ActiveLeaseID      string     `json:"active_lease_id,omitempty"`
	CodeNotBefore      *time.Time `json:"code_not_before,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type Message struct {
	ID          string    `json:"id"`
	MailboxID   string    `json:"mailbox_id"`
	RemoteID    string    `json:"remote_id"`
	CanonicalID string    `json:"canonical_id"`
	UID         string    `json:"uid"`
	Subject     string    `json:"subject"`
	From        string    `json:"from"`
	Body        string    `json:"-"`
	Source      string    `json:"source,omitempty"`
	ReceivedAt  time.Time `json:"received_at"`
}

type Lease struct {
	ID          string     `json:"id"`
	MailboxID   string     `json:"mailbox_id"`
	Project     string     `json:"project"`
	Purpose     string     `json:"purpose"`
	RequestID   string     `json:"request_id"`
	State       string     `json:"state"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CommittedAt *time.Time `json:"committed_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	ExpiredAt   *time.Time `json:"expired_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Settings struct {
	PublicAPIEnabled         bool   `json:"public_api_enabled"`
	APIKey                   string `json:"api_key,omitempty"`
	PublicBaseURL            string `json:"public_base_url"`
	DefaultKeyword           string `json:"default_keyword"`
	LeaseTTLMinutes          int    `json:"lease_ttl_minutes"`
	MessageRetentionDays     int    `json:"message_retention_days"`
	SchedulerEnabled         bool   `json:"scheduler_enabled"`
	SchedulerIntervalSeconds int    `json:"scheduler_interval_seconds"`
	SchedulerBatch           int    `json:"scheduler_batch"`
	WatcherEnabled           bool   `json:"watcher_enabled"`
	WatcherIntervalSeconds   int    `json:"watcher_interval_seconds"`
	WebCodeSyncEnabled       bool   `json:"web_code_sync_enabled"`
	AppleAccountAPIKey       string `json:"apple_account_api_key,omitempty"`
}

type Admin struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type AdminSession struct {
	TokenHash string
	AdminID   string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Event struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Level     string    `json:"level"`
	Action    string    `json:"action"`
	AccountID string    `json:"account_id,omitempty"`
	MailboxID string    `json:"mailbox_id,omitempty"`
	Message   string    `json:"message"`
}

type ClaimInput struct {
	Project   string
	Purpose   string
	RequestID string
	AccountID string
	TTL       time.Duration
	Now       time.Time
	Note      string
}

type ClaimResult struct {
	Mailbox    Mailbox `json:"mailbox"`
	Lease      *Lease  `json:"lease,omitempty"`
	Created    bool    `json:"created"`
	Idempotent bool    `json:"idempotent"`
}

type CodeResult struct {
	Email      string    `json:"email"`
	Code       string    `json:"code"`
	Subject    string    `json:"subject,omitempty"`
	From       string    `json:"from,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
	StaleCache bool      `json:"stale_cache"`
}

type ExportItem struct {
	Email    string `json:"email"`
	APIURL   string `json:"api_url"`
	APIToken string `json:"api_token"`
}
