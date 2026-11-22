package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	DomainActive   = "AKTIF"
	DomainExpired  = "EXPIRED"
	DomainDisabled = "DISABLED"
	DomainUnknown  = "TIDAK_DIKETAHUI"

	SizeFromSSH     = "ssh"
	SizeUnknown     = "tidak_diketahui"
	SizeFromManual  = "manual"
	UnknownQuantity = -1
)

type Website struct {
	ID              int64
	AccountID       string
	Domain          string
	UnitPath        string
	DocRoot         string
	VhostType       string
	ParentDomain    string
	IncludedIn      string
	SiteType        string
	TypeConfidence  float64
	SizeBytes       int64
	FileCount       int64
	SizeSource      string
	DomainStatus    string
	DomainExpiresAt string
	IsEnabled       bool
	APIOrderID      int64
	APIType         string
	PHPVersion      string
	NSExternal      *bool
	Nameservers     []string
	DNSInToken      bool
	DNSRecordCount  int
	Subdomains      []string
	Parked          []string
	Signals         []string
	Notes           []string
	MailOrderID     string
	DiscoveredAt    string
}

func (w Website) IsUnit() bool {
	return w.IncludedIn == "" && w.VhostType != "parked" && w.UnitPath != ""
}

func (w Website) DummyOnly() bool {
	return w.DomainStatus == DomainExpired || w.DomainStatus == DomainDisabled
}

type DatabaseRow struct {
	WebsiteDomain string
	AccountID     string
	Name          string
	User          string
	Host          string
	Port          int
	Source        string
	SizeBytes     int64
	InAPI         bool
	APIDomain     string
}

type MailboxRow struct {
	Domain       string
	Address      string
	APIID        string
	OrderID      string
	Status       string
	IsCatchall   bool
	QuotaKB      int64
	UsedKB       int64
	MessageCount int64
}

type CronRow struct {
	UID      string
	Schedule string
	Command  string
}

type InventorySnapshot struct {
	AccountID string
	Alias     string
	Websites  []Website
	Databases []DatabaseRow
	Mailboxes []MailboxRow
	Cron      []CronRow
	Warnings  []string
	SSHUsed   bool
	JSONPath  string
}

type InventoryRun struct {
	AccountID    string
	Alias        string
	FinishedAt   string
	WebsiteCount int
	TotalSize    int64
	SSHUsed      bool
	JSONPath     string
	Warnings     []string
}

