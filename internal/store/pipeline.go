package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	SideWaitingManual = "WAITING_MANUAL"
	SideBlocked       = "BLOCKED"
	SideFailed        = "FAILED"

	ManualOpen      = "MENUNGGU"
	ManualConfirmed = "DIKONFIRMASI"

	StepRunning = "RUNNING"
	StepDone    = "SELESAI"
	StepFailed  = "GAGAL"
)

var ErrStateChanged = errors.New("status item berubah oleh proses lain")

type DestDB struct {
	Source   string `json:"sumber"`
	Name     string `json:"nama"`
	User     string `json:"user"`
	Host     string `json:"host"`
	Created  bool   `json:"dibuat"`
	Imported bool   `json:"diimport"`
}

type PipelineItem struct {
	ID                int64
	BatchID           string
	WebsiteID         int64
	SourceDomain      string
	DestWebsiteRef    string
	DummyDomain       string
	DestRootPath      string
	State             string
	SideState         string
	ManualCode        string
	RetryCount        int
	LastError         string
	ExpectedFinal     string
	PlanNotes         string
	DestIP            string
	SourceTempDomain  string
	SearchReplaceDone bool
	SSLExcluded       bool
	FinalSyncDone     bool
	DestDBs           []DestDB
	EmailState        string
	StartedAt         string
	FinishedAt        string
	UpdatedAt         string
}

const itemColumns = `id, batch_id, COALESCE(source_website_id, 0), source_domain, COALESCE(dest_website_ref, ''), COALESCE(dummy_domain, ''),
    COALESCE(dest_root_path, ''), state, COALESCE(side_state, ''), COALESCE(manual_code, ''), retry_count, COALESCE(last_error, ''),
    COALESCE(expected_final, ''), COALESCE(plan_notes, ''), COALESCE(dest_ip, ''), COALESCE(source_temp_domain, ''), search_replace_done,
    ssl_excluded, final_sync_done, dest_db_json, COALESCE(email_state, ''), COALESCE(started_at, ''), COALESCE(finished_at, ''), updated_at`

type scanner interface{ Scan(...any) error }

func scanItem(sc scanner) (*PipelineItem, error) {
	var it PipelineItem
	var dbs sql.NullString
	err := sc.Scan(&it.ID, &it.BatchID, &it.WebsiteID, &it.SourceDomain, &it.DestWebsiteRef, &it.DummyDomain, &it.DestRootPath, &it.State,
		&it.SideState, &it.ManualCode, &it.RetryCount, &it.LastError, &it.ExpectedFinal, &it.PlanNotes, &it.DestIP, &it.SourceTempDomain,
		&it.SearchReplaceDone, &it.SSLExcluded, &it.FinalSyncDone, &dbs, &it.EmailState, &it.StartedAt, &it.FinishedAt, &it.UpdatedAt)
	if err != nil {
		return nil, err
	}
	fromJSON(dbs, &it.DestDBs)
	return &it, nil
}

