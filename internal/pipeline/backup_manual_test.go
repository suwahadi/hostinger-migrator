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
	if err := st.InsertCredential(ctx, tx, "b"); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceHostingAccounts(ctx, tx, "b", []store.HostingAccount{{ID: "u2", Alias: "b"}}); err != nil {
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
	sites, err := st.ListWebsites(ctx, "u1")
	if err != nil || len(sites) != 1 {
		t.Fatalf("website fixture: %v %v", sites, err)
	}
	id, err := st.CreateBatch(ctx, store.Batch{SourceAlias: "a", SourceAccount: "u1", DestAlias: "b", DestAccount: "u2"}, []store.Item{{SourceDomain: "a.com", WebsiteID: sites[0].ID, State: "BACKUP_RUNNING"}})
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
	setSource(t, st, "u1_x", ".env")
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, "lewati"); err == nil || !strings.Contains(err.Error(), "tidak ada database") {
		t.Fatalf("lewati tanpa database bersumber tidak diketahui harus ditolak: %v", err)
	}
	setSource(t, st, "u1_x", "api")
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, " Lewati "); err != nil {
		t.Fatal(err)
	}
	task, err := st.GetManualTask(ctx, it.ID, "MANUAL_DB_SOURCE")
	if err != nil || task.Value != "lewati:u1_x" {
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

func setSource(t *testing.T, st *store.Store, name, source string) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(), "UPDATE databases SET source = ? WHERE name = ?", source, name); err != nil {
		t.Fatal(err)
	}
}

func TestSkippedDatabasesSnapshot(t *testing.T) {
	ctx := context.Background()
	st, it := skipFixture(t)
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, "lewati"); err != nil {
		t.Fatal(err)
	}
	setSource(t, st, "u1_ok", "api")
	if got, err := SkippedDatabases(ctx, st, it); err != nil || !reflect.DeepEqual(got, []string{"u1_x"}) {
		t.Fatalf("database lain yang kehilangan sumber tidak boleh ikut dilewati: %v %v", got, err)
	}
	setSource(t, st, "u1_x", ".env")
	if got, err := SkippedDatabases(ctx, st, it); err != nil || len(got) != 0 {
		t.Fatalf("database yang sumbernya kini diketahui tidak dilewati lagi: %v %v", got, err)
	}
	if got, err := SkipSnapshot(ctx, st, it); err != nil || !reflect.DeepEqual(got, []string{"u1_x"}) {
		t.Fatalf("snapshot tetap: %v %v", got, err)
	}
}

func TestLoadExcludesSkipped(t *testing.T) {
	ctx := context.Background()
	st, it := skipFixture(t)
	if _, err := Confirm(ctx, st, "MANUAL_DB_SOURCE", "a.com", it.BatchID, "lewati"); err != nil {
		t.Fatal(err)
	}
	r := &Runner{}
	r.Store = st
	ic, err := r.load(ctx, it)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range ic.Databases {
		names = append(names, d.Name)
	}
	if !reflect.DeepEqual(names, []string{"u1_ok"}) {
		t.Fatalf("database item = %v", names)
	}
}

func TestSiteConfigRead(t *testing.T) {
	w := &store.Website{Domain: "a.com", UnitPath: "/home/u1/domains/a.com"}
	cases := []struct {
		run  *store.InventoryRun
		want bool
	}{
		{&store.InventoryRun{SSHUsed: false}, false},
		{&store.InventoryRun{SSHUsed: true}, true},
		{&store.InventoryRun{SSHUsed: true, Warnings: []string{"Probe SSH a.com gagal: timeout"}}, false},
		{&store.InventoryRun{SSHUsed: true, Warnings: []string{"Folder /home/u1/domains/a.com tidak ditemukan di server."}}, false},
		{&store.InventoryRun{SSHUsed: true, Warnings: []string{"Probe SSH b.com gagal: timeout"}}, true},
		{nil, false},
	}
	for i, c := range cases {
		if got := siteConfigRead(c.run, w); got != c.want {
			t.Errorf("kasus %d: %v", i, got)
		}
	}
}
