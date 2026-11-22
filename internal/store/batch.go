package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	BatchPlanned = "PLANNED"
	ItemPlanned  = "PLANNED"
	ItemBlocked  = "BLOCKED"
)

type Batch struct {
	ID            string
	SourceAlias   string
	SourceAccount string
	DestAlias     string
	DestAccount   string
	Status        string
	CreatedAt     string
	PlanJSON      string
}

type Item struct {
	ID            int64
	BatchID       string
	WebsiteID     int64
	SourceDomain  string
	State         string
	ExpectedFinal string
	PlanNotes     string
	LastError     string
}

func (s *Store) NextBatchID(ctx context.Context, tx *sql.Tx) (string, error) {
	prefix := "MIG-" + s.now().UTC().Format("20060102") + "-"
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM batches WHERE id LIKE ?`, prefix+"%").Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%03d", prefix, n+1), nil
}

func (s *Store) CreateBatch(ctx context.Context, b Batch, items []Item) (string, error) {
	var id string
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = s.NextBatchID(ctx, tx)
		if err != nil {
			return err
		}
		ts := s.timestamp()
		_, err = tx.ExecContext(ctx, `INSERT INTO batches (id, source_alias, source_account, dest_alias, dest_account, status, created_at, plan_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, b.SourceAlias, b.SourceAccount, b.DestAlias, b.DestAccount, BatchPlanned, ts, b.PlanJSON)
		if err != nil {
			return err
		}
		for _, it := range items {
			var wid any
			if it.WebsiteID > 0 {
				wid = it.WebsiteID
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO items (batch_id, source_website_id, source_domain, state, expected_final, plan_notes, last_error, updated_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)`,
				id, wid, it.SourceDomain, it.State, it.ExpectedFinal, it.PlanNotes, it.LastError, ts)
			if err != nil {
				return fmt.Errorf("gagal menyimpan item %s: %w", it.SourceDomain, err)
			}
		}
		var domains []string
		for _, it := range items {
			domains = append(domains, it.SourceDomain)
		}
		return s.AppendAudit(ctx, tx, "operator", "plan.create", id, BatchPlanned,
			fmt.Sprintf("%s/%s → %s/%s: %s", b.SourceAlias, b.SourceAccount, b.DestAlias, b.DestAccount, strings.Join(domains, ", ")))
	})
	return id, err
}

func (s *Store) GetBatch(ctx context.Context, id string) (*Batch, []Item, error) {
	var b Batch
	var plan sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, source_alias, source_account, dest_alias, dest_account, status, created_at, plan_json FROM batches WHERE id = ?`, id).
		Scan(&b.ID, &b.SourceAlias, &b.SourceAccount, &b.DestAlias, &b.DestAccount, &b.Status, &b.CreatedAt, &plan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	b.PlanJSON = plan.String
	rows, err := s.db.QueryContext(ctx, `SELECT id, batch_id, COALESCE(source_website_id, 0), source_domain, state, COALESCE(expected_final, ''), COALESCE(plan_notes, ''), COALESCE(last_error, '') FROM items WHERE batch_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.BatchID, &it.WebsiteID, &it.SourceDomain, &it.State, &it.ExpectedFinal, &it.PlanNotes, &it.LastError); err != nil {
			return nil, nil, err
		}
		items = append(items, it)
	}
	return &b, items, rows.Err()
}

func (s *Store) ListBatches(ctx context.Context) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_alias, source_account, dest_alias, dest_account, status, created_at FROM batches ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Batch
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID, &b.SourceAlias, &b.SourceAccount, &b.DestAlias, &b.DestAccount, &b.Status, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) ActiveItemDomains(ctx context.Context, accountID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT i.source_domain, i.batch_id FROM items i JOIN batches b ON b.id = i.batch_id
WHERE b.source_account = ? AND i.state NOT IN ('COMPLETED', 'CLEANED', 'DUMMY_ONLY', 'BLOCKED', 'DIBATALKAN')`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var d, b string
		if err := rows.Scan(&d, &b); err != nil {
			return nil, err
		}
		out[d] = b
	}
	return out, rows.Err()
}