func (s *Store) GetItem(ctx context.Context, id int64) (*PipelineItem, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

func (s *Store) FindItem(ctx context.Context, domain, batchID string) (*PipelineItem, error) {
	q := `SELECT ` + itemColumns + ` FROM items WHERE source_domain = ?`
	args := []any{domain}
	if batchID != "" {
		q += ` AND batch_id = ?`
		args = append(args, batchID)
	}
	q += ` ORDER BY id DESC LIMIT 1`
	it, err := scanItem(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

func (s *Store) ListItems(ctx context.Context, batchID string) ([]*PipelineItem, error) {
	q := `SELECT ` + itemColumns + ` FROM items`
	var args []any
	if batchID != "" {
		q += ` WHERE batch_id = ?`
		args = append(args, batchID)
	}
	q += ` ORDER BY id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PipelineItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) saveItem(ctx context.Context, tx *sql.Tx, it *PipelineItem) error {
	var dbs any
	if len(it.DestDBs) > 0 {
		b, _ := json.Marshal(it.DestDBs)
		dbs = string(b)
	}
	it.UpdatedAt = s.timestamp()
	_, err := tx.ExecContext(ctx, `
UPDATE items SET dest_website_ref = NULLIF(?, ''), dummy_domain = NULLIF(?, ''), dest_root_path = NULLIF(?, ''), state = ?, side_state = NULLIF(?, ''),
    manual_code = NULLIF(?, ''), retry_count = ?, last_error = NULLIF(?, ''), dest_ip = NULLIF(?, ''), source_temp_domain = NULLIF(?, ''),
    search_replace_done = ?, ssl_excluded = ?, final_sync_done = ?, dest_db_json = ?, email_state = NULLIF(?, ''), started_at = NULLIF(?, ''),
    finished_at = NULLIF(?, ''), updated_at = ?
WHERE id = ?`, it.DestWebsiteRef, it.DummyDomain, it.DestRootPath, it.State, it.SideState, it.ManualCode, it.RetryCount, it.LastError, it.DestIP,
		it.SourceTempDomain, it.SearchReplaceDone, it.SSLExcluded, it.FinalSyncDone, dbs, it.EmailState, it.StartedAt, it.FinishedAt, it.UpdatedAt, it.ID)
	return err
}

func (s *Store) UpdateItem(ctx context.Context, id int64, actor, action string, fn func(it *PipelineItem) error) (*PipelineItem, error) {
	var out *PipelineItem
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		it, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE id = ?`, id))
		if err != nil {
			return err
		}
		before := it.State + "/" + it.SideState
		if err := fn(it); err != nil {
			return err
		}
		if err := s.saveItem(ctx, tx, it); err != nil {
			return err
		}
		if action != "" {
			detail := before + " → " + it.State + "/" + it.SideState
			if it.LastError != "" && it.SideState != "" {
				detail += ": " + it.LastError
			}
			if err := s.AppendAudit(ctx, tx, actor, action, it.SourceDomain, it.State, detail); err != nil {
				return err
			}
		}
		out = it
		return nil
	})
	return out, err
}

func (s *Store) Transition(ctx context.Context, id int64, from, to string, fn func(it *PipelineItem) error) (*PipelineItem, error) {
	return s.UpdateItem(ctx, id, "worker", "item.transition", func(it *PipelineItem) error {
		if from != "" && it.State != from {
			return fmt.Errorf("%w: harapan %s, sekarang %s", ErrStateChanged, from, it.State)
		}
		it.State = to
		it.SideState = ""
		it.ManualCode = ""
		it.LastError = ""
		if fn != nil {
			return fn(it)
		}
		return nil
	})
}

func (s *Store) SetBatchStatus(ctx context.Context, id, status string) error {
	ts := s.timestamp()
	_, err := s.db.ExecContext(ctx, `UPDATE batches SET status = ?, started_at = COALESCE(started_at, CASE WHEN ? <> 'PLANNED' THEN ? END),
    finished_at = CASE WHEN ? IN ('SELESAI', 'SELESAI_DENGAN_CATATAN') THEN ? ELSE NULL END WHERE id = ?`, status, status, ts, status, ts, id)
	return err
}

func (s *Store) StartStep(ctx context.Context, itemID int64, name string) error {
	ts := s.timestamp()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO steps (item_id, name, status, attempts, started_at) VALUES (?, ?, ?, 1, ?)
ON CONFLICT (item_id, name) DO UPDATE SET status = excluded.status, attempts = steps.attempts + 1, started_at = excluded.started_at, finished_at = NULL, error = NULL`,
		itemID, name, StepRunning, ts)
	return err
}

func (s *Store) FinishStep(ctx context.Context, itemID int64, name, status, checkpoint, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE steps SET status = ?, checkpoint_json = COALESCE(NULLIF(?, ''), checkpoint_json), finished_at = ?, error = NULLIF(?, '') WHERE item_id = ? AND name = ?`,
		status, checkpoint, s.timestamp(), errMsg, itemID, name)
	return err
}

