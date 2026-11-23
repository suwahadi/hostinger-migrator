package inventory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/fakehostinger"
	"hostinger-migrator/tests/sshtest"
)

const (
	token      = "FAKE-inventory-token-1234567890ab"
	wpPassword = "FAKE-wp-db-pass-777"
	envPass    = "FAKE-laravel-pass-888"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func probeOutput(unit string) string {
	switch unit {
	case "/home/u494983133/domains/domainanda.com":
		wp := "<?php\ndefine('DB_NAME', 'u494983133_wp');\ndefine('DB_USER', 'u494983133_wp');\ndefine('DB_PASSWORD', '" + wpPassword + "');\ndefine('DB_HOST', 'localhost');\n"
		return "SIZE=1288490188\nFILES=5321\nROOT public_html/\nROOT logs/\nPUB wp-config.php\nPUB wp-content/\nPUB wp-includes/\nPUB .htaccess\nPUB blog/\nFILE public_html/wp-config.php\n" + b64(wp) + "\n"
	case "/home/u494983133/domains/toko.id":
		env := "DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_DATABASE=u494983133_toko\nDB_USERNAME=u494983133_toko\nDB_PASSWORD=" + envPass + "\n"
		return "SIZE=891289600\nFILES=12000\nROOT app/\nROOT public_html/\nROOT vendor/\nROOT artisan\nROOT composer.json\nROOT .env\nPUB index.php\nPUB .htaccess\nFILE .env\n" + b64(env) + "\nFILE composer.json\n" + b64(`{"require":{"laravel/framework":"^11"}}`) + "\n"
	}
	return "ERR=tidak_ada\n"
}

type fixture struct {
	scanner *Scanner
	st      *store.Store
	api     *fakehostinger.Server
	ssh     *sshtest.Server
	dir     string
	logBuf  *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	api := fakehostinger.New(false, token)
	t.Cleanup(api.Close)
	api.Zones["toko.id"] = nil
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		if err := st.InsertCredential(ctx, tx, "xyz"); err != nil {
			return err
		}
		return st.ReplaceHostingAccounts(ctx, tx, "xyz", []store.HostingAccount{{ID: "u494983133", Plan: "hostinger_premium"}, {ID: "u597522494", Plan: "hostinger_single"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	sshSrv, err := sshtest.New("u494983133")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sshSrv.Close() })
	sshSrv.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		if cmd == "echo ok; whoami; pwd" {
			return sshtest.ExecResult{Stdout: "ok\nu494983133\n/home/u494983133\n"}
		}
		if strings.HasPrefix(cmd, "cd '") {
			unit := strings.TrimPrefix(cmd, "cd '")
			unit = unit[:strings.Index(unit, "'")]
			return sshtest.ExecResult{Stdout: probeOutput(unit)}
		}
		return sshtest.ExecResult{Exit: 127}
	}
	r := logx.NewRedactor()
	var logBuf bytes.Buffer
	log := slog.New(logx.NewRedactingHandler(slog.NewJSONHandler(&logBuf, nil), r))
	svc := &sshx.Service{Store: st, Secrets: secrets.New(secrets.NewMemoryBackend(), r), KeyDir: filepath.Join(dir, "ssh"), Timeout: 5 * time.Second, Log: log}
	kp, _ := svc.Keys()
	sshSrv.Authorize(kp.Signer.PublicKey())
	if res, err := svc.Check(ctx, store.SSHInfo{AccountID: "u494983133", Host: sshSrv.Host(), Port: sshSrv.Port(), User: "u494983133"}, false); err != nil || !res.Ready() {
		t.Fatalf("ssh: %+v %v", res, err)
	}
	sc := &Scanner{
		Store:    st,
		SSH:      svc,
		DataDir:  dir,
		Redactor: r,
		Log:      log,
		NewClient: func(alias string) (*hostinger.Client, error) {
			return hostinger.NewClient(hostinger.Options{Token: token, BaseURL: api.URL, Limiter: hostinger.NewLimiter(60000)})
		},
		LookupNS: func(ctx context.Context, domain string) ([]string, error) {
			switch domain {
			case "domainanda.com":
				return []string{"ns1.dns-parking.com", "ns2.dns-parking.com"}, nil
			case "toko.id":
				return []string{"ana.ns.cloudflare.com", "bob.ns.cloudflare.com"}, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
		},
	}
	return &fixture{scanner: sc, st: st, api: api, ssh: sshSrv, dir: dir, logBuf: &logBuf}
}

func find(ws []store.Website, domain string) store.Website {
	for _, w := range ws {
		if w.Domain == domain {
			return w
		}
	}
	return store.Website{}
}

func TestScanAliasFull(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	snaps, err := f.scanner.ScanAlias(ctx, "xyz")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshot = %d", len(snaps))
	}
	ws, _ := f.st.ListWebsites(ctx, "u494983133")
	wp := find(ws, "domainanda.com")
	if wp.SiteType != "WordPress" || wp.UnitPath != "/home/u494983133/domains/domainanda.com" || wp.DocRoot != "/home/u494983133/domains/domainanda.com/public_html" {
		t.Fatalf("wp = %+v", wp)
	}
	if wp.SizeBytes != 1288490188 || wp.FileCount != 5321 || wp.SizeSource != store.SizeFromSSH {
		t.Fatalf("ukuran wp = %+v", wp)
	}
	if wp.DomainStatus != store.DomainActive || wp.NSExternal == nil || *wp.NSExternal || !wp.DNSInToken || wp.DNSRecordCount != 2 || wp.PHPVersion != "8.2" {
		t.Fatalf("domain wp = %+v", wp)
	}
	if len(wp.Subdomains) != 1 || wp.Subdomains[0] != "blog.domainanda.com" || wp.MailOrderID != "mo-1" {
		t.Fatalf("sub/mail wp = %+v", wp)
	}
	toko := find(ws, "toko.id")
	if toko.SiteType != "Laravel" || toko.NSExternal == nil || !*toko.NSExternal || toko.SizeBytes != 891289600 {
		t.Fatalf("toko = %+v", toko)
	}
	if !strings.Contains(strings.Join(toko.Notes, " "), "di luar public_html") || !strings.Contains(strings.Join(toko.Notes, " "), "tidak ada di daftar database API") {
		t.Fatalf("catatan toko = %v", toko.Notes)
	}
	dbs, _ := f.st.ListDatabasesByAccount(ctx, "u494983133")
	if len(dbs) != 2 {
		t.Fatalf("db = %+v", dbs)
	}
	for _, d := range dbs {
		switch d.Name {
		case "u494983133_wp":
			if d.WebsiteDomain != "domainanda.com" || !d.InAPI || d.Source != "public_html/wp-config.php" {
				t.Fatalf("db wp = %+v", d)
			}
		case "u494983133_toko":
			if d.WebsiteDomain != "toko.id" || d.InAPI || d.Host != "127.0.0.1" {
				t.Fatalf("db toko = %+v", d)
			}
		}
	}
	boxes, _ := f.st.ListMailboxes(ctx, []string{"domainanda.com"})
	if len(boxes) != 2 || boxes[1].MessageCount != 120 {
		t.Fatalf("mailbox = %+v", boxes)
	}
	cron, _ := f.st.ListCron(ctx, "u494983133")
	if len(cron) != 1 || !strings.Contains(cron[0].Command, "artisan schedule:run") {
		t.Fatalf("cron = %+v", cron)
	}

	other, _ := f.st.ListWebsites(ctx, "u597522494")
	contoh := find(other, "contoh.com")
	if contoh.SiteType != "Statis" || contoh.SizeSource != store.SizeUnknown || contoh.SizeBytes != -1 || !contoh.DummyOnly() {
		t.Fatalf("contoh = %+v", contoh)
	}
	if !strings.Contains(strings.Join(contoh.Notes, " "), "listing API") {
		t.Fatalf("catatan contoh = %v", contoh.Notes)
	}
	run, err := f.st.GetInventoryRun(ctx, "u597522494", "xyz")
	if err != nil || run.SSHUsed || len(run.Warnings) == 0 || !strings.Contains(run.Warnings[0], "SSH") {
		t.Fatalf("run = %+v %v", run, err)
	}
}

func TestInventoryNeverWrites(t *testing.T) {
	f := newFixture(t)
	if _, err := f.scanner.ScanAlias(context.Background(), "xyz"); err != nil {
		t.Fatal(err)
	}
	if f.api.Writes() != 0 {
		t.Fatalf("inventaris mengirim %d request tulis", f.api.Writes())
	}
	for _, cmd := range f.ssh.Commands() {
		for _, banned := range []string{"rm ", "mv ", " > ", ">>", "chmod", "mysql ", "wp "} {
			if strings.Contains(cmd, banned) {
				t.Fatalf("perintah SSH tidak read-only (%q): %s", banned, cmd)
			}
		}
	}
}

func TestInventorySecretsNeverPersisted(t *testing.T) {
	f := newFixture(t)
	if _, err := f.scanner.ScanAlias(context.Background(), "xyz"); err != nil {
		t.Fatal(err)
	}
	filepath.Walk(f.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		for _, secret := range []string{wpPassword, envPass, token} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("rahasia bocor ke %s", p)
			}
		}
		return nil
	})
	if strings.Contains(f.logBuf.String(), wpPassword) || strings.Contains(f.logBuf.String(), envPass) {
		t.Fatal("rahasia bocor ke log")
	}
	if !strings.Contains(f.scanner.redactor().String("x "+wpPassword), logx.Mask) {
		t.Fatal("password DB harus terdaftar di redactor")
	}
	data, err := os.ReadFile(filepath.Join(f.dir, "inventory", "xyz-u494983133.json"))
	if err != nil || !strings.Contains(string(data), `"folder_backup": "/home/u494983133/domains/toko.id"`) {
		t.Fatalf("json inventaris: %v\n%s", err, data)
	}
}

