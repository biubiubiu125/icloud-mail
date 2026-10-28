package protocol

import "time"

const (
	StatusActive    = "active"
	StatusAvailable = "available"
	StatusUsed      = "used"
	StatusFailed    = "failed"
	StatusDisabled  = "disabled"

	LoginStateICloudWeb    = "icloud_web"
	LoginStateAppleAccount = "apple_account"
	LoginStateICloudIMAP   = "icloud_imap"

	OriginICloudWeb    = "ICLOUD_WEB"
	OriginAppleAccount = "APPLE_ACCOUNT"

	mailboxRemoteOriginICloudWeb    = OriginICloudWeb
	mailboxRemoteOriginAppleAccount = OriginAppleAccount
)

type mailboxCreateChannel string

const (
	mailboxCreateChannelAuto         mailboxCreateChannel = ""
	mailboxCreateChannelAppleAccount mailboxCreateChannel = "apple_account"
	mailboxCreateChannelICloudWeb    mailboxCreateChannel = "icloud_web"
)

// Mailbox is the slim shape the ported Apple and IMAP clients read.
// The application store keeps the full mailbox record elsewhere.
type Mailbox struct {
	ID          string
	Email       string
	LastSyncAt  time.Time
	LastSyncUID string
}

type SessionCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	HTTPOnly bool    `json:"http_only,omitempty"`
	SameSite string  `json:"same_site,omitempty"`
}

type LoginState struct {
	Kind               string          `json:"kind"`
	Host               string          `json:"host,omitempty"`
	Origin             string          `json:"origin,omitempty"`
	ProxyURL           string          `json:"proxy_url,omitempty"`
	SavedAt            time.Time       `json:"saved_at,omitempty"`
	Cookies            []SessionCookie `json:"cookies,omitempty"`
	Scnt               string          `json:"scnt,omitempty"`
	SessionID          string          `json:"session_id,omitempty"`
	APIKey             string          `json:"api_key,omitempty"`
	DataAccessToken    string          `json:"data_access_token,omitempty"`
	UserAgent          string          `json:"user_agent,omitempty"`
	Note               string          `json:"note,omitempty"`
	IMAPEmail          string          `json:"imap_email,omitempty"`
	IMAPUsername       string          `json:"imap_username,omitempty"`
	IMAPHost           string          `json:"imap_host,omitempty"`
	IMAPPort           int             `json:"imap_port,omitempty"`
	IMAPAppPassword    string          `json:"imap_app_password,omitempty"`
	IMAPLastSyncAt     time.Time       `json:"imap_last_sync_at,omitempty"`
	IMAPLastSyncUID    string          `json:"imap_last_sync_uid,omitempty"`
	ManageExpiresAt    time.Time       `json:"manage_expires_at,omitempty"`
	LastCheckedAt      time.Time       `json:"last_checked_at,omitempty"`
	LastCheckOK        bool            `json:"last_check_ok,omitempty"`
	LastStatusMessage  string          `json:"last_status_message,omitempty"`
	KeepAliveFailCount int             `json:"keep_alive_fail_count,omitempty"`
	KeepAliveStopped   bool            `json:"keep_alive_stopped,omitempty"`
}

// ICloudSession is one Apple account's saved login material.
// ProxyURL on this struct is a transport copy. The account row is the authority.
type ICloudSession struct {
	OwnerID                   string          `json:"owner_id,omitempty"`
	AccountID                 string          `json:"account_id,omitempty"`
	ProxyURL                  string          `json:"proxy_url,omitempty"`
	SavedAt                   time.Time       `json:"saved_at"`
	AppleID                   string          `json:"apple_id,omitempty"`
	DSID                      string          `json:"dsid"`
	ClientID                  string          `json:"client_id"`
	ClientBuildNumber         string          `json:"client_build_number"`
	MasteringNumber           string          `json:"client_mastering_number"`
	PremiumMailBaseURL        string          `json:"premium_mail_base_url"`
	MailGatewayBaseURL        string          `json:"mail_gateway_base_url,omitempty"`
	MailBaseURL               string          `json:"mail_base_url,omitempty"`
	Host                      string          `json:"host"`
	IsICloudPlus              bool            `json:"is_icloud_plus"`
	CanCreateHME              bool            `json:"can_create_hme"`
	CapabilitiesAuthoritative bool            `json:"capabilities_authoritative,omitempty"`
	Cookies                   []SessionCookie `json:"cookies"`
	LoginStates               []LoginState    `json:"login_states,omitempty"`
	Note                      string          `json:"note,omitempty"`
	LastCheckedAt             time.Time       `json:"last_checked_at,omitempty"`
	LastCheckOK               bool            `json:"last_check_ok,omitempty"`
	LastStatusMessage         string          `json:"last_status_message,omitempty"`
}

func hasAnyNonAppleAccountLoginState(states []LoginState) bool {
	for _, state := range states {
		if state.Kind != LoginStateAppleAccount {
			return true
		}
	}
	return false
}
