ALTER TABLE items ADD COLUMN dest_ip TEXT;
ALTER TABLE items ADD COLUMN source_temp_domain TEXT;
ALTER TABLE items ADD COLUMN search_replace_done INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN ssl_excluded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN final_sync_done INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN dest_db_json TEXT;
ALTER TABLE items ADD COLUMN email_state TEXT;
ALTER TABLE items ADD COLUMN started_at TEXT;
ALTER TABLE items ADD COLUMN finished_at TEXT;

ALTER TABLE worker_lock ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE worker_lock ADD COLUMN started_at TEXT;

ALTER TABLE manual_tasks ADD COLUMN value TEXT;

CREATE INDEX idx_items_batch ON items(batch_id);
CREATE INDEX idx_manual_open ON manual_tasks(item_id, code, status);
CREATE INDEX idx_artifacts_item ON artifacts(item_id, kind);
