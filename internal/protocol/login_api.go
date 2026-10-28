package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// NewProviderClient returns a client whose deadline comes from the caller context.
func NewProviderClient() *ICloudClient {
	return newICloudKeepAliveClient()
}

// UncertainProviderResult is true when the caller cannot know whether Apple applied the change.
func UncertainProviderResult(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		return true
	}
	if isAppleTransientNetworkError(err) {
		return true
	}
	switch CodeOf(err) {
	case "apple_account_create_uncertain", "icloud_create_uncertain", "apple_account_create_empty":
		return true
	default:
		return false
	}
}

// RandomToken returns a URL-safe token.
func RandomToken(bytesLen int) (string, error) {
	return randomToken(bytesLen)
}

// RandomID returns a UUID string.
func RandomID() (string, error) {
	return randomUUID()
}

// LoginStart is the exported result of a protocol login that may still need 2FA.
type LoginStart struct {
	PendingID string
	Needs2FA  bool
	Message   string
	AppleID   string
	Session   ICloudSession
	ExpiresAt time.Time
}

// PendingLogins holds in-memory 2FA challenges. It is not persisted.
type PendingLogins struct {
	store *appleAuthPendingStore
}

func NewPendingLogins() *PendingLogins {
	return &PendingLogins{store: newAppleAuthPendingStore()}
}

func (p *PendingLogins) ready() *appleAuthPendingStore {
	if p == nil || p.store == nil {
		return newAppleAuthPendingStore()
	}
	return p.store
}

func loginStartFrom(result appleAuthStartResult) LoginStart {
	return LoginStart{
		PendingID: result.PendingID,
		Needs2FA:  result.Needs2FA,
		Message:   result.Message,
		AppleID:   result.AppleID,
		Session:   result.Session,
		ExpiresAt: result.ExpiresAt,
	}
}

func (c *AppleAuthClient) StartICloudWebLogin(ctx context.Context, appleID, password, defaultHost, clientID, twoFactorMethod, proxyURL string, pending *PendingLogins) (LoginStart, error) {
	result, err := c.StartLoginWithProxy(ctx, appleID, password, defaultHost, clientID, pending.ready(), twoFactorMethod, proxyURL)
	if err != nil {
		return LoginStart{}, err
	}
	return loginStartFrom(result), nil
}

func (c *AppleAuthClient) FinishICloudWebLogin(ctx context.Context, pendingID, code string, pending *PendingLogins) (ICloudSession, error) {
	item, ok := pending.ready().get(pendingID)
	if !ok {
		return ICloudSession{}, errCode("login_pending_expired", "登录挑战已过期，请重新发起", true)
	}
	return c.Submit2FA(ctx, item, code)
}

func (c *AppleAuthClient) StartAppleAccountLogin(ctx context.Context, appleID, password, twoFactorMethod, proxyURL string, pending *PendingLogins) (LoginStart, error) {
	result, err := c.StartAppleAccountManageLoginWithProxy(ctx, appleID, password, pending.ready(), twoFactorMethod, proxyURL)
	if err != nil {
		return LoginStart{}, err
	}
	return loginStartFrom(result), nil
}

func (c *AppleAuthClient) FinishAppleAccountLogin(ctx context.Context, pendingID, code string, pending *PendingLogins) (ICloudSession, error) {
	item, ok := pending.ready().get(pendingID)
	if !ok {
		return ICloudSession{}, errCode("login_pending_expired", "登录挑战已过期，请重新发起", true)
	}
	return c.SubmitAppleAccountManage2FA(ctx, item, code, json.RawMessage(nil))
}
