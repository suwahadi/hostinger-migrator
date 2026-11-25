package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/store"
)

func TestBackupManualUnknownSource(t *testing.T) {
	ic := &ItemContext{
		Item:  &store.PipelineItem{SourceDomain: "a.com"},
		Batch: &store.Batch{SourceAccount: "u874"},
		Databases: []store.DatabaseRow{
			{Name: "u874_ok", Source: "public_html/wp-config.php"},
			{Name: "u874_x", Source: "api"},
		},
	}
	cases := map[bool]string{true: "sudah memakai SSH", false: "dibuat sebelum SSH siap"}
	for usedSSH, reason := range cases {
		err := backupManual(fmt.Errorf("%w: x", backup.ErrUnknownDBSource), ic, usedSSH)
		var m *ManualError
		if !errors.As(err, &m) || m.Code != "MANUAL_DB_SOURCE" {
			t.Fatalf("harus tindakan manual MANUAL_DB_SOURCE: %v", err)
		}
		for _, want := range []string{"u874_x", "a.com", reason, "migrator inventory --akun u874 --refresh", "migrator confirm MANUAL_DB_SOURCE a.com", "migrator confirm MANUAL_DB_SOURCE a.com --nilai lewati"} {
			if !strings.Contains(m.Instructions, want) {
				t.Errorf("SSH=%v: instruksi tidak memuat %q:\n%s", usedSSH, want, m.Instructions)
			}
		}
		if strings.Contains(m.Instructions, "u874_ok") {
			t.Errorf("instruksi tidak boleh menyebut database yang sumbernya diketahui:\n%s", m.Instructions)
		}
	}
	other := errors.New("lain")
	if got := backupManual(other, ic, false); got != other {
		t.Fatalf("error lain harus dikembalikan apa adanya: %v", got)
	}
}

func skipFixture(t *testing.T) (*store.Store, *store.PipelineItem) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertCredential(ctx, tx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceHostingAccounts(ctx, tx, "a", []store.HostingAccount{{ID: "u1", Alias: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInventory(ctx, &store.InventorySnapshot{AccountID: "u1", Alias: "a",
		Websites: []store.Website{{Domain: "a.com", UnitPath: "/home/u1/domains/a.com", VhostType: "main", IsEnabled: true, SizeBytes: -1, FileCount: -1}},
		Databases: []store.DatabaseRow{
			{WebsiteDomain: "a.com", Name: "u1_ok", Source: ".env"},
			{WebsiteDomain: "a.com", Name: "u1_x", Source: "api", InAPI: true},
		}}); err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateBatch(ctx, store.Batch{SourceAlias: "a", SourceAccount: "u1", DestAlias: "b", DestAccount: "u2"}, []store.Item{{SourceDomain: "a.com", State: "BACKUP_RUNNING"}})
	if err != nil {
		t.Fatal(err)
	}
	it, err := st.FindItem(ctx, "a.com", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.OpenManualTask(ctx, it.ID, "MANUAL_DB_SOURCE", "instruksi"); err != nil {
		t.Fatal(err)
	}
	return st, it
}

func TestConfirmDBSourceValue(t *testing.T) {
	ctx := context.Background()
	st, it := skipFixture(t)
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, "hapus"); err == nil || !strings.Contains(err.Error(), "lewati") {
		t.Fatalf("nilai selain kosong/lewati harus ditolak: %v", err)
	}
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, " Lewati "); err != nil {
		t.Fatal(err)
	}
	task, err := st.GetManualTask(ctx, it.ID, "MANUAL_DB_SOURCE")
	if err != nil || task.Value != "lewati" {
		t.Fatalf("nilai tersimpan = %+v %v", task, err)
	}
	entries, _ := st.ListAudit(ctx, 50)
	found := false
	for _, e := range entries {
		if e.Action == "item.db_skipped" && e.Target == "a.com" && strings.Contains(e.Detail, "u1_x") && !strings.Contains(e.Detail, "u1_ok") {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit item.db_skipped tidak ditemukan: %+v", entries)
	}
}

func TestSkippedDatabases(t *testing.T) {
	ctx := context.Background()
	st, it := skipFixture(t)
	if got, err := SkippedDatabases(ctx, st, it); err != nil || len(got) != 0 {
		t.Fatalf("sebelum konfirmasi: %v %v", got, err)
	}
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := SkippedDatabases(ctx, st, it); err != nil || len(got) != 0 {
		t.Fatalf("konfirmasi tanpa nilai tidak melewati apa pun: %v %v", got, err)
	}
	if _, _, err := st.OpenManualTask(ctx, it.ID, "MANUAL_DB_SOURCE", "instruksi"); err != nil {
		t.Fatal(err)
	}
	st.ReopenManualTask(ctx, it.ID, "MANUAL_DB_SOURCE", "instruksi")
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, "lewati"); err != nil {
		t.Fatal(err)
	}
	got, err := SkippedDatabases(ctx, st, it)
	if err != nil || !reflect.DeepEqual(got, []string{"u1_x"}) {
		t.Fatalf("setelah lewati: %v %v", got, err)
	}
}