type Step struct {
	Name       string
	Status     string
	Attempts   int
	Checkpoint string
	Error      string
	StartedAt  string
	FinishedAt string
}

func (s *Store) ListSteps(ctx context.Context, itemID int64) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, status, attempts, COALESCE(checkpoint_json, ''), COALESCE(error, ''), COALESCE(started_at, ''), COALESCE(finished_at, '') FROM steps WHERE item_id = ? ORDER BY id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.Name, &st.Status, &st.Attempts, &st.Checkpoint, &st.Error, &st.StartedAt, &st.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) ResetRunningSteps(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE steps SET status = ?, error = 'dihentikan sebelum selesai; diulang', finished_at = ? WHERE status = ?`, StepFailed, s.timestamp(), StepRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type Artifact struct {
	ID        int64
	ItemID    int64
	Kind      string
	Path      string
	Size      int64
	SHA256    string
	Validated bool
	CreatedAt string
}

func (s *Store) SaveArtifact(ctx context.Context, a Artifact) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO artifacts (item_id, kind, local_path, size_bytes, sha256, validated, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (item_id, kind, local_path) DO UPDATE SET size_bytes = excluded.size_bytes, sha256 = excluded.sha256, validated = excluded.validated, created_at = excluded.created_at`,
		a.ItemID, a.Kind, a.Path, a.Size, a.SHA256, a.Validated, s.timestamp())
	return err
}

func (s *Store) DeleteArtifacts(ctx context.Context, itemID int64, kinds ...string) error {
	for _, k := range kinds {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE item_id = ? AND kind = ?`, itemID, k); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListArtifacts(ctx context.Context, itemID int64) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, item_id, kind, local_path, COALESCE(size_bytes, 0), COALESCE(sha256, ''), validated, created_at FROM artifacts WHERE item_id = ? ORDER BY id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.ItemID, &a.Kind, &a.Path, &a.Size, &a.SHA256, &a.Validated, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ValidatedArtifact(ctx context.Context, itemID int64, kind string) (*Artifact, error) {
	arts, err := s.ListArtifacts(ctx, itemID)
	if err != nil {
		return nil, err
	}
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Kind == kind && arts[i].Validated {
			return &arts[i], nil
		}
	}
	return nil, ErrNotFound
}

type Verification struct {
	CheckType string
	Result    string
	Detail    string
	CheckedAt string
}

func (s *Store) ReplaceVerifications(ctx context.Context, itemID int64, prefix string, vs []Verification) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM verifications WHERE item_id = ? AND check_type LIKE ?`, itemID, prefix+"%"); err != nil {
			return err
		}
		for _, v := range vs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO verifications (item_id, check_type, result, detail, checked_at) VALUES (?, ?, ?, NULLIF(?, ''), ?)`,
				itemID, v.CheckType, v.Result, v.Detail, s.timestamp()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListVerifications(ctx context.Context, itemID int64) ([]Verification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT check_type, result, COALESCE(detail, ''), checked_at FROM verifications WHERE item_id = ? ORDER BY id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Verification
	for rows.Next() {
		var v Verification
		if err := rows.Scan(&v.CheckType, &v.Result, &v.Detail, &v.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type ManualTask struct {
	ID           int64
	ItemID       int64
	BatchID      string
	Domain       string
	Code         string
	Instructions string
	Status       string
	Value        string
	CreatedAt    string
	ConfirmedAt  string
}

func (s *Store) OpenManualTask(ctx context.Context, itemID int64, code, instructions string) (*ManualTask, bool, error) {
	if t, err := s.GetManualTask(ctx, itemID, code); err == nil {
		if t.Status == ManualOpen {
			return t, false, nil
		}
		return t, false, nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO manual_tasks (item_id, code, instructions, status, created_at) VALUES (?, ?, ?, ?, ?)`, itemID, code, instructions, ManualOpen, s.timestamp())
	if err != nil {
		return nil, false, err
	}
	t, err := s.GetManualTask(ctx, itemID, code)
	return t, true, err
}

