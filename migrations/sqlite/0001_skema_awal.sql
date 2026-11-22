CREATE TABLE credentials (
    alias TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    last_status TEXT NOT NULL DEFAULT 'BELUM_DIUJI',
    last_checked_at TEXT,
    last_error TEXT
);

CREATE TABLE hosting_accounts (
    id TEXT NOT NULL,
    alias TEXT NOT NULL REFERENCES credentials(alias) ON DELETE CASCADE,
    order_id INTEGER,
    plan TEXT,
    order_status TEXT,
    website_count INTEGER NOT NULL DEFAULT 0,
    ssh_host TEXT,
    ssh_port INTEGER,
    ssh_user TEXT,
    ssh_fingerprint TEXT,
    ssh_status TEXT NOT NULL DEFAULT 'BELUM_DIATUR',
    discovered_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (id, alias)
);

CREATE TABLE capabilities (
    account_id TEXT NOT NULL,
    code TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('SUPPORTED', 'MANUAL', 'UNSUPPORTED', 'UNKNOWN')),
    note TEXT,
    tested_at TEXT,
    PRIMARY KEY (account_id, code)
);

CREATE TABLE websites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT NOT NULL,
    domain TEXT NOT NULL,
    root_path TEXT,
    vhost_type TEXT,
    site_type TEXT,
    type_confidence REAL,
    size_bytes INTEGER,
    file_count INTEGER,
    domain_status TEXT,
    is_enabled INTEGER,
    api_order_id INTEGER,
    php_version TEXT,
    discovered_at TEXT NOT NULL,
    UNIQUE (account_id, domain)
);

CREATE TABLE databases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    website_id INTEGER REFERENCES websites(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL,
    name TEXT NOT NULL,
    db_user TEXT,
    host TEXT,
    port INTEGER,
    size_bytes INTEGER,
    UNIQUE (account_id, name)
);

CREATE TABLE mailboxes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain TEXT NOT NULL,
    address TEXT NOT NULL UNIQUE,
    api_id TEXT,
    quota_kb INTEGER,
    used_kb INTEGER,
    message_count INTEGER,
    discovered_at TEXT NOT NULL
);

CREATE TABLE batches (
    id TEXT PRIMARY KEY,
    source_alias TEXT NOT NULL,
    source_account TEXT NOT NULL,
    dest_alias TEXT NOT NULL,
    dest_account TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT
);

CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id TEXT NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
    source_website_id INTEGER REFERENCES websites(id),
    source_domain TEXT NOT NULL,
    dest_website_ref TEXT,
    dummy_domain TEXT,
    dest_root_path TEXT,
    state TEXT NOT NULL,
    side_state TEXT,
    manual_code TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    updated_at TEXT NOT NULL,
    UNIQUE (batch_id, source_domain)
);

CREATE TABLE steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    checkpoint_json TEXT,
    started_at TEXT,
    finished_at TEXT,
    error TEXT,
    UNIQUE (item_id, name)
);

CREATE TABLE artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    local_path TEXT NOT NULL,
    size_bytes INTEGER,
    sha256 TEXT,
    validated INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    UNIQUE (item_id, kind, local_path)
);

CREATE TABLE verifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    check_type TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('PASS', 'WARN', 'FAIL')),
    detail TEXT,
    checked_at TEXT NOT NULL
);

CREATE TABLE manual_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER REFERENCES items(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    instructions TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'MENUNGGU',
    created_at TEXT NOT NULL,
    confirmed_at TEXT
);

CREATE TABLE dns_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain TEXT NOT NULL,
    item_id INTEGER REFERENCES items(id) ON DELETE SET NULL,
    records_json TEXT NOT NULL,
    api_snapshot_id INTEGER,
    reason TEXT,
    taken_at TEXT NOT NULL
);

CREATE TABLE approvals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id TEXT NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    item_list_json TEXT NOT NULL,
    approved_at TEXT NOT NULL
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    at TEXT NOT NULL,
    actor TEXT NOT NULL CHECK (actor IN ('worker', 'operator')),
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    result TEXT NOT NULL,
    detail TEXT
);

CREATE TABLE worker_lock (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    pid INTEGER NOT NULL,
    lease_until TEXT NOT NULL
);
