package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

type Provider interface {
	Delete(ctx context.Context, session protocol.ICloudSession, origin, anonymousID, apiKey string) (protocol.ICloudSession, error)
	Create(ctx context.Context, session protocol.ICloudSession, channel, label, note, apiKey string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error)
	List(ctx context.Context, session protocol.ICloudSession, origin, apiKey string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error)
	SyncIMAP(ctx context.Context, state protocol.LoginState, mailboxes []protocol.Mailbox, after time.Time, keyword string, maxMessages int) (map[string][]protocol.ICloudSyncedMessage, string, error)
	SyncWeb(ctx context.Context, session protocol.ICloudSession, mailbox protocol.Mailbox, after time.Time, keyword string, maxThreads int) ([]protocol.ICloudSyncedMessage, error)
	CheckIMAP(ctx context.Context, email, appPassword, proxyURL string) error
}

type appleProvider struct {
	client *protocol.ICloudClient
}

func NewProvider() Provider {
	return appleProvider{client: protocol.NewProviderClient()}
}

func (p appleProvider) Delete(ctx context.Context, session protocol.ICloudSession, origin, anonymousID, apiKey string) (protocol.ICloudSession, error) {
	switch origin {
	case domain.OriginAppleAccount:
		return p.client.DeletePrivacyMailboxWithAppleAccount(ctx, session, apiKey, anonymousID)
	case domain.OriginICloudWeb:
		err := p.client.DeletePrivacyMailbox(ctx, session, anonymousID)
		return session, err
	default:
		return session, protocol.NewError("remote_origin_unknown", "未知远端来源，已拒绝访问 Apple", false)
	}
}

func (p appleProvider) Create(ctx context.Context, session protocol.ICloudSession, channel, label, note, apiKey string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	switch strings.TrimSpace(channel) {
	case string(domain.OriginAppleAccount), "apple_account":
		return p.client.CreatePrivacyMailboxWithAppleAccount(ctx, session, apiKey, label, note)
	default:
		remote, err := p.client.CreatePrivacyMailbox(ctx, session, label, note)
		return remote, session, err
	}
}

func (p appleProvider) List(ctx context.Context, session protocol.ICloudSession, origin, apiKey string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	switch origin {
	case domain.OriginAppleAccount, domain.OriginICloudWeb:
		return p.client.ListPrivacyMailboxesForOriginWithSessionAndAPIKey(ctx, session, origin, apiKey)
	default:
		return nil, session, protocol.NewError("remote_origin_unknown", "未知远端来源，已拒绝访问 Apple", false)
	}
}

func (p appleProvider) SyncIMAP(ctx context.Context, state protocol.LoginState, mailboxes []protocol.Mailbox, after time.Time, keyword string, maxMessages int) (map[string][]protocol.ICloudSyncedMessage, string, error) {
	result, err := protocol.SyncICloudIMAPMessagesWithCursor(ctx, state, mailboxes, after, keyword, maxMessages)
	if err != nil {
		return nil, "", err
	}
	return result.MessagesByMailbox, result.LastUID, nil
}

func (p appleProvider) SyncWeb(ctx context.Context, session protocol.ICloudSession, mailbox protocol.Mailbox, after time.Time, keyword string, maxThreads int) ([]protocol.ICloudSyncedMessage, error) {
	return p.client.SyncMailboxMessages(ctx, session, mailbox, after, keyword, maxThreads)
}

func (p appleProvider) CheckIMAP(ctx context.Context, email, appPassword, proxyURL string) error {
	return protocol.CheckICloudIMAPLoginWithProxy(ctx, email, appPassword, proxyURL)
}