func (s *Store) ReopenManualTask(ctx context.Context, itemID int64, code, instructions string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE manual_tasks SET status = ?, instructions = ?, confirmed_at = NULL, value = NULL WHERE item_id = ? AND code = ?`, ManualOpen, instructions, itemID, code)
	return err
}

const manualColumns = `m.id, COALESCE(m.item_id, 0), COALESCE(i.batch_id, ''), COALESCE(i.source_domain, ''), m.code, m.instructions, m.status, COALESCE(m.value, ''), m.created_at, COALESCE(m.confirmed_at, '')`

func scanManual(sc scanner) (*ManualTask, error) {
	var t ManualTask
	err := sc.Scan(&t.ID, &t.ItemID, &t.BatchID, &t.Domain, &t.Code, &t.Instructions, &t.Status, &t.Value, &t.CreatedAt, &t.ConfirmedAt)
	return &t, err
}

func (s *Store) GetManualTask(ctx context.Context, itemID int64, code string) (*ManualTask, error) {
	t, err := scanManual(s.db.QueryRowContext(ctx, `SELECT `+manualColumns+` FROM manual_tasks m LEFT JOIN items i ON i.id = m.item_id WHERE m.item_id = ? AND m.code = ? ORDER BY m.id DESC LIMIT 1`, itemID, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Store) ListManualTasks(ctx context.Context, onlyOpen bool) ([]*ManualTask, error) {
	q := `SELECT ` + manualColumns + ` FROM manual_tasks m LEFT JOIN items i ON i.id = m.item_id`
	var args []any
	if onlyOpen {
		q += ` WHERE m.status = ?`
		args = append(args, ManualOpen)
	}
	q += ` ORDER BY m.id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ManualTask
	for rows.Next() {
		t, err := scanManual(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ConfirmManualTask(ctx context.Context, itemID int64, code, value string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE manual_tasks SET status = ?, value = NULLIF(?, ''), confirmed_at = ? WHERE item_id = ? AND code = ? AND status = ?`,
			ManualConfirmed, value, s.timestamp(), itemID, code, ManualOpen)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		var domain string
		tx.QueryRowContext(ctx, `SELECT source_domain FROM items WHERE id = ?`, itemID).Scan(&domain)
		return s.AppendAudit(ctx, tx, "operator", "manual.confirm", domain, ManualConfirmed, code)
	})
}

type Approval struct {
	BatchID    string
	Kind       string
	Items      []string
	ApprovedAt string
}

func (s *Store) SaveApproval(ctx context.Context, batchID, kind string, items []string) error {
	b, _ := json.Marshal(items)
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO approvals (batch_id, kind, item_list_json, approved_at) VALUES (?, ?, ?, ?)`, batchID, kind, string(b), s.timestamp()); err != nil {
			return err
		}
		return s.AppendAudit(ctx, tx, "operator", "approval."+kind, batchID, "DISETUJUI", string(b))
	})
}

