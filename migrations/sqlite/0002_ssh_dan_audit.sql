ALTER TABLE hosting_accounts ADD COLUMN ssh_auth TEXT;
ALTER TABLE hosting_accounts ADD COLUMN ssh_last_error TEXT;
ALTER TABLE hosting_accounts ADD COLUMN ssh_checked_at TEXT;
ALTER TABLE hosting_accounts ADD COLUMN ssh_home TEXT;
ALTER TABLE capabilities ADD COLUMN detail_json TEXT;
