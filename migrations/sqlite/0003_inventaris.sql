ALTER TABLE websites ADD COLUMN doc_root TEXT;
ALTER TABLE websites ADD COLUMN parent_domain TEXT;
ALTER TABLE websites ADD COLUMN included_in TEXT;
ALTER TABLE websites ADD COLUMN api_website_type TEXT;
ALTER TABLE websites ADD COLUMN domain_expires_at TEXT;
ALTER TABLE websites ADD COLUMN ns_external INTEGER;
ALTER TABLE websites ADD COLUMN nameservers TEXT;
ALTER TABLE websites ADD COLUMN dns_in_token INTEGER;
ALTER TABLE websites ADD COLUMN dns_record_count INTEGER;
ALTER TABLE websites ADD COLUMN subdomains_json TEXT;
ALTER TABLE websites ADD COLUMN parked_json TEXT;
ALTER TABLE websites ADD COLUMN signals_json TEXT;
ALTER TABLE websites ADD COLUMN notes_json TEXT;
ALTER TABLE websites ADD COLUMN size_source TEXT;
ALTER TABLE websites ADD COLUMN mail_order_id TEXT;
ALTER TABLE websites ADD COLUMN removed_at TEXT;

ALTER TABLE databases ADD COLUMN source TEXT;
ALTER TABLE databases ADD COLUMN api_domain TEXT;
ALTER TABLE databases ADD COLUMN in_api INTEGER NOT NULL DEFAULT 0;

ALTER TABLE mailboxes ADD COLUMN mail_order_id TEXT;
ALTER TABLE mailboxes ADD COLUMN status TEXT;
ALTER TABLE mailboxes ADD COLUMN is_catchall INTEGER NOT NULL DEFAULT 0;

CREATE TABLE cron_jobs (
    account_id TEXT NOT NULL,
    uid TEXT NOT NULL,
    schedule TEXT NOT NULL,
    command TEXT NOT NULL,
    discovered_at TEXT NOT NULL,
    PRIMARY KEY (account_id, uid)
);

CREATE TABLE inventory_runs (
    account_id TEXT NOT NULL,
    alias TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    website_count INTEGER NOT NULL,
    total_size_bytes INTEGER,
    ssh_used INTEGER NOT NULL DEFAULT 0,
    json_path TEXT,
    warnings_json TEXT,
    PRIMARY KEY (account_id, alias)
);

ALTER TABLE batches ADD COLUMN plan_json TEXT;
ALTER TABLE items ADD COLUMN expected_final TEXT;
ALTER TABLE items ADD COLUMN plan_notes TEXT;