func (s *Store) GetApprovals(ctx context.Context, batchID, kind string) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT batch_id, kind, item_list_json, approved_at FROM approvals WHERE batch_id = ? AND kind = ? ORDER BY id`, batchID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		var a Approval
		var raw string
		if err := rows.Scan(&a.BatchID, &a.Kind, &raw, &a.ApprovedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(raw), &a.Items)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) IsApproved(ctx context.Context, batchID, kind, domain string) (bool, error) {
	approvals, err := s.GetApprovals(ctx, batchID, kind)
	if err != nil {
		return false, err
	}
	for _, a := range approvals {
		for _, d := range a.Items {
			if d == domain {
				return true, nil
			}
		}
	}
	return false, nil
}

type LockInfo struct {
	PID           int
	LeaseUntil    time.Time
	StopRequested bool
	StartedAt     string
}

func (s *Store) AcquireWorkerLock(ctx context.Context, pid int, lease time.Duration) (bool, *LockInfo, error) {
	var acquired bool
	var holder *LockInfo
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.now().UTC()
		var cur LockInfo
		var until string
		var started sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT pid, lease_until, stop_requested, started_at FROM worker_lock WHERE id = 1`).Scan(&cur.PID, &until, &cur.StopRequested, &started)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			cur.LeaseUntil, _ = time.Parse(time.RFC3339, until)
			cur.StartedAt = started.String
			if cur.PID != pid && cur.LeaseUntil.After(now) {
				holder = &cur
				return nil
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO worker_lock (id, pid, lease_until, stop_requested, started_at) VALUES (1, ?, ?, 0, ?)
ON CONFLICT (id) DO UPDATE SET pid = excluded.pid, lease_until = excluded.lease_until, stop_requested = 0, started_at = excluded.started_at`,
			pid, now.Add(lease).Format(time.RFC3339), now.Format(time.RFC3339))
		acquired = err == nil
		return err
	})
	return acquired, holder, err
}

func (s *Store) RenewWorkerLock(ctx context.Context, pid int, lease time.Duration) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE worker_lock SET lease_until = ? WHERE id = 1 AND pid = ?`, s.now().UTC().Add(lease).Format(time.RFC3339), pid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) ReleaseWorkerLock(ctx context.Context, pid int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM worker_lock WHERE id = 1 AND pid = ?`, pid)
	return err
}

func (s *Store) RequestWorkerStop(ctx context.Context) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE worker_lock SET stop_requested = 1 WHERE id = 1`)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) WorkerLock(ctx context.Context) (*LockInfo, error) {
	var l LockInfo
	var until string
	var started sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT pid, lease_until, stop_requested, started_at FROM worker_lock WHERE id = 1`).Scan(&l.PID, &until, &l.StopRequested, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.LeaseUntil, _ = time.Parse(time.RFC3339, until)
	l.StartedAt = started.String
	return &l, nil
}

func (s *Store) Now() time.Time {
	return s.now()
}

type DNSSnapshot struct {
	ID            int64
	Domain        string
	ItemID        int64
	RecordsJSON   string
	APISnapshotID int64
	Reason        string
	TakenAt       string
}

func (s *Store) SaveDNSSnapshot(ctx context.Context, d DNSSnapshot) (int64, error) {
	var itemID any
	if d.ItemID > 0 {
		itemID = d.ItemID
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO dns_snapshots (domain, item_id, records_json, api_snapshot_id, reason, taken_at) VALUES (?, ?, ?, NULLIF(?, 0), NULLIF(?, ''), ?)`,
		d.Domain, itemID, d.RecordsJSON, d.APISnapshotID, d.Reason, s.timestamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListDNSSnapshots(ctx context.Context, domain string) ([]DNSSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, domain, COALESCE(item_id, 0), records_json, COALESCE(api_snapshot_id, 0), COALESCE(reason, ''), taken_at FROM dns_snapshots WHERE domain = ? ORDER BY id`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNSSnapshot
	for rows.Next() {
		var d DNSSnapshot
		if err := rows.Scan(&d.ID, &d.Domain, &d.ItemID, &d.RecordsJSON, &d.APISnapshotID, &d.Reason, &d.TakenAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetWebsiteByID(ctx context.Context, id int64) (*Website, error) {
	w, err := scanWebsite(s.db.QueryRowContext(ctx, `SELECT `+websiteColumns+` FROM websites WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Store) GetHostingAccount(ctx context.Context, alias, id string) (*HostingAccount, error) {
	accs, err := s.ListHostingAccounts(ctx, alias)
	if err != nil {
		return nil, err
	}
	for _, a := range accs {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, ErrNotFound
}
