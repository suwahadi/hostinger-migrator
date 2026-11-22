package pipeline_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/testenv"
)

func tree(t *testing.T, root string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for _, s := range skip {
			if strings.HasSuffix(rel, s) {
				return nil
			}
		}
		data, _ := os.ReadFile(p)
		out[rel] = string(data)
		return nil
	})
	return out
}

func keys(m map[string]string) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func TestFullMigrationToVerified(t *testing.T) {
	e := testenv.New(t)
	batch := e.Prepare("domainanda.com", "toko.id", "mati.com")
	r := pipeline.NewRunner(e.Deps())
	e.RunUntilIdle(r, 20)

	wp := e.Item("domainanda.com")
	if wp.State != pipeline.StateVerified || wp.SideState != "" {
		t.Fatalf("wp = %s/%s: %s", wp.State, wp.SideState, wp.LastError)
	}
	lv := e.Item("toko.id")
	if lv.State != pipeline.StateVerified {
		t.Fatalf("laravel = %s/%s: %s", lv.State, lv.SideState, lv.LastError)
	}
	dead := e.Item("mati.com")
	if dead.State != pipeline.StateDummyOnly || dead.FinishedAt == "" {
		t.Fatalf("expired = %s/%s: %s", dead.State, dead.SideState, dead.LastError)
	}
	if !strings.HasSuffix(wp.DummyDomain, ".hostingersite.com") || wp.DestIP != testenv.DestIP || !strings.HasPrefix(wp.DestRootPath, e.DstHome()) {
		t.Fatalf("dummy = %+v", wp)
	}

	src := tree(t, e.SrcUnit("domainanda.com"), "wp-config.php", ".wp-siteurl")
	dst := tree(t, wp.DestRootPath, "wp-config.php", ".wp-siteurl", ".migrator-orig")
	if strings.Join(keys(src), ",") != strings.Join(keys(dst), ",") {
		t.Fatalf("struktur file berbeda:\nsumber %v\ntujuan %v", keys(src), keys(dst))
	}
	for k, v := range src {
		if dst[k] != v {
			t.Fatalf("isi %s berbeda", k)
		}
	}
	for _, hidden := range []string{"public_html/.htaccess", "public_html/.well-known/acme-challenge/token"} {
		if _, ok := dst[hidden]; !ok {
			t.Fatalf("file tersembunyi %s tidak ikut", hidden)
		}
	}
	if _, err := os.Stat(filepath.Join(wp.DestRootPath, "public_html", "default.php")); !os.IsNotExist(err) {
		t.Fatal("isi bawaan dummy harus dipindah ke .migrator")
	}
	stash, _ := filepath.Glob(filepath.Join(e.DstHome(), ".migrator", "*", "isi-bawaan-*", "public_html", "default.php"))
	if len(stash) == 0 {
		t.Fatal("isi bawaan dummy harus disimpan, bukan dihapus")
	}

	cfg, _ := os.ReadFile(filepath.Join(wp.DestRootPath, "public_html", "wp-config.php"))
	orig, _ := os.ReadFile(filepath.Join(wp.DestRootPath, "public_html", "wp-config.php.migrator-orig"))
	srcCfg, _ := os.ReadFile(filepath.Join(e.SrcUnit("domainanda.com"), "public_html", "wp-config.php"))
	if !bytes.Equal(orig, srcCfg) {
		t.Fatal("salinan .migrator-orig harus sama dengan konfigurasi asli")
	}
	if len(wp.DestDBs) != 1 || wp.DestDBs[0].Name != "u73839202_wp" || !wp.DestDBs[0].Imported {
		t.Fatalf("db tujuan = %+v", wp.DestDBs)
	}
	pw, err := e.Secrets.Get(secrets.KindDBPassword, testenv.DstAccount+":u73839202_wp")
	if err != nil || !strings.Contains(string(cfg), "'u73839202_wp'") || !strings.Contains(string(cfg), pw) || strings.Contains(string(cfg), testenv.WPPassword) {
		t.Fatalf("wp-config tujuan tidak diperbarui:\n%s", cfg)
	}
	srcDump, _ := os.ReadFile(filepath.Join(e.FakeDB, "u494983133_wp.sql"))
	dstDump, _ := os.ReadFile(filepath.Join(e.FakeDB, "u73839202_wp.sql"))
	if !bytes.Contains(dstDump, []byte("),(palsu')")) || !bytes.Contains(dstDump, srcDump[:60]) {
		t.Fatalf("isi database tujuan tidak sesuai:\n%s", dstDump)
	}
	env, _ := os.ReadFile(filepath.Join(lv.DestRootPath, ".env"))
	if !strings.Contains(string(env), "DB_DATABASE=u73839202_toko") || !strings.Contains(string(env), "APP_KEY=base64:FAKEappkeyLaravel") {
		t.Fatalf(".env tujuan:\n%s", env)
	}
	if _, err := os.Stat(filepath.Join(lv.DestRootPath, "bootstrap", "cache", "config.php")); !os.IsNotExist(err) {
		t.Fatal("cache konfigurasi Laravel harus dibersihkan")
	}
	if e.API.PHP[wp.DummyDomain] != "8.2" || e.API.PHP[lv.DummyDomain] != "8.1" {
		t.Fatalf("PHP tujuan = %v", e.API.PHP)
	}

	ctx := context.Background()
	vs, _ := e.Store.ListVerifications(ctx, wp.ID)
	results := map[string]string{}
	for _, v := range vs {
		results[v.CheckType] = v.Result + " " + v.Detail
	}
	for _, want := range []string{"VERIFIKASI_HTTP", "VERIFIKASI_TANDA_ERROR", "VERIFIKASI_JUDUL", "VERIFIKASI_ASSET", "VERIFIKASI_FILE", "VERIFIKASI_DB_u73839202_wp", "VERIFIKASI_HASIL"} {
		if !strings.HasPrefix(results[want], "PASS") {
			t.Errorf("%s = %q", want, results[want])
		}
	}
	if !strings.Contains(results["VERIFIKASI_METODE"], "search-replace") || !wp.SearchReplaceDone {
		t.Errorf("WordPress harus memakai fallback search-replace: %q", results["VERIFIKASI_METODE"])
	}
	lvVs, _ := e.Store.ListVerifications(ctx, lv.ID)
	for _, v := range lvVs {
		if v.CheckType == "VERIFIKASI_METODE" && !strings.Contains(v.Detail, "fallback GET langsung") {
			t.Errorf("metode Laravel = %s", v.Detail)
		}
	}

	arts, _ := e.Store.ListArtifacts(ctx, wp.ID)
	kinds := map[string]bool{}
	for _, a := range arts {
		kinds[a.Kind] = a.Validated
	}
	if !kinds["files"] || !kinds["db:u494983133_wp"] || !kinds["manifest"] {
		t.Fatalf("artefak = %+v", arts)
	}
	if leftover, _ := filepath.Glob(filepath.Join(e.DstHome(), ".migrator", "*", "*.tar.gz")); len(leftover) != 0 {
		t.Fatalf("file sementara di server tujuan harus dihapus: %v", leftover)
	}
	b, _, _ := e.Store.GetBatch(ctx, batch)
	if b.Status != pipeline.BatchRunning {
		t.Fatalf("status batch = %s", b.Status)
	}
	e.AssertNoSecretsOnDisk(testenv.WPPassword, testenv.LVPassword, testenv.SrcToken, testenv.DstToken, pw)
}

