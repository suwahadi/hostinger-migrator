package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	CredentialUntested = "BELUM_DIUJI"
	CredentialActive   = "AKTIF"
	CredentialRejected = "DITOLAK"
	CredentialError    = "ERROR"
)

type Credential struct {
	Alias         string
	CreatedAt     string
	LastStatus    string
	LastCheckedAt string
	LastError     string
}

type HostingAccount struct {
	ID             string
	Alias          string
	OrderID        int64
	Plan           string
	OrderStatus    string
	WebsiteCount   int
	SSHHost        string
	SSHPort        int
	SSHUser        string
	SSHFingerprint string
	SSHStatus      string
	DiscoveredAt   string
	UpdatedAt      string
}

type AuditEntry struct {
	ID     int64
	At     string
	Actor  string
	Action string
	Target string
	Result string
	Detail string
}

func (s *Store) CredentialExists(ctx context.Context, alias string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM credentials WHERE alias = ?`, alias).Scan(&n)
	return n > 0, err
}

func (s *Store) InsertCredential(ctx context.Context, tx *sql.Tx, alias string) error {
	ts := s.timestamp()
	_, err := tx.ExecContext(ctx, `INSERT INTO credentials (alias, created_at, last_status, last_checked_at) VALUES (?, ?, ?, ?)`, alias, ts, CredentialActive, ts)
	if err != nil {
		return fmt.Errorf("gagal menyimpan alias %s: %w", alias, err)
	}
	return nil
}

func (s *Store) UpdateCredentialStatus(ctx context.Context, tx *sql.Tx, alias, status, lastError string) error {
	res, err := tx.ExecContext(ctx, `UPDATE credentials SET last_status = ?, last_checked_at = ?, last_error = NULLIF(?, '') WHERE alias = ?`, status, s.timestamp(), lastError, alias)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetCredential(ctx context.Context, alias string) (*Credential, error) {
	var c Credential
	var checked, lastErr sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT alias, created_at, last_status, last_checked_at, last_error FROM credentials WHERE alias = ?`, alias).
		Scan(&c.Alias, &c.CreatedAt, &c.LastStatus, &checked, &lastErr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.LastCheckedAt = checked.String
	c.LastError = lastErr.String
	return &c, nil
}

func (s *Store) ListCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT alias, created_at, last_status, last_checked_at, last_error FROM credentials ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		var checked, lastErr sql.NullString
		if err := rows.Scan(&c.Alias, &c.CreatedAt, &c.LastStatus, &checked, &lastErr); err != nil {
			return nil, err
		}
		c.LastCheckedAt = checked.String
		c.LastError = lastErr.String
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCredential(ctx context.Context, tx *sql.Tx, alias string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM credentials WHERE alias = ?`, alias)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ReplaceHostingAccounts(ctx context.Context, tx *sql.Tx, alias string, accounts []HostingAccount) error {
	ts := s.timestamp()
	keep := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		keep[a.ID] = true
		_, err := tx.ExecContext(ctx, `
INSERT INTO hosting_accounts (id, alias, order_id, plan, order_status, website_count, discovered_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id, alias) DO UPDATE SET
    order_id = excluded.order_id,
    plan = excluded.plan,
    order_status = excluded.order_status,
    website_count = excluded.website_count,
    updated_at = excluded.updated_at`,
			a.ID, alias, nullInt(a.OrderID), nullStr(a.Plan), nullStr(a.OrderStatus), a.WebsiteCount, ts, ts)
		if err != nil {
			return fmt.Errorf("gagal menyimpan akun hosting %s: %w", a.ID, err)
		}
		_, err = tx.ExecContext(ctx, `
UPDATE hosting_accounts SET
    ssh_host = src.ssh_host, ssh_port = src.ssh_port, ssh_user = src.ssh_user, ssh_fingerprint = src.ssh_fingerprint,
    ssh_status = src.ssh_status, ssh_auth = src.ssh_auth, ssh_last_error = src.ssh_last_error, ssh_checked_at = src.ssh_checked_at, ssh_home = src.ssh_home
FROM (SELECT * FROM hosting_accounts WHERE id = ? AND alias <> ? AND ssh_host IS NOT NULL LIMIT 1) AS src
WHERE hosting_accounts.id = ? AND hosting_accounts.alias = ? AND hosting_accounts.ssh_host IS NULL`, a.ID, alias, a.ID, alias)
		if err != nil {
			return fmt.Errorf("gagal menyalin info SSH akun %s: %w", a.ID, err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM hosting_accounts WHERE alias = ?`, alias)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM hosting_accounts WHERE id = ? AND alias = ?`, id, alias); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListHostingAccounts(ctx context.Context, alias string) ([]HostingAccount, error) {
	query := `SELECT id, alias, order_id, plan, order_status, website_count, ssh_host, ssh_port, ssh_user, ssh_fingerprint, ssh_status, discovered_at, updated_at FROM hosting_accounts`
	var args []any
	if alias != "" {
		query += ` WHERE alias = ?`
		args = append(args, alias)
	}
	query += ` ORDER BY alias, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HostingAccount
	for rows.Next() {
		var a HostingAccount
		var orderID, port sql.NullInt64
		var plan, status, host, user, fp sql.NullString
		if err := rows.Scan(&a.ID, &a.Alias, &orderID, &plan, &status, &a.WebsiteCount, &host, &port, &user, &fp, &a.SSHStatus, &a.DiscoveredAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.OrderID = orderID.Int64
		a.Plan = plan.String
		a.OrderStatus = status.String
		a.SSHHost = host.String
		a.SSHPort = int(port.Int64)
		a.SSHUser = user.String
		a.SSHFingerprint = fp.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AppendAudit(ctx context.Context, tx *sql.Tx, actor, action, target, result, detail string) error {
	exec := func(q string, args ...any) error {
		var err error
		if tx != nil {
			_, err = tx.ExecContext(ctx, q, args...)
		} else {
			_, err = s.db.ExecContext(ctx, q, args...)
		}
		return err
	}
	return exec(`INSERT INTO audit_log (at, actor, action, target, result, detail) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`,
		s.timestamp(), actor, action, target, result, detail)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, actor, action, target, result, COALESCE(detail, '') FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Result, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *Store) WithTxUpdateCredential(ctx context.Context, alias, status, lastError string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return s.UpdateCredentialStatus(ctx, tx, alias, status, lastError)
	})
}