func TestReadOnlyClientRefusesWrites(t *testing.T) {
	f := newFixture(t)
	c, _ := f.scanner.NewClient("xyz")
	ro := c.ReadOnlyCopy()
	_, err := ro.Do(context.Background(), hostinger.Request{Endpoint: "hosting.free_subdomains.generate"}, nil)
	if !errors.Is(err, hostinger.ErrReadOnly) {
		t.Fatalf("err = %v", err)
	}
	if f.api.Writes() != 0 {
		t.Fatal("request tulis tidak boleh sampai ke server")
	}
}

func TestRescanMarksRemovedWebsite(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.scanner.ScanAlias(ctx, "xyz")
	f.api.Websites = f.api.Websites[:1]
	if _, err := f.scanner.ScanAlias(ctx, "xyz"); err != nil {
		t.Fatal(err)
	}
	ws, _ := f.st.ListWebsites(ctx, "u494983133")
	if len(ws) != 1 || ws[0].Domain != "domainanda.com" {
		t.Fatalf("website = %+v", ws)
	}
}

func TestRevokedTokenStopsInventory(t *testing.T) {
	f := newFixture(t)
	f.api.Revoke(token)
	if _, err := f.scanner.ScanAlias(context.Background(), "xyz"); !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseProbeAndCommand(t *testing.T) {
	r, err := ParseProbe("SIZE=10\nFILES=2\nROOT a/\nPUB b.php\nFILE x\n" + b64("isi") + "\n")
	if err != nil || r.size != 10 || r.files != 2 || r.root[0] != "a/" || r.public[0] != "b.php" || r.content["x"] != "isi" {
		t.Fatalf("r = %+v %v", r, err)
	}
	if _, err := ParseProbe("FILE x\n!!!\n"); err == nil {
		t.Fatal("base64 rusak harus error")
	}
	if r, _ := ParseProbe("ERR=tidak_ada\n"); !r.missing {
		t.Fatal("folder hilang harus terdeteksi")
	}
	cmd := ProbeCommand("/home/u1/domains/a'b.com")
	if !strings.HasPrefix(cmd, `cd '/home/u1/domains/a'"'"'b.com'`) {
		t.Fatalf("quote salah: %s", cmd)
	}
}

func TestUnitPathAndRegistrable(t *testing.T) {
	if unitPath("/home/u1/domains/a.com/public_html") != "/home/u1/domains/a.com" || unitPath("/home/u1/domains/a.com/public_html/blog") != "/home/u1/domains/a.com/public_html/blog" {
		t.Fatal("unit path salah")
	}
	cases := map[string]string{"a.com": "a.com", "blog.a.com": "a.com", "toko.co.id": "toko.co.id", "x.toko.co.id": "toko.co.id", "sekolah.sch.id": "sekolah.sch.id", "toko.id": "toko.id"}
	for in, want := range cases {
		if got := registrable(in); got != want {
			t.Errorf("%s = %s, harus %s", in, got, want)
		}
	}
}

func TestMarkIncluded(t *testing.T) {
	ws := []store.Website{
		{Domain: "a.com", UnitPath: "/home/u/domains/a.com"},
		{Domain: "blog.a.com", UnitPath: "/home/u/domains/a.com/public_html/blog"},
		{Domain: "b.com", UnitPath: "/home/u/domains/b.com"},
	}
	markIncluded(ws)
	if ws[1].IncludedIn != "a.com" || ws[0].IncludedIn != "" || ws[2].IncludedIn != "" || ws[1].IsUnit() {
		t.Fatalf("ws = %+v", ws)
	}
}

func TestScanReportsProgressInParallel(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var events []Progress
	f.scanner.Parallel = 3
	f.scanner.Progress = func(p Progress) {
		mu.Lock()
		events = append(events, p)
		mu.Unlock()
	}
	if _, err := f.scanner.ScanAlias(context.Background(), "xyz"); err != nil {
		t.Fatal(err)
	}
	accounts, sites := 0, map[string]bool{}
	last := map[string]int{}
	for _, e := range events {
		switch e.Stage {
		case StageAccount:
			accounts++
		case StageWebsite:
			if e.Done != last[e.AccountID]+1 || e.Done > e.Total {
				t.Fatalf("urutan progres salah: %+v", e)
			}
			last[e.AccountID] = e.Done
			sites[e.Domain] = true
		}
	}
	if accounts != 2 || !sites["domainanda.com"] || !sites["toko.id"] {
		t.Fatalf("progres = %+v", events)
	}
}

func TestScanQuickSkipsSize(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var cmds []string
	inner := f.ssh.Exec
	f.ssh.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		res := inner(cmd, env)
		if strings.HasPrefix(cmd, "cd '") {
			var keep []string
			for _, line := range strings.Split(res.Stdout, "\n") {
				if !strings.HasPrefix(line, "SIZE=") && !strings.HasPrefix(line, "FILES=") {
					keep = append(keep, line)
				}
			}
			res.Stdout = strings.Join(keep, "\n")
		}
		return res
	}
	f.scanner.SkipSize = true
	ctx := context.Background()
	if _, err := f.scanner.ScanAlias(ctx, "xyz"); err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if strings.HasPrefix(c, "cd '") && (strings.Contains(c, "SIZE=") || strings.Contains(c, "du -sb")) {
			t.Fatalf("scan cepat tidak boleh menghitung ukuran: %s", c)
		}
	}
	ws, _ := f.st.ListWebsites(ctx, "u494983133")
	wp := find(ws, "domainanda.com")
	if wp.SiteType != "WordPress" || wp.SizeBytes != store.UnknownQuantity || !strings.Contains(strings.Join(wp.Notes, " "), "scan cepat") {
		t.Fatalf("wp = %+v", wp)
	}
}