func TestResumeAfterKillDuringBackupAndRestore(t *testing.T) {
	e := testenv.New(t)
	e.Prepare("domainanda.com")
	ctx, cancel := context.WithCancel(context.Background())
	srcExec := e.SrcSSH.Exec
	killedBackup := false
	e.SrcSSH.Exec = func(cmd string, env map[string]string) sshtestResult {
		if strings.HasPrefix(cmd, "tar -czf") && !killedBackup {
			killedBackup = true
			cancel()
			return sshtestResult{Stdout: "sebagian", Exit: 0}
		}
		return srcExec(cmd, env)
	}
	r := pipeline.NewRunner(e.Deps())
	r.RunOnce(ctx)
	it := e.Item("domainanda.com")
	if it.State != pipeline.StateBackupRunning {
		t.Fatalf("harus berhenti di BACKUP_RUNNING, dapat %s", it.State)
	}
	e.Store.ResetRunningSteps(context.Background())
	if steps, _ := e.Store.ListSteps(context.Background(), it.ID); len(steps) == 0 || steps[0].Status != store.StepFailed || !strings.Contains(steps[0].Error, "akan diulang") {
		t.Fatalf("langkah besar harus ditandai untuk diulang: %+v", steps)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	dstExec := e.DstSSH.Exec
	killedRestore := false
	e.DstSSH.Exec = func(cmd string, env map[string]string) sshtestResult {
		if strings.Contains(cmd, "tar -xzf") && !killedRestore {
			killedRestore = true
			cancel2()
			return sshtestResult{Stderr: "terputus", Exit: 1}
		}
		return dstExec(cmd, env)
	}
	r2 := pipeline.NewRunner(e.Deps())
	r2.RunOnce(ctx2)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateRestoreRunning {
		t.Fatalf("harus berhenti di RESTORE_RUNNING, dapat %s (%s)", it.State, it.LastError)
	}
	e.Store.ResetRunningSteps(context.Background())
	r3 := pipeline.NewRunner(e.Deps())
	e.RunUntilIdle(r3, 20)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateVerified {
		t.Fatalf("setelah resume = %s/%s: %s", it.State, it.SideState, it.LastError)
	}
	dummies := 0
	for _, w := range e.API.WebsitesOf(testenv.DstToken) {
		if strings.HasSuffix(w.Domain, ".hostingersite.com") {
			dummies++
		}
	}
	if dummies != 1 {
		t.Fatalf("website dummy dibuat %d kali, harus 1", dummies)
	}
	if n := e.Count(`SELECT COUNT(1) FROM artifacts WHERE item_id = ? AND kind = 'files'`, it.ID); n != 1 {
		t.Fatalf("artefak file = %d", n)
	}
	steps, _ := e.Store.ListSteps(context.Background(), it.ID)
	for _, s := range steps {
		if s.Name == "backup" && s.Attempts < 2 {
			t.Fatalf("backup harus diulang setelah dimatikan: %+v", s)
		}
	}
}

func TestManualCreateSiteCheckpoint(t *testing.T) {
	e := testenv.New(t)
	e.Prepare("domainanda.com", "mati.com")
	e.API.SetFail("onboarding", 422)
	r := pipeline.NewRunner(e.Deps())
	e.RunUntilIdle(r, 20)
	it := e.Item("domainanda.com")
	if it.SideState != store.SideWaitingManual || it.ManualCode != "MANUAL_CREATE_SITE" {
		t.Fatalf("item = %s/%s %s", it.State, it.SideState, it.LastError)
	}
	other := e.Item("mati.com")
	if other.SideState != store.SideWaitingManual {
		t.Fatalf("item lain juga harus manual karena API menolak: %s/%s", other.State, other.SideState)
	}
	tasks, _ := e.Store.ListManualTasks(context.Background(), true)
	if len(tasks) != 2 || !strings.Contains(tasks[0].Instructions, "migrator confirm MANUAL_CREATE_SITE") {
		t.Fatalf("tugas manual = %+v", tasks)
	}
	if _, err := pipeline.Confirm(context.Background(), e.Store, "MANUAL_CREATE_SITE", "domainanda.com", "", "manual-1.hostingersite.com"); err != nil {
		t.Fatal(err)
	}
	e.RunUntilIdle(r, 5)
	it = e.Item("domainanda.com")
	if it.ManualCode != "MANUAL_CREATE_SITE" || !strings.Contains(it.LastError, "MANUAL_CREATE_SITE") {
		t.Fatalf("website manual belum ada, harus kembali menunggu: %+v", it)
	}
	e.API.Lock()
	root := e.API.SiteRoot(testenv.DstAccount, "manual-1.hostingersite.com")
	e.API.WebsitesFor[testenv.DstToken] = append(e.API.WebsitesFor[testenv.DstToken], websiteFor("manual-1.hostingersite.com", root))
	e.API.Unlock()
	if _, err := pipeline.Confirm(context.Background(), e.Store, "MANUAL_CREATE_SITE", "domainanda.com", "", "manual-1.hostingersite.com"); err != nil {
		t.Fatal(err)
	}
	e.RunUntilIdle(r, 20)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateVerified || it.DummyDomain != "manual-1.hostingersite.com" {
		t.Fatalf("setelah konfirmasi = %s/%s %s %s", it.State, it.SideState, it.DummyDomain, it.LastError)
	}
	if _, err := pipeline.Confirm(context.Background(), e.Store, "MANUAL_CREATE_SITE", "domainanda.com", "", "x"); err == nil {
		t.Fatal("konfirmasi ganda harus ditolak")
	}
}

func TestManualBackupWithoutSSH(t *testing.T) {
	e := testenv.New(t)
	e.Prepare("domainanda.com")
	ctx := context.Background()
	info, _ := e.Store.GetSSH(ctx, testenv.SrcAccount)
	info.Status = store.SSHFailed
	e.Store.SaveSSH(ctx, *info)
	r := pipeline.NewRunner(e.Deps())
	e.RunUntilIdle(r, 10)
	it := e.Item("domainanda.com")
	if it.ManualCode != "MANUAL_BACKUP" {
		t.Fatalf("item = %+v", it)
	}
	task, _ := e.Store.GetManualTask(ctx, it.ID, "MANUAL_BACKUP")
	dir := filepath.Join(e.DataDir, "backups", it.BatchID, "domainanda.com")
	if !strings.Contains(task.Instructions, filepath.Join(dir, "files.tar.gz")) || !strings.Contains(task.Instructions, "db-u494983133_wp.sql.gz") {
		t.Fatalf("instruksi = %s", task.Instructions)
	}
	if _, err := pipeline.Confirm(ctx, e.Store, "MANUAL_BACKUP", "domainanda.com", "", ""); err != nil {
		t.Fatal(err)
	}
	e.RunUntilIdle(r, 5)
	it = e.Item("domainanda.com")
	if it.ManualCode != "MANUAL_BACKUP" || !strings.Contains(it.LastError, "MANUAL_BACKUP") {
		t.Fatalf("file belum ada, harus kembali menunggu: %+v", it)
	}
	os.MkdirAll(dir, 0o700)
	if err := makeTarGz(e.SrcUnit("domainanda.com"), filepath.Join(dir, "files.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if err := gzipFile(filepath.Join(e.FakeDB, "u494983133_wp.sql"), filepath.Join(dir, "db-u494983133_wp.sql.gz")); err != nil {
		t.Fatal(err)
	}
	pipeline.Confirm(ctx, e.Store, "MANUAL_BACKUP", "domainanda.com", "", "")
	e.RunUntilIdle(r, 20)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateVerified {
		t.Fatalf("backup manual = %s/%s %s", it.State, it.SideState, it.LastError)
	}
}

func TestImportRetryAndRevokedToken(t *testing.T) {
	e := testenv.New(t)
	e.Prepare("domainanda.com", "toko.id")
	os.WriteFile(filepath.Join(e.FakeDB, "failimport"), nil, 0o600)
	r := pipeline.NewRunner(e.Deps())
	e.RunUntilIdle(r, 20)
	for _, d := range []string{"domainanda.com", "toko.id"} {
		if it := e.Item(d); it.State == pipeline.StateRestoreRunning && it.LastError != "" {
			e.ExpireBackoff(d)
		}
	}
	e.RunUntilIdle(r, 20)
	for _, d := range []string{"domainanda.com", "toko.id"} {
		if it := e.Item(d); it.State != pipeline.StateVerified {
			t.Fatalf("%s setelah import diulang = %s/%s %s", d, it.State, it.SideState, it.LastError)
		}
	}

	e2 := testenv.New(t)
	e2.Prepare("domainanda.com")
	e2.API.Revoke(testenv.DstToken)
	r2 := pipeline.NewRunner(e2.Deps())
	e2.RunUntilIdle(r2, 20)
	it := e2.Item("domainanda.com")
	if it.SideState != store.SideBlocked || !strings.Contains(it.LastError, "token ditolak") {
		t.Fatalf("token dicabut harus BLOCKED: %s/%s %s", it.State, it.SideState, it.LastError)
	}
	if it.State != pipeline.StateDestProvisioning {
		t.Fatalf("backup tetap selesai, berhenti di provisioning: %s", it.State)
	}
}
