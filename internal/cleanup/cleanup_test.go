package cleanup_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/internal/cleanup"
	"hostinger-migrator/internal/cutover"
	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/report"
	"hostinger-migrator/tests/testenv"
)

func resolver(e *testenv.Env) func(ctx context.Context, host string) ([]string, error) {
	return func(ctx context.Context, host string) ([]string, error) {
		for _, rr := range e.API.Zone(host) {
			if rr["name"] == "@" && rr["type"] == "A" {
				b, _ := json.Marshal(rr["records"])
				var recs []map[string]any
				json.Unmarshal(b, &recs)
				return []string{recs[0]["content"].(string)}, nil
			}
		}
		return nil, os.ErrNotExist
	}
}

func TestCleanupGatesDeleteAndReport(t *testing.T) {
	e := testenv.New(t)
	batch := e.Prepare("domainanda.com", "mati.com")
	ctx := context.Background()
	cut := &cutover.Stage{SSLWait: time.Hour, HTTPSScheme: "http"}
	opts := cleanup.Options{RequireSSL: true, Resolve: resolver(e)}
	clean := &cleanup.Stage{Opts: opts}
	finished := ""
	deps := e.Deps(cut, clean)
	deps.OnBatchFinished = func(ctx context.Context, id string) { finished = id }
	r := pipeline.NewRunner(deps)
	cut.Runner, clean.Runner = r, r
	e.RunUntilIdle(r, 30)

	early := cleanup.Evaluate(ctx, r, e.Item("domainanda.com"), opts)
	if early.Eligible || !strings.Contains(strings.Join(early.Reasons, " "), "belum siap cleanup") {
		t.Fatalf("sebelum cutover tidak boleh lolos: %+v", early.Reasons)
	}
	dummy := cleanup.Evaluate(ctx, r, e.Item("mati.com"), opts)
	if dummy.Eligible || !strings.Contains(dummy.Reasons[0], "--termasuk-dummy") {
		t.Fatalf("DUMMY_ONLY tanpa opsi tidak boleh lolos: %+v", dummy.Reasons)
	}
	if d2 := cleanup.Evaluate(ctx, r, e.Item("mati.com"), cleanup.Options{IncludeDummy: true}); !d2.Eligible {
		t.Fatalf("DUMMY_ONLY dengan opsi harus lolos: %+v", d2.Reasons)
	}

	gate.Request(ctx, e.Store, batch, gate.KindCutover, []string{"domainanda.com"}, "CUTOVER")
	e.API.Lock()
	for i, w := range e.API.WebsitesFor[testenv.SrcToken] {
		if w.Domain == "domainanda.com" {
			e.API.WebsitesFor[testenv.SrcToken][i].Domain = "sementara.hostingersite.com"
		}
	}
	e.API.Unlock()
	e.RunUntilIdle(r, 10)
	pipeline.Confirm(ctx, e.Store, "MANUAL_SOURCE_RENAME", "domainanda.com", "", "sementara.hostingersite.com")
	e.RunUntilIdle(r, 30)
	it := e.Item("domainanda.com")
	if it.State != pipeline.StateCleanupReady {
		t.Fatalf("status = %s %s", it.State, it.LastError)
	}

	notYet := cleanup.Options{RequireSSL: true, Resolve: func(ctx context.Context, host string) ([]string, error) { return []string{testenv.SourceIP}, nil }}
	if c := cleanup.Evaluate(ctx, r, it, notYet); c.Eligible || !strings.Contains(strings.Join(c.Reasons, " "), "masih mengarah ke "+testenv.SourceIP) {
		t.Fatalf("DNS belum propagasi harus tertahan: %+v", c.Reasons)
	}
	e.Store.DB().Exec(`UPDATE verifications SET result = 'WARN' WHERE check_type = 'SSL_STATUS'`)
	if c := cleanup.Evaluate(ctx, r, it, opts); c.Eligible || !strings.Contains(strings.Join(c.Reasons, " "), "SSL belum aktif") {
		t.Fatalf("SSL belum aktif harus tertahan: %+v", c.Reasons)
	}
	e.Store.DB().Exec(`UPDATE items SET ssl_excluded = 1 WHERE id = ?`, it.ID)
	it = e.Item("domainanda.com")
	if c := cleanup.Evaluate(ctx, r, it, opts); !c.Eligible {
		t.Fatalf("SSL dikecualikan harus lolos: %+v", c.Reasons)
	}
	arts, _ := e.Store.ListArtifacts(ctx, it.ID)
	for _, a := range arts {
		if a.Kind == "final-files" {
			orig, _ := os.ReadFile(a.Path)
			os.WriteFile(a.Path, []byte("rusak"), 0o600)
			if c := cleanup.Evaluate(ctx, r, it, opts); c.Eligible || !strings.Contains(strings.Join(c.Reasons, " "), "backup lokal tidak valid") {
				t.Fatalf("backup rusak harus menahan cleanup: %+v", c.Reasons)
			}
			os.WriteFile(a.Path, orig, 0o600)
		}
	}

	e.RunUntilIdle(r, 5)
	if it = e.Item("domainanda.com"); it.State != pipeline.StateCleanupReady {
		t.Fatal("tanpa persetujuan HAPUS tidak boleh dihapus")
	}
	gate.Request(ctx, e.Store, batch, gate.KindCleanup, []string{"domainanda.com"}, "HAPUS")
	e.RunUntilIdle(r, 10)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateCompleted {
		t.Fatalf("setelah cleanup = %s %s", it.State, it.LastError)
	}
	for _, w := range e.API.WebsitesOf(testenv.SrcToken) {
		if w.Domain == "sementara.hostingersite.com" {
			t.Fatal("website sumber harus terhapus")
		}
	}
	for _, d := range e.API.Databases[testenv.SrcAccount] {
		if d["name"] == "u494983133_wp" {
			t.Fatal("database sumber harus terhapus")
		}
	}
	if len(e.API.Portfolio) != 3 {
		t.Fatal("domain/registrar tidak boleh dihapus")
	}
	if finished != batch {
		t.Fatalf("hook batch selesai harus dipanggil, dapat %q", finished)
	}

	rb := &report.Builder{Store: e.Store, BackupDir: filepath.Join(e.DataDir, "backups"), Redactor: e.Redactor, Version: "uji"}
	rep, err := rb.Build(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ringkasan.Total != 2 || rep.Ringkasan.Selesai != 1 || rep.Ringkasan.DummyOnly != 1 || rep.StatusBatch != pipeline.BatchDoneNote {
		t.Fatalf("ringkasan = %+v %s", rep.Ringkasan, rep.StatusBatch)
	}
	var wp, mati report.WebsiteRow
	for _, w := range rep.Website {
		if w.Domain == "domainanda.com" {
			wp = w
		} else {
			mati = w
		}
	}
	if !strings.HasPrefix(wp.Kesimpulan, "Sumber dibersihkan, catatan: SSL belum aktif") || wp.Tujuan != "domainanda.com" || !strings.Contains(wp.DNS, "A @ → "+testenv.DestIP) {
		t.Fatalf("baris wp harus jujur soal SSL: %+v", wp)
	}
	if !strings.Contains(mati.Kesimpulan, "Hanya dummy") || len(rep.DummyOnly) != 1 || !strings.HasSuffix(rep.DummyOnly[0].Dummy, ".hostingersite.com") {
		t.Fatalf("dummy only = %+v", mati)
	}
	if len(rep.DNS) != 1 || !strings.Contains(rep.DNS[0].Sebelum, "A @ → "+testenv.SourceIP) || !strings.Contains(rep.DNS[0].Sesudah, "record lain tidak diubah") {
		t.Fatalf("dns = %+v", rep.DNS)
	}
	if len(rep.SSHKey) != 2 {
		t.Fatalf("akun dengan SSH key = %+v", rep.SSHKey)
	}
	dir := filepath.Join(e.DataDir, "reports", batch)
	files, err := rb.Write(rep, dir, map[string]bool{"html": true, "json": true, "csv": true})
	if err != nil || len(files) != 3 {
		t.Fatalf("file laporan = %v %v", files, err)
	}
	html, _ := os.ReadFile(filepath.Join(dir, "laporan.html"))
	for _, want := range []string{"Laporan Migrasi " + batch, "Akun yang masih memasang SSH key migrator", "u494983133", "u73839202", "DNS sebelum dan sesudah", "Website DUMMY_ONLY", "Hanya dummy (domain mati)"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("HTML tidak memuat %q", want)
		}
	}
	csvData, _ := os.ReadFile(filepath.Join(dir, "laporan.csv"))
	if !strings.HasPrefix(string(csvData), "domain_asal,tujuan,dummy") || strings.Count(string(csvData), "\n") != 3 {
		t.Fatalf("csv = %s", csvData)
	}
	if !strings.Contains(rep.Terminal(), "PENGINGAT: hapus SSH key migrator") {
		t.Fatal("ringkasan terminal harus mengingatkan SSH key")
	}
	e.AssertNoSecretsOnDisk(testenv.WPPassword, testenv.SrcToken, testenv.DstToken)
}
