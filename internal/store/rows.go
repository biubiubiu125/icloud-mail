package store

import (
	"github.com/jackc/pgx/v5"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

const accountSelect = `
	SELECT id, label, apple_id, apple_password, proxy_url, session_json,
	       reconciliation_required, reconciliation_at, reconciliation_error, reconciliation_origin,
	       reconciliation_anonymous_id, reconciliation_email,
	       created_at, updated_at
	FROM accounts
`

const mailboxSelect = `
	SELECT id, account_id, email, label, note, status, api_token, api_active, icloud_active,
	       remote_anonymous_id, remote_origin, remote_missing_at, remote_delete_status,
	       remote_delete_error, remote_delete_at, api_exported_at, last_sync_at, last_sync_uid,
	       active_lease_id, code_not_before, created_at, updated_at
	FROM mailboxes
`

func collectAccounts(rows pgx.Rows) ([]domain.Account, error) {
	var out []domain.Account
	for rows.Next() {
		var item domain.Account
		var raw []byte
		if err := rows.Scan(
			&item.ID, &item.Label, &item.AppleID, &item.ApplePassword, &item.ProxyURL, &raw,
			&item.ReconciliationRequired, &item.ReconciliationAt, &item.ReconciliationError, &item.ReconciliationOrigin,
			&item.ReconciliationAnonymousID, &item.ReconciliationEmail,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.HasPassword = item.ApplePassword != ""
		item.WebLoginSaved, item.AppleAccountLoginSaved, item.IMAPSaved = sessionFlags(raw)
		out = append(out, item)
	}
	if out == nil {
		out = []domain.Account{}
	}
	return out, rows.Err()
}

func sessionFlags(raw []byte) (web, apple, imap bool) {
	var session protocol.ICloudSession
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return false, false, false
	}
	if err := jsonUnmarshal(raw, &session); err != nil {
		return false, false, false
	}
	for _, state := range session.LoginStates {
		switch state.Kind {
		case protocol.LoginStateICloudWeb:
			if len(state.Cookies) > 0 || len(session.Cookies) > 0 {
				web = true
			}
		case protocol.LoginStateAppleAccount:
			if state.APIKey != "" || len(state.Cookies) > 0 {
				apple = true
			}
		case protocol.LoginStateICloudIMAP:
			if state.IMAPAppPassword != "" {
				imap = true
			}
		}
	}
	if !web && len(session.LoginStates) == 0 && len(session.Cookies) > 0 {
		web = true
	}
	return web, apple, imap
}

func collectMailboxes(rows pgx.Rows) ([]domain.Mailbox, error) {
	var out []domain.Mailbox
	for rows.Next() {
		var item domain.Mailbox
		if err := rows.Scan(
			&item.ID, &item.AccountID, &item.Email, &item.Label, &item.Note, &item.Status,
			&item.APIToken, &item.APIActive, &item.ICloudActive, &item.RemoteAnonymousID,
			&item.RemoteOrigin, &item.RemoteMissingAt, &item.RemoteDeleteStatus, &item.RemoteDeleteError,
			&item.RemoteDeleteAt, &item.APIExportedAt, &item.LastSyncAt, &item.LastSyncUID,
			&item.ActiveLeaseID, &item.CodeNotBefore, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if out == nil {
		out = []domain.Mailbox{}
	}
	return out, rows.Err()
}