func TestProbeCommandRealShell(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash tidak tersedia")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "public_html", "wp-content"), 0o755)
	os.WriteFile(filepath.Join(dir, "public_html", "index.php"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(dir, "public_html", "wp-content", "a.txt"), []byte("1234567890"), 0o644)
	os.WriteFile(filepath.Join(dir, "public_html", "wp-config.php"), []byte("<?php define('DB_NAME', 'x');"), 0o644)
	out, err := exec.Command("bash", "-c", ProbeCommand(dir)).Output()
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseProbe(string(out))
	if err != nil || r.size != int64(5+10+len("<?php define('DB_NAME', 'x');")) || r.files != 3 || r.content["public_html/wp-config.php"] == "" {
		t.Fatalf("r = %+v %v\n%s", r, err, out)
	}
	out, _ = exec.Command("bash", "-c", QuickProbeCommand(dir)).Output()
	q, _ := ParseProbe(string(out))
	if q.size != -1 || q.files != -1 || len(q.public) == 0 {
		t.Fatalf("q = %+v", q)
	}
	out, _ = exec.Command("bash", "-c", ProbeCommand(filepath.Join(dir, "tidak-ada"))).Output()
	if m, _ := ParseProbe(string(out)); !m.missing {
		t.Fatal("folder hilang harus terdeteksi")
	}
}
