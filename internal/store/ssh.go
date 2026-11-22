package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	SSHNotConfigured = "BELUM_DIATUR"
	SSHReady         = "SSH_READY"
	SSHFailed        = "SSH_GAGAL"
	SSHBlocked       = "BLOCKED"

	SSHAuthKey      = "key"
	SSHAuthPassword = "password"
)

type SSHInfo struct {
	AccountID   string
	Host        string
	Port        int
	User        string
	Fingerprint string
	Status      string
	Auth        string
	LastError   string
	CheckedAt   string
	Home        string
	Aliases     []string
	Plan        string
}

func (s *Store) ListSSH(ctx context.Context) ([]SSHInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, alias, COALESCE(ssh_host, ''), COALESCE(ssh_port, 0), COALESCE(ssh_user, ''), COALESCE(ssh_fingerprint, ''),
       ssh_status, COALESCE(ssh_auth, ''), COALESCE(ssh_last_error, ''), COALESCE(ssh_checked_at, ''), COALESCE(ssh_home, ''), COALESCE(plan, '')
FROM hosting_accounts ORDER BY id, alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SSHInfo
	index := map[string]int{}
	for rows.Next() {
		var i SSHInfo
		var alias string
		if err := rows.Scan(&i.AccountID, &alias, &i.Host, &i.Port, &i.User, &i.Fingerprint, &i.Status, &i.Auth, &i.LastError, &i.CheckedAt, &i.Home, &i.Plan); err != nil {
			return nil, err
		}
		if pos, ok := index[i.AccountID]; ok {
			out[pos].Aliases = append(out[pos].Aliases, alias)
			if out[pos].Host == "" && i.Host != "" {
				aliases := out[pos].Aliases
				i.Aliases = aliases
				out[pos] = i
			}
			continue
		}
		i.Aliases = []string{alias}
		index[i.AccountID] = len(out)
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) GetSSH(ctx context.Context, accountID string) (*SSHInfo, error) {
	all, err := s.ListSSH(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range all {
		if i.AccountID == accountID {
			return &i, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) SaveSSH(ctx context.Context, i SSHInfo) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE hosting_accounts SET ssh_host = NULLIF(?, ''), ssh_port = NULLIF(?, 0), ssh_user = NULLIF(?, ''), ssh_fingerprint = NULLIF(?, ''),
       ssh_status = ?, ssh_auth = NULLIF(?, ''), ssh_last_error = NULLIF(?, ''), ssh_checked_at = ?, ssh_home = NULLIF(?, ''), updated_at = ?
WHERE id = ?`, i.Host, i.Port, i.User, i.Fingerprint, i.Status, i.Auth, i.LastError, s.timestamp(), i.Home, s.timestamp(), i.AccountID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type Capability struct {
	AccountID  string
	Code       string
	Status     string
	Note       string
	DetailJSON string
	TestedAt   string
}

func (s *Store) SaveCapabilities(ctx context.Context, accountID string, caps []Capability) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		ts := s.timestamp()
		for _, c := range caps {
			_, err := tx.ExecContext(ctx, `
INSERT INTO capabilities (account_id, code, status, note, tested_at, detail_json) VALUES (?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''))
ON CONFLICT (account_id, code) DO UPDATE SET status = excluded.status, note = excluded.note, tested_at = excluded.tested_at, detail_json = excluded.detail_json`,
				accountID, c.Code, c.Status, c.Note, ts, c.DetailJSON)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListCapabilities(ctx context.Context, accountID string) ([]Capability, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, code, status, COALESCE(note, ''), COALESCE(detail_json, ''), COALESCE(tested_at, '') FROM capabilities WHERE account_id = ? ORDER BY code`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Capability
	for rows.Next() {
		var c Capability
		if err := rows.Scan(&c.AccountID, &c.Code, &c.Status, &c.Note, &c.DetailJSON, &c.TestedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var ErrNoAccount = errors.New("akun hosting tidak ditemukan; jalankan 'migrator account add' atau 'migrator account test' dulu")
