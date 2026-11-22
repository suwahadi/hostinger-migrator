package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAppliesPragmasAndMigrations(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if v, _ := s.Pragma(ctx, "journal_mode"); v != "wal" {
		t.Fatalf("journal_mode = %s", v)
	}
	if v, _ := s.Pragma(ctx, "foreign_keys"); v != "1" {
		t.Fatalf("foreign_keys = %s", v)
	}
	if v, _ := s.Pragma(ctx, "busy_timeout"); v != "5000" {
		t.Fatalf("busy_timeout = %s", v)
	}
	got, err := s.AppliedMigrations(ctx)
	if err != nil || len(got) != 1 || got[0] != "0001_skema_awal" {
		t.Fatalf("migrasi = %v, %v", got, err)
	}
	for _, table := range []string{"credentials", "hosting_accounts", "capabilities", "websites", "databases", "mailboxes", "dns_snapshots", "batches", "items", "steps", "artifacts", "verifications", "manual_tasks", "approvals", "audit_log", "worker_lock"} {
		var n int
		if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("tabel %s tidak ada", table)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migrator.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s2.AppliedMigrations(ctx)
	if len(got) != 1 {
		t.Fatalf("migrasi berulang: %v", got)
	}
}

func TestCapabilityStatusConstraint(t *testing.T) {
	s := openTest(t)
	_, err := s.DB().Exec(`INSERT INTO capabilities (account_id, code, status) VALUES ('u1', 'SSH', 'MUNGKIN')`)
	if err == nil {
		t.Fatal("status kapabilitas di luar daftar harus ditolak")
	}
}

func TestCredentialLifecycleAndCascade(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.InsertCredential(ctx, tx, "xyz"); err != nil {
			return err
		}
		return s.ReplaceHostingAccounts(ctx, tx, "xyz", []HostingAccount{
			{ID: "u111", OrderID: 10, Plan: "hostinger_premium", OrderStatus: "active", WebsiteCount: 2},
			{ID: "u222", OrderID: 11, Plan: "hostinger_single", WebsiteCount: 1},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.CredentialExists(ctx, "xyz"); !ok {
		t.Fatal("credential harus ada")
	}
	accs, _ := s.ListHostingAccounts(ctx, "xyz")
	if len(accs) != 2 || accs[0].ID != "u111" || accs[0].SSHStatus != "BELUM_DIATUR" {
		t.Fatalf("akun = %+v", accs)
	}
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		return s.ReplaceHostingAccounts(ctx, tx, "xyz", []HostingAccount{{ID: "u111", WebsiteCount: 5}})
	})
	if err != nil {
		t.Fatal(err)
	}
	accs, _ = s.ListHostingAccounts(ctx, "xyz")
	if len(accs) != 1 || accs[0].WebsiteCount != 5 {
		t.Fatalf("akun lama harus dihapus: %+v", accs)
	}
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		return s.UpdateCredentialStatus(ctx, tx, "xyz", CredentialRejected, "401")
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetCredential(ctx, "xyz")
	if c.LastStatus != CredentialRejected || c.LastError != "401" {
		t.Fatalf("status = %+v", c)
	}
	err = s.WithTx(ctx, func(tx *sql.Tx) error { return s.DeleteCredential(ctx, tx, "xyz") })
	if err != nil {
		t.Fatal(err)
	}
	accs, _ = s.ListHostingAccounts(ctx, "")
	if len(accs) != 0 {
		t.Fatal("akun hosting harus ikut terhapus")
	}
	if _, err := s.GetCredential(ctx, "xyz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.AppendAudit(ctx, nil, "operator", "account.add", "xyz", "OK", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAudit(ctx, nil, "robot", "x", "y", "OK", ""); err == nil {
		t.Fatal("aktor tidak valid harus ditolak")
	}
	entries, _ := s.ListAudit(ctx, 10)
	if len(entries) != 1 || entries[0].Action != "account.add" {
		t.Fatalf("audit = %+v", entries)
	}
}
