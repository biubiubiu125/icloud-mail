package httpapi

import (
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

type accountListItem struct {
	ID                        string     `json:"id"`
	Label                     string     `json:"label"`
	AppleID                   string     `json:"apple_id"`
	HasPassword               bool       `json:"has_password"`
	HasProxy                  bool       `json:"has_proxy"`
	WebLoginSaved             bool       `json:"web_login_saved"`
	AppleAccountLoginSaved    bool       `json:"apple_account_login_saved"`
	IMAPSaved                 bool       `json:"imap_saved"`
	ReconciliationRequired    bool       `json:"reconciliation_required"`
	ReconciliationAt          *time.Time `json:"reconciliation_at,omitempty"`
	ReconciliationError       string     `json:"reconciliation_error,omitempty"`
	ReconciliationOrigin      string     `json:"reconciliation_origin,omitempty"`
	ReconciliationAnonymousID string     `json:"reconciliation_anonymous_id,omitempty"`
	ReconciliationEmail       string     `json:"reconciliation_email,omitempty"`
}

type accountDetail struct {
	ID                     string `json:"id"`
	Label                  string `json:"label"`
	AppleID                string `json:"apple_id"`
	Password               string `json:"password"`
	ProxyScheme            string `json:"proxy_scheme"`
	ProxyHost              string `json:"proxy_host"`
	ProxyPort              string `json:"proxy_port"`
	ProxyUsername          string `json:"proxy_username"`
	ProxyPassword          string `json:"proxy_password"`
	HasPassword            bool   `json:"has_password"`
	HasProxy               bool   `json:"has_proxy"`
	WebLoginSaved          bool   `json:"web_login_saved"`
	AppleAccountLoginSaved bool   `json:"apple_account_login_saved"`
	IMAPSaved              bool   `json:"imap_saved"`
	IMAPEmail              string `json:"imap_email,omitempty"`
	ReconciliationRequired bool   `json:"reconciliation_required"`
}

type accountPatch struct {
	Label          string
	AppleID        string
	Password       string
	UpdatePassword bool
	UpdateProxy    bool
	ProxyScheme    string
	ProxyHost      string
	ProxyPort      string
	ProxyUsername  string
	ProxyPassword  string
}

type accountPatchResult struct {
	Label         string
	AppleID       string
	Password      string
	WritePassword bool
	ProxyURL      string
	WriteProxy    bool
}

func accountListItemFrom(item domain.Account) accountListItem {
	return accountListItem{
		ID: item.ID, Label: item.Label, AppleID: item.AppleID,
		HasPassword: item.HasPassword, HasProxy: strings.TrimSpace(item.ProxyURL) != "",
		WebLoginSaved: item.WebLoginSaved, AppleAccountLoginSaved: item.AppleAccountLoginSaved,
		IMAPSaved: item.IMAPSaved, ReconciliationRequired: item.ReconciliationRequired,
		ReconciliationAt: item.ReconciliationAt, ReconciliationError: item.ReconciliationError,
		ReconciliationOrigin: item.ReconciliationOrigin, ReconciliationAnonymousID: item.ReconciliationAnonymousID,
		ReconciliationEmail: item.ReconciliationEmail,
	}
}

func accountDetailFrom(item domain.Account) (accountDetail, error) {
	parts, err := protocol.SplitProxyURL(item.ProxyURL)
	if err != nil {
		return accountDetail{}, err
	}
	return accountDetail{
		ID: item.ID, Label: item.Label, AppleID: item.AppleID, Password: item.ApplePassword,
		ProxyScheme: parts.Scheme, ProxyHost: parts.Host, ProxyPort: parts.Port,
		ProxyUsername: parts.Username, ProxyPassword: parts.Password,
		HasPassword:   item.HasPassword || strings.TrimSpace(item.ApplePassword) != "",
		HasProxy:      strings.TrimSpace(item.ProxyURL) != "",
		WebLoginSaved: item.WebLoginSaved, AppleAccountLoginSaved: item.AppleAccountLoginSaved,
		IMAPSaved: item.IMAPSaved, IMAPEmail: item.IMAPEmail, ReconciliationRequired: item.ReconciliationRequired,
	}, nil
}

func accountPatchPlan(payload accountPatch) (accountPatchResult, error) {
	appleID := strings.TrimSpace(payload.AppleID)
	if appleID == "" {
		return accountPatchResult{}, apperr.New(400, "apple_id_missing", "请填写 Apple ID", false)
	}
	result := accountPatchResult{Label: strings.TrimSpace(payload.Label), AppleID: appleID}
	if payload.UpdatePassword || strings.TrimSpace(payload.Password) != "" {
		result.WritePassword = true
		if strings.TrimSpace(payload.Password) != "" {
			result.Password = payload.Password
		}
	}
	if !payload.UpdateProxy {
		return result, nil
	}
	joined, err := protocol.JoinProxyURL(protocol.ProxyForm{
		Scheme: payload.ProxyScheme, Host: payload.ProxyHost, Port: payload.ProxyPort,
		Username: payload.ProxyUsername, Password: payload.ProxyPassword,
	})
	if err != nil {
		return accountPatchResult{}, apperr.New(400, "proxy_invalid", "代理地址无效，只支持 http、https、socks5 和 socks5h", false)
	}
	result.WriteProxy = true
	result.ProxyURL = joined
	return result, nil
}
