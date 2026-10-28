CREATE TABLE IF NOT EXISTS schema_migrations (
    version integer PRIMARY KEY,
    applied_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS admins (
    id text PRIMARY KEY,
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    token_hash text PRIMARY KEY,
    admin_id text NOT NULL REFERENCES admins (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    id integer PRIMARY KEY CHECK (id = 1),
    public_api_enabled boolean NOT NULL DEFAULT true,
    api_key text NOT NULL DEFAULT '',
    public_base_url text NOT NULL DEFAULT '',
    default_keyword text NOT NULL DEFAULT 'OpenAI',
    lease_ttl_minutes integer NOT NULL DEFAULT 30,
    message_retention_days integer NOT NULL DEFAULT 90,
    scheduler_enabled boolean NOT NULL DEFAULT false,
    scheduler_interval_seconds integer NOT NULL DEFAULT 300,
    scheduler_batch integer NOT NULL DEFAULT 1,
    watcher_enabled boolean NOT NULL DEFAULT true,
    watcher_interval_seconds integer NOT NULL DEFAULT 60,
    web_code_sync_enabled boolean NOT NULL DEFAULT false,
    apple_account_api_key text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL
);

INSERT INTO settings (id, updated_at)
VALUES (1, now())
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS accounts (
    id text PRIMARY KEY,
    label text NOT NULL DEFAULT '',
    apple_id text NOT NULL DEFAULT '',
    apple_password text NOT NULL DEFAULT '',
    proxy_url text NOT NULL DEFAULT '',
    session_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    reconciliation_required boolean NOT NULL DEFAULT false,
    reconciliation_at timestamptz,
    reconciliation_error text NOT NULL DEFAULT '',
    reconciliation_origin text NOT NULL DEFAULT '',
    reconciliation_anonymous_id text NOT NULL DEFAULT '',
    reconciliation_email text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS reconciliation_anonymous_id text NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS reconciliation_email text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS mailboxes (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts (id),
    email text NOT NULL,
    label text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'available',
    api_token text NOT NULL,
    api_active boolean NOT NULL DEFAULT true,
    icloud_active boolean NOT NULL DEFAULT true,
    remote_anonymous_id text NOT NULL DEFAULT '',
    remote_origin text NOT NULL DEFAULT 'ICLOUD_WEB',
    remote_missing_at timestamptz,
    remote_delete_status text NOT NULL DEFAULT '',
    remote_delete_error text NOT NULL DEFAULT '',
    remote_delete_at timestamptz,
    api_exported_at timestamptz,
    last_sync_at timestamptz,
    last_sync_uid text NOT NULL DEFAULT '',
    active_lease_id text NOT NULL DEFAULT '',
    code_not_before timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (email)
);

ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS code_not_before timestamptz;

CREATE INDEX IF NOT EXISTS mailboxes_claim_idx
    ON mailboxes (created_at)
    WHERE status = 'available'
      AND api_active
      AND icloud_active
      AND active_lease_id = ''
      AND remote_delete_status = '';

CREATE TABLE IF NOT EXISTS mailbox_leases (
    id text PRIMARY KEY,
    mailbox_id text NOT NULL REFERENCES mailboxes (id) ON DELETE CASCADE,
    project text NOT NULL DEFAULT '',
    purpose text NOT NULL DEFAULT '',
    request_id text NOT NULL DEFAULT '',
    state text NOT NULL,
    expires_at timestamptz NOT NULL,
    committed_at timestamptz,
    released_at timestamptz,
    expired_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS mailbox_leases_request_idx
    ON mailbox_leases (project, request_id)
    WHERE request_id <> '';

CREATE TABLE IF NOT EXISTS messages (
    id text PRIMARY KEY,
    mailbox_id text NOT NULL REFERENCES mailboxes (id) ON DELETE CASCADE,
    remote_id text NOT NULL DEFAULT '',
    canonical_id text NOT NULL,
    uid text NOT NULL DEFAULT '',
    subject text NOT NULL DEFAULT '',
    from_addr text NOT NULL DEFAULT '',
    body text NOT NULL DEFAULT '',
    source text NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (mailbox_id, canonical_id)
);

ALTER TABLE messages ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS messages_lookup_idx
    ON messages (mailbox_id, received_at DESC);

CREATE TABLE IF NOT EXISTS events (
    id bigserial PRIMARY KEY,
    at timestamptz NOT NULL,
    level text NOT NULL,
    action text NOT NULL,
    account_id text NOT NULL DEFAULT '',
    mailbox_id text NOT NULL DEFAULT '',
    message text NOT NULL
);

CREATE INDEX IF NOT EXISTS events_at_idx ON events (at DESC);