func toJSON(v any) any {
	switch x := v.(type) {
	case []string:
		if len(x) == 0 {
			return nil
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func fromJSON(s sql.NullString, out any) {
	if s.Valid && s.String != "" {
		json.Unmarshal([]byte(s.String), out)
	}
}

func boolPtrToNull(b *bool) any {
	if b == nil {
		return nil
	}
	if *b {
		return 1
	}
	return 0
}

func (s *Store) SaveInventory(ctx context.Context, snap *InventorySnapshot) error {
	if snap.AccountID == "" {
		return errors.New("snapshot inventaris tanpa id akun")
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		ts := s.timestamp()
		ids := map[string]int64{}
		var total int64
		for _, w := range snap.Websites {
			var id int64
			err := tx.QueryRowContext(ctx, `
INSERT INTO websites (account_id, domain, root_path, doc_root, vhost_type, parent_domain, included_in, site_type, type_confidence,
    size_bytes, file_count, size_source, domain_status, domain_expires_at, is_enabled, api_order_id, api_website_type, php_version,
    ns_external, nameservers, dns_in_token, dns_record_count, subdomains_json, parked_json, signals_json, notes_json, mail_order_id,
    discovered_at, removed_at)
VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, 0), NULLIF(?, ''), NULLIF(?, ''),
    ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULL)
ON CONFLICT (account_id, domain) DO UPDATE SET
    root_path = excluded.root_path, doc_root = excluded.doc_root, vhost_type = excluded.vhost_type, parent_domain = excluded.parent_domain,
    included_in = excluded.included_in, site_type = excluded.site_type, type_confidence = excluded.type_confidence,
    size_bytes = excluded.size_bytes, file_count = excluded.file_count, size_source = excluded.size_source,
    domain_status = excluded.domain_status, domain_expires_at = excluded.domain_expires_at, is_enabled = excluded.is_enabled,
    api_order_id = excluded.api_order_id, api_website_type = excluded.api_website_type, php_version = excluded.php_version,
    ns_external = excluded.ns_external, nameservers = excluded.nameservers, dns_in_token = excluded.dns_in_token,
    dns_record_count = excluded.dns_record_count, subdomains_json = excluded.subdomains_json, parked_json = excluded.parked_json,
    signals_json = excluded.signals_json, notes_json = excluded.notes_json, mail_order_id = excluded.mail_order_id,
    discovered_at = excluded.discovered_at, removed_at = NULL
RETURNING id`,
				snap.AccountID, w.Domain, w.UnitPath, w.DocRoot, w.VhostType, w.ParentDomain, w.IncludedIn, w.SiteType, w.TypeConfidence,
				w.SizeBytes, w.FileCount, w.SizeSource, w.DomainStatus, w.DomainExpiresAt, w.IsEnabled, w.APIOrderID, w.APIType, w.PHPVersion,
				boolPtrToNull(w.NSExternal), toJSON(w.Nameservers), w.DNSInToken, w.DNSRecordCount, toJSON(w.Subdomains), toJSON(w.Parked),
				toJSON(w.Signals), toJSON(w.Notes), w.MailOrderID, ts).Scan(&id)
			if err != nil {
				return fmt.Errorf("gagal menyimpan website %s: %w", w.Domain, err)
			}
			ids[w.Domain] = id
			if w.IsUnit() && w.SizeBytes > 0 {
				total += w.SizeBytes
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, domain FROM websites WHERE account_id = ? AND removed_at IS NULL`, snap.AccountID)
		if err != nil {
			return err
		}
		var stale []int64
		for rows.Next() {
			var id int64
			var domain string
			if err := rows.Scan(&id, &domain); err != nil {
				rows.Close()
				return err
			}
			if _, ok := ids[domain]; !ok {
				stale = append(stale, id)
			}
		}
		rows.Close()
		for _, id := range stale {
			if _, err := tx.ExecContext(ctx, `UPDATE websites SET removed_at = ? WHERE id = ?`, ts, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM databases WHERE account_id = ?`, snap.AccountID); err != nil {
			return err
		}
		for _, d := range snap.Databases {
			var websiteID any
			if id, ok := ids[d.WebsiteDomain]; ok {
				websiteID = id
			}
			_, err := tx.ExecContext(ctx, `
INSERT INTO databases (website_id, account_id, name, db_user, host, port, size_bytes, source, api_domain, in_api)
VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0), ?, NULLIF(?, ''), NULLIF(?, ''), ?)
ON CONFLICT (account_id, name) DO UPDATE SET website_id = COALESCE(excluded.website_id, databases.website_id),
    db_user = COALESCE(excluded.db_user, databases.db_user), source = COALESCE(excluded.source, databases.source),
    in_api = MAX(excluded.in_api, databases.in_api), size_bytes = MAX(excluded.size_bytes, databases.size_bytes)`,
				websiteID, snap.AccountID, d.Name, d.User, d.Host, d.Port, d.SizeBytes, d.Source, d.APIDomain, d.InAPI)
			if err != nil {
				return fmt.Errorf("gagal menyimpan database %s: %w", d.Name, err)
			}
		}
		domains := map[string]bool{}
		for _, w := range snap.Websites {
			domains[w.Domain] = true
		}
		for d := range domains {
			if _, err := tx.ExecContext(ctx, `DELETE FROM mailboxes WHERE domain = ?`, d); err != nil {
				return err
			}
		}
		for _, m := range snap.Mailboxes {
			_, err := tx.ExecContext(ctx, `
INSERT INTO mailboxes (domain, address, api_id, quota_kb, used_kb, message_count, discovered_at, mail_order_id, status, is_catchall)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)
ON CONFLICT (address) DO UPDATE SET domain = excluded.domain, api_id = excluded.api_id, quota_kb = excluded.quota_kb, used_kb = excluded.used_kb,
    message_count = excluded.message_count, discovered_at = excluded.discovered_at, mail_order_id = excluded.mail_order_id,
    status = excluded.status, is_catchall = excluded.is_catchall`,
				m.Domain, m.Address, m.APIID, m.QuotaKB, m.UsedKB, m.MessageCount, ts, m.OrderID, m.Status, m.IsCatchall)
			if err != nil {
				return fmt.Errorf("gagal menyimpan mailbox %s: %w", m.Address, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cron_jobs WHERE account_id = ?`, snap.AccountID); err != nil {
			return err
		}
		for _, c := range snap.Cron {
			if _, err := tx.ExecContext(ctx, `INSERT INTO cron_jobs (account_id, uid, schedule, command, discovered_at) VALUES (?, ?, ?, ?, ?)`, snap.AccountID, c.UID, c.Schedule, c.Command, ts); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO inventory_runs (account_id, alias, finished_at, website_count, total_size_bytes, ssh_used, json_path, warnings_json)
VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)
ON CONFLICT (account_id, alias) DO UPDATE SET finished_at = excluded.finished_at, website_count = excluded.website_count,
    total_size_bytes = excluded.total_size_bytes, ssh_used = excluded.ssh_used, json_path = excluded.json_path, warnings_json = excluded.warnings_json`,
			snap.AccountID, snap.Alias, ts, len(snap.Websites), total, snap.SSHUsed, snap.JSONPath, toJSON(snap.Warnings))
		return err
	})
}

const websiteColumns = `id, account_id, domain, COALESCE(root_path, ''), COALESCE(doc_root, ''), COALESCE(vhost_type, ''), COALESCE(parent_domain, ''),
    COALESCE(included_in, ''), COALESCE(site_type, ''), COALESCE(type_confidence, 0), COALESCE(size_bytes, -1), COALESCE(file_count, -1),
    COALESCE(size_source, ''), COALESCE(domain_status, ''), COALESCE(domain_expires_at, ''), COALESCE(is_enabled, 0), COALESCE(api_order_id, 0),
    COALESCE(api_website_type, ''), COALESCE(php_version, ''), ns_external, nameservers, COALESCE(dns_in_token, 0), COALESCE(dns_record_count, 0),
    subdomains_json, parked_json, signals_json, notes_json, COALESCE(mail_order_id, ''), discovered_at`

func scanWebsite(sc interface{ Scan(...any) error }) (Website, error) {
	var w Website
	var ns sql.NullInt64
	var nameservers, subs, parked, signals, notes sql.NullString
	err := sc.Scan(&w.ID, &w.AccountID, &w.Domain, &w.UnitPath, &w.DocRoot, &w.VhostType, &w.ParentDomain, &w.IncludedIn, &w.SiteType,
		&w.TypeConfidence, &w.SizeBytes, &w.FileCount, &w.SizeSource, &w.DomainStatus, &w.DomainExpiresAt, &w.IsEnabled, &w.APIOrderID,
		&w.APIType, &w.PHPVersion, &ns, &nameservers, &w.DNSInToken, &w.DNSRecordCount, &subs, &parked, &signals, &notes, &w.MailOrderID, &w.DiscoveredAt)
	if err != nil {
		return w, err
	}
	if ns.Valid {
		v := ns.Int64 == 1
		w.NSExternal = &v
	}
	fromJSON(nameservers, &w.Nameservers)
	fromJSON(subs, &w.Subdomains)
	fromJSON(parked, &w.Parked)
	fromJSON(signals, &w.Signals)
	fromJSON(notes, &w.Notes)
	return w, nil
}

func (s *Store) ListWebsites(ctx context.Context, accountID string) ([]Website, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+websiteColumns+` FROM websites WHERE account_id = ? AND removed_at IS NULL ORDER BY domain`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Website
	for rows.Next() {
		w, err := scanWebsite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) ListDatabasesByAccount(ctx context.Context, accountID string) ([]DatabaseRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(w.domain, ''), d.account_id, d.name, COALESCE(d.db_user, ''), COALESCE(d.host, ''), COALESCE(d.port, 0), COALESCE(d.source, ''),
       COALESCE(d.size_bytes, 0), d.in_api, COALESCE(d.api_domain, '')
FROM databases d LEFT JOIN websites w ON w.id = d.website_id
WHERE d.account_id = ? ORDER BY d.name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatabaseRow
	for rows.Next() {
		var d DatabaseRow
		if err := rows.Scan(&d.WebsiteDomain, &d.AccountID, &d.Name, &d.User, &d.Host, &d.Port, &d.Source, &d.SizeBytes, &d.InAPI, &d.APIDomain); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ListMailboxes(ctx context.Context, domains []string) ([]MailboxRow, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(domains)), ",")
	args := make([]any, len(domains))
	for i, d := range domains {
		args[i] = d
	}
	rows, err := s.db.QueryContext(ctx, `SELECT domain, address, COALESCE(api_id, ''), COALESCE(mail_order_id, ''), COALESCE(status, ''), is_catchall,
    COALESCE(quota_kb, 0), COALESCE(used_kb, 0), COALESCE(message_count, 0) FROM mailboxes WHERE domain IN (`+ph+`) ORDER BY domain, address`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailboxRow
	for rows.Next() {
		var m MailboxRow
		if err := rows.Scan(&m.Domain, &m.Address, &m.APIID, &m.OrderID, &m.Status, &m.IsCatchall, &m.QuotaKB, &m.UsedKB, &m.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListCron(ctx context.Context, accountID string) ([]CronRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uid, schedule, command FROM cron_jobs WHERE account_id = ? ORDER BY uid`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CronRow
	for rows.Next() {
		var c CronRow
		if err := rows.Scan(&c.UID, &c.Schedule, &c.Command); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetInventoryRun(ctx context.Context, accountID, alias string) (*InventoryRun, error) {
	var r InventoryRun
	var warnings, path sql.NullString
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT account_id, alias, finished_at, website_count, total_size_bytes, ssh_used, json_path, warnings_json FROM inventory_runs WHERE account_id = ? AND alias = ?`, accountID, alias).
		Scan(&r.AccountID, &r.Alias, &r.FinishedAt, &r.WebsiteCount, &total, &r.SSHUsed, &path, &warnings)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.TotalSize = total.Int64
	r.JSONPath = path.String
	fromJSON(warnings, &r.Warnings)
	return &r, nil
}
