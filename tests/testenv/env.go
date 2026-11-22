package testenv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/capability"
	"hostinger-migrator/internal/config"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/inventory"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/planner"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/internal/verify"
	"hostinger-migrator/tests/fakehostinger"
	"hostinger-migrator/tests/imaptest"
	"hostinger-migrator/tests/sshtest"
)

const (
	SrcAlias   = "xyz"
	DstAlias   = "zyx"
	SrcAccount = "u494983133"
	DstAccount = "u73839202"
	SrcToken   = "FAKE-env-sumber-token-1111222233334444"
	DstToken   = "FAKE-env-tujuan-token-5555666677778888"
	WPPassword = "FAKE-wp-db-Sandi-001"
	LVPassword = "FAKE-lv-db-Sandi-002"
	DestIP     = "10.0.0.2"
	SourceIP   = "10.0.0.1"
)

type Env struct {
	T        testing.TB
	Root     string
	DataDir  string
	FakeDB   string
	Store    *store.Store
	Backend  *secrets.MemoryBackend
	Secrets  *secrets.Store
	Redactor *logx.Redactor
	API      *fakehostinger.Server
	SrcSSH   *sshtest.Server
	DstSSH   *sshtest.Server
	SSH      *sshx.Service
	SrcHTTP  *httptest.Server
	DstHTTP  *httptest.Server
	Log      *slog.Logger
	LogBuf   *SafeBuffer
	Config   *config.Config
	Limiters *hostinger.LimiterPool
	IMAP     *imaptest.Server
	mu       sync.Mutex
	clients  map[string]*hostinger.Client
}

type SafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *SafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *SafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (e *Env) SrcHome() string { return filepath.Join(e.Root, "home", SrcAccount) }
func (e *Env) DstHome() string { return filepath.Join(e.Root, "home", DstAccount) }
func (e *Env) SrcUnit(domain string) string {
	return filepath.Join(e.SrcHome(), "domains", domain)
}

func write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const wpIndex = `<!DOCTYPE html><html><head><title>Toko Domain Anda</title>
<link rel="stylesheet" href="/wp-content/themes/toko/style.css">
<script src="/wp-includes/js/app.js"></script></head>
<body><img src="/wp-content/uploads/logo.png"><img src="https://cdn.pihak-ketiga.com/x.png"></body></html>`

const wpDump = "CREATE TABLE `wp_options` (\n  `option_id` int NOT NULL,\n  `option_value` longtext\n);\n" +
	"INSERT INTO `wp_options` VALUES (1,'https://domainanda.com'),(2,'Toko; Domain (Anda)'),(3,'teks dengan \\'kutip\\' dan \\\\ garis');\n" +
	"CREATE TABLE `wp_posts` (\n  `ID` int NOT NULL,\n  `post_title` text\n);\n" +
	"INSERT INTO `wp_posts` VALUES (1,'Halo, dunia!'),(2,'Produk (baru), diskon'),(3,'),(palsu'),(4,'Akhir');\n" +
	"CREATE TABLE `wp_kosong` (\n  `id` int\n);\n"

const laravelDump = "CREATE TABLE `users` (\n  `id` int\n);\nINSERT INTO `users` VALUES (1),(2);\n"

func (e *Env) buildSources() {
	t := e.T
	wp := e.SrcUnit("domainanda.com")
	write(t, filepath.Join(wp, "public_html", "index.php"), wpIndex)
	write(t, filepath.Join(wp, "public_html", ".wp-siteurl"), "https://domainanda.com")
	write(t, filepath.Join(wp, "public_html", "wp-config.php"), "<?php\ndefine( 'DB_NAME', 'u494983133_wp' );\ndefine( 'DB_USER', 'u494983133_wp' );\ndefine( 'DB_PASSWORD', '"+WPPassword+"' );\ndefine( 'DB_HOST', 'localhost' );\n$table_prefix = 'wp_';\n")
	write(t, filepath.Join(wp, "public_html", "wp-content", "themes", "toko", "style.css"), "body{}")
	write(t, filepath.Join(wp, "public_html", "wp-content", "uploads", "logo.png"), "PNG")
	write(t, filepath.Join(wp, "public_html", "wp-includes", "js", "app.js"), "console.log(1)")
	write(t, filepath.Join(wp, "public_html", ".htaccess"), "RewriteEngine On")
	write(t, filepath.Join(wp, "public_html", ".well-known", "acme-challenge", "token"), "x")
	write(t, filepath.Join(wp, "public_html", "blog", "index.html"), "<title>Blog</title>")
	write(t, filepath.Join(wp, "logs", "access.log"), "log")

	lv := e.SrcUnit("toko.id")
	write(t, filepath.Join(lv, "artisan"), "#!/usr/bin/env php")
	write(t, filepath.Join(lv, "composer.json"), `{"require":{"laravel/framework":"^11"}}`)
	write(t, filepath.Join(lv, ".env"), "APP_KEY=base64:FAKEappkeyLaravel\nDB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_DATABASE=u494983133_toko\nDB_USERNAME=u494983133_toko\nDB_PASSWORD="+LVPassword+"\n")
	write(t, filepath.Join(lv, "app", "Http", "Kernel.php"), "<?php")
	write(t, filepath.Join(lv, "bootstrap", "cache", "config.php"), "<?php return [];")
	write(t, filepath.Join(lv, "public_html", "index.php"), `<html><head><title>Toko ID</title><link href="/css/app.css" rel="stylesheet"><script src="/js/app.js"></script></head><body><img src="/img/a.png"></body></html>`)
	write(t, filepath.Join(lv, "public_html", "css", "app.css"), "x")
	write(t, filepath.Join(lv, "public_html", "js", "app.js"), "x")
	write(t, filepath.Join(lv, "public_html", "img", "a.png"), "x")

	st := e.SrcUnit("mati.com")
	write(t, filepath.Join(st, "public_html", "index.html"), `<html><head><title>Mati</title></head><body>arsip</body></html>`)

	write(t, filepath.Join(e.FakeDB, "u494983133_wp.sql"), wpDump)
	write(t, filepath.Join(e.FakeDB, "u494983133_wp.pwd"), WPPassword)
	write(t, filepath.Join(e.FakeDB, "u494983133_toko.sql"), laravelDump)
	write(t, filepath.Join(e.FakeDB, "u494983133_toko.pwd"), LVPassword)
}

func New(t testing.TB) *Env {
	t.Helper()
	root, err := os.MkdirTemp("", "migrator-env-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	e := &Env{T: t, Root: root, DataDir: filepath.Join(root, "data"), FakeDB: filepath.Join(root, "fakedb"), clients: map[string]*hostinger.Client{}}
	e.buildSources()
	os.MkdirAll(e.DataDir, 0o700)
	bin := filepath.Join(root, "bin")
	if err := writeFakeBin(bin); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(e.DstHome(), "domains"), 0o755)
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "FAKEDB=" + e.FakeDB}

	ctx := context.Background()
	e.Redactor = logx.NewRedactor()
	e.LogBuf = &SafeBuffer{}
	e.Log = slog.New(logx.NewRedactingHandler(slog.NewJSONHandler(e.LogBuf, &slog.HandlerOptions{Level: slog.LevelDebug}), e.Redactor))
	e.Store, err = store.Open(ctx, filepath.Join(e.DataDir, "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Store.Close() })
	e.Backend = secrets.NewMemoryBackend()
	e.Secrets = secrets.New(e.Backend, e.Redactor)
	e.Config, _ = config.Default()
	e.Limiters = hostinger.NewLimiterPool(60000)

	e.API = fakehostinger.New(false, SrcToken, DstToken)
	t.Cleanup(e.API.Close)
	e.setupAPI()

	e.SrcSSH, err = sshtest.New(SrcAccount)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.SrcSSH.Close() })
	e.SrcSSH.Exec = sshtest.ShellExec(SrcAccount, e.SrcHome(), env)
	e.DstSSH, err = sshtest.New(DstAccount)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DstSSH.Close() })
	e.DstSSH.Exec = sshtest.ShellExec(DstAccount, e.DstHome(), env)

	e.SrcHTTP = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.serveSite(w, r, SrcToken) }))
	t.Cleanup(e.SrcHTTP.Close)
	e.DstHTTP = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.serveSite(w, r, DstToken) }))
	t.Cleanup(e.DstHTTP.Close)

	e.SSH = &sshx.Service{Store: e.Store, Secrets: e.Secrets, KeyDir: filepath.Join(e.DataDir, "ssh"), Timeout: 10 * time.Second, Log: e.Log}
	return e
}

func (e *Env) setupAPI() {
	a := e.API
	a.Orders = []hostinger.Order{{ID: 101, Status: "active", Plan: hostinger.OrderPlan{Name: "hostinger_premium"}}}
	a.OrdersFor[SrcToken] = a.Orders
	a.OrdersFor[DstToken] = []hostinger.Order{{ID: 201, Status: "active", Plan: hostinger.OrderPlan{Name: "hostinger_business"}}}
	a.OrderUsers[201] = DstAccount
	a.WebsitesFor[SrcToken] = []hostinger.Website{
		{Domain: "domainanda.com", Username: SrcAccount, OrderID: 101, VhostType: "main", IsEnabled: true, RootDirectory: filepath.Join(e.SrcUnit("domainanda.com"), "public_html"), WebsiteType: "wordpress"},
		{Domain: "toko.id", Username: SrcAccount, OrderID: 101, VhostType: "addon", IsEnabled: true, RootDirectory: filepath.Join(e.SrcUnit("toko.id"), "public_html"), WebsiteType: "other"},
		{Domain: "mati.com", Username: SrcAccount, OrderID: 101, VhostType: "addon", IsEnabled: true, RootDirectory: filepath.Join(e.SrcUnit("mati.com"), "public_html"), WebsiteType: "other"},
	}
	a.WebsitesFor[DstToken] = []hostinger.Website{{Domain: "lama1.com", Username: DstAccount, OrderID: 201, VhostType: "main", IsEnabled: true, RootDirectory: filepath.Join(e.DstHome(), "domains", "lama1.com", "public_html")}}
	a.Databases = map[string][]map[string]any{SrcAccount: {
		{"name": "u494983133_wp", "user": "u494983133_wp", "domain": "domainanda.com", "disk_usage_mb": 1},
		{"name": "u494983133_toko", "user": "u494983133_toko", "domain": "toko.id", "disk_usage_mb": 1},
	}}
	a.Portfolio = []hostinger.PortfolioDomain{
		{ID: 1, Domain: "domainanda.com", Status: "active"},
		{ID: 2, Domain: "toko.id", Status: "active"},
		{ID: 3, Domain: "mati.com", Status: "expired"},
	}
	a.PHP = map[string]string{"domainanda.com": "8.2", "toko.id": "8.1", "mati.com": "7.4"}
	a.Cron = map[string][]map[string]any{}
	a.MailOrders = nil
	a.Zones = map[string][]map[string]any{
		"domainanda.com": {
			{"name": "@", "type": "A", "ttl": 3600, "records": []map[string]any{{"content": SourceIP}}},
			{"name": "www", "type": "CNAME", "ttl": 3600, "records": []map[string]any{{"content": "domainanda.com."}}},
			{"name": "@", "type": "MX", "ttl": 3600, "records": []map[string]any{{"content": "5 mx1.hostinger.com."}, {"content": "10 mx2.hostinger.com."}}},
			{"name": "@", "type": "TXT", "ttl": 3600, "records": []map[string]any{{"content": "\"google-site-verification=abc\""}, {"content": "\"v=spf1 include:_spf.mail.hostinger.com ~all\""}}},
			{"name": "shop", "type": "CNAME", "ttl": 3600, "records": []map[string]any{{"content": "shops.myshopify.com."}}},
		},
		"toko.id": {{"name": "@", "type": "A", "ttl": 3600, "records": []map[string]any{{"content": SourceIP}}}},
	}
	a.SiteRoot = func(user, domain string) string {
		doc := filepath.Join(e.Root, "home", user, "domains", domain, "public_html")
		os.MkdirAll(doc, 0o755)
		os.WriteFile(filepath.Join(doc, "default.php"), []byte("<?php echo 'Default page';"), 0o644)
		return doc
	}
	a.OnCreateDB = func(user, name, dbUser, pw string) error {
		if err := os.WriteFile(filepath.Join(e.FakeDB, name+".pwd"), []byte(pw), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(e.FakeDB, name+".sql"), nil, 0o600)
	}
}

func (e *Env) docroot(token, host string) string {
	for _, w := range e.API.WebsitesOf(token) {
		if strings.EqualFold(w.Domain, host) {
			return w.RootDirectory
		}
	}
	e.API.Lock()
	defer e.API.Unlock()
	for parent, list := range e.API.Parked {
		for _, p := range list {
			if d, _ := p["domain"].(string); strings.EqualFold(d, host) {
				for _, w := range e.API.WebsitesFor[token] {
					if w.Domain == parent {
						return w.RootDirectory
					}
				}
			}
		}
	}
	return ""
}

func (e *Env) serveSite(w http.ResponseWriter, r *http.Request, token string) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	doc := e.docroot(token, host)
	if doc == "" {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "<html><title>Domain belum terhubung</title></html>")
		return
	}
	if site, err := os.ReadFile(filepath.Join(doc, ".wp-siteurl")); err == nil {
		want := strings.TrimPrefix(strings.TrimPrefix(string(site), "https://"), "http://")
		if !strings.EqualFold(want, host) {
			w.Header().Set("Location", string(site)+r.URL.Path)
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
	}
	p := r.URL.Path
	if p == "/" {
		for _, idx := range []string{"index.php", "index.html"} {
			if _, err := os.Stat(filepath.Join(doc, idx)); err == nil {
				p = "/" + idx
				break
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(doc, filepath.FromSlash(p)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Write(data)
}

func (e *Env) Client(alias string) (*hostinger.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.clients[alias]; ok {
		return c, nil
	}
	token, err := e.Secrets.Token(alias)
	if err != nil {
		return nil, err
	}
	c, err := hostinger.NewClient(hostinger.Options{Token: token, BaseURL: e.API.URL, Limiter: e.Limiters.For(alias), RetryMax: 1, Logger: e.Log, Sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() }})
	if err != nil {
		return nil, err
	}
	e.clients[alias] = c
	return c, nil
}

func (e *Env) Verifier() *verify.Verifier {
	addr := func(s *httptest.Server) string { return strings.TrimPrefix(s.URL, "http://") }
	return &verify.Verifier{
		Resolve: func(ctx context.Context, host string) (string, error) {
			for _, w := range e.API.WebsitesOf(DstToken) {
				if strings.EqualFold(w.Domain, host) {
					return DestIP, nil
				}
			}
			return SourceIP, nil
		},
		DialOverride: func(a string) string {
			h, _, _ := net.SplitHostPort(a)
			switch h {
			case DestIP:
				return addr(e.DstHTTP)
			case SourceIP:
				return addr(e.SrcHTTP)
			}
			if strings.HasSuffix(h, ".hostingersite.com") {
				return addr(e.DstHTTP)
			}
			return addr(e.SrcHTTP)
		},
		DummyScheme: "http",
		HostScheme:  "http",
		SourceTitle: func(ctx context.Context, domain string) (string, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.SrcHTTP.URL+"/", nil)
			req.Host = domain
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return "", err
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			return verify.Title(string(b)), nil
		},
		Timeout: 5 * time.Second,
	}
}

func (e *Env) LookupNS(ctx context.Context, domain string) ([]string, error) {
	if domain == "mati.com" {
		return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
	}
	return []string{"ns1.dns-parking.com", "ns2.dns-parking.com"}, nil
}

func (e *Env) AddTokens() {
	e.T.Helper()
	m := &account.Manager{Store: e.Store, Secrets: e.Secrets, Log: e.Log, Redactor: e.Redactor, NewClient: func(alias, token string) (*hostinger.Client, error) {
		return hostinger.NewClient(hostinger.Options{Token: token, BaseURL: e.API.URL, Limiter: e.Limiters.For(alias)})
	}}
	ctx := context.Background()
	if _, err := m.Add(ctx, SrcAlias, SrcToken); err != nil {
		e.T.Fatal(err)
	}
	if _, err := m.Add(ctx, DstAlias, DstToken); err != nil {
		e.T.Fatal(err)
	}
}

func (e *Env) SetupSSH() {
	e.T.Helper()
	ctx := context.Background()
	kp, err := e.SSH.Keys()
	if err != nil {
		e.T.Fatal(err)
	}
	e.SrcSSH.Authorize(kp.Signer.PublicKey())
	e.DstSSH.Authorize(kp.Signer.PublicKey())
	for _, s := range []struct {
		id  string
		srv *sshtest.Server
	}{{SrcAccount, e.SrcSSH}, {DstAccount, e.DstSSH}} {
		res, err := e.SSH.Check(ctx, store.SSHInfo{AccountID: s.id, Host: s.srv.Host(), Port: s.srv.Port(), User: s.id}, false)
		if err != nil || !res.Ready() {
			e.T.Fatalf("ssh %s: %+v %v", s.id, res, err)
		}
	}
}

func (e *Env) Audit() {
	e.T.Helper()
	reg, _ := hostinger.DefaultRegistry()
	a := &capability.Auditor{Store: e.Store, Registry: reg, SSH: e.SSH, Log: e.Log, NewClient: e.Client}
	for _, alias := range []string{SrcAlias, DstAlias} {
		if _, err := a.AuditAlias(context.Background(), alias); err != nil {
			e.T.Fatal(err)
		}
	}
}

func (e *Env) Inventory() {
	e.T.Helper()
	sc := &inventory.Scanner{Store: e.Store, NewClient: e.Client, SSH: e.SSH, DataDir: e.DataDir, Redactor: e.Redactor, Log: e.Log, LookupNS: e.LookupNS}
	if _, err := sc.ScanAlias(context.Background(), SrcAlias); err != nil {
		e.T.Fatal(err)
	}
}

func (e *Env) Plan(domains ...string) string {
	e.T.Helper()
	pl := &planner.Planner{Store: e.Store, Quotas: e.Config.KuotaPaket, NewClient: e.Client}
	p, err := pl.Build(context.Background(), planner.Request{SourceAlias: SrcAlias, SourceAccount: SrcAccount, Domains: domains, DestAlias: DstAlias, DestAccount: DstAccount})
	if err != nil {
		e.T.Fatal(err)
	}
	if p.Rejected() {
		e.T.Fatalf("rencana ditolak: %+v", p.Failures())
	}
	id, err := pl.Save(context.Background(), p)
	if err != nil {
		e.T.Fatal(err)
	}
	return id
}

func (e *Env) Prepare(domains ...string) string {
	e.T.Helper()
	e.AddTokens()
	e.SetupSSH()
	e.Audit()
	e.Inventory()
	return e.Plan(domains...)
}

func (e *Env) Deps(stages ...pipeline.Stage) pipeline.Deps {
	return pipeline.Deps{
		Store:            e.Store,
		Secrets:          e.Secrets,
		SSH:              e.SSH,
		NewClient:        e.Client,
		Backup:           &backup.Engine{Store: e.Store, BackupDir: filepath.Join(e.DataDir, "backups"), Redactor: e.Redactor, Log: e.Log, Version: "test"},
		Verifier:         e.Verifier(),
		Redactor:         e.Redactor,
		Log:              e.Log,
		RetryMax:         3,
		Parallel:         2,
		ProvisionPoll:    time.Millisecond,
		ProvisionTimeout: 5 * time.Second,
		Sleep:            func(ctx context.Context, d time.Duration) error { return ctx.Err() },
		Stages:           stages,
	}
}

func (e *Env) RunUntilIdle(r *pipeline.Runner, max int) {
	e.T.Helper()
	for i := 0; i < max; i++ {
		sum, err := r.RunOnce(context.Background())
		if err != nil {
			e.T.Fatal(err)
		}
		if sum.Processed == 0 && sum.Pending == 0 {
			return
		}
	}
}

func (e *Env) Item(domain string) *store.PipelineItem {
	e.T.Helper()
	it, err := e.Store.FindItem(context.Background(), domain, "")
	if err != nil {
		e.T.Fatal(err)
	}
	return it
}

func (e *Env) ExpireBackoff(domain string) {
	e.T.Helper()
	it := e.Item(domain)
	e.Store.DB().Exec(`UPDATE items SET updated_at = ? WHERE id = ?`, "2000-01-01T00:00:00Z", it.ID)
}

func (e *Env) AssertNoSecretsOnDisk(secretsList ...string) {
	e.T.Helper()
	filepath.Walk(e.DataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(p, ".sql.gz") || strings.HasSuffix(p, ".tar.gz") {
			return nil
		}
		data, _ := os.ReadFile(p)
		for _, s := range secretsList {
			if bytes.Contains(data, []byte(s)) {
				e.T.Errorf("rahasia %q bocor ke %s", s[:6]+"…", p)
			}
		}
		return nil
	})
	for _, s := range secretsList {
		if strings.Contains(e.LogBuf.String(), s) {
			e.T.Errorf("rahasia bocor ke log")
		}
	}
}

func (e *Env) Count(query string, args ...any) int {
	var n int
	e.Store.DB().QueryRow(query, args...).Scan(&n)
	return n
}

func (e *Env) Count2(itemID int64) string {
	var s string
	e.Store.DB().QueryRow(`SELECT instructions FROM manual_tasks WHERE item_id = ? ORDER BY id DESC LIMIT 1`, itemID).Scan(&s)
	return s
}

const (
	MailOldInfo  = "FAKE-lama-info-Pass1"
	MailOldAdmin = "FAKE-lama-admin-Pass2"
)

var BigAttachment = bytes.Repeat([]byte("LAMPIRAN-BESAR-"), 200000)

func (e *Env) SetupMail() {
	e.T.Helper()
	srv, err := imaptest.New()
	if err != nil {
		e.T.Fatal(err)
	}
	e.T.Cleanup(func() { srv.Close() })
	e.IMAP = srv
	a := e.API
	a.Lock()
	a.MailOrdersFor[SrcToken] = []map[string]any{{"id": "mo-src", "status": "active", "domain": map[string]any{"id": "d1", "name": "domainanda.com"}, "plan": map[string]any{"name": "hostinger_free"}}}
	a.MailOrdersFor[DstToken] = []map[string]any{}
	a.Mailboxes = map[string][]map[string]any{"mo-src": {
		{"id": "mbx-info", "address": "info@domainanda.com", "status": "active", "usage": map[string]any{"messages_used": 3, "storage_used": 3000}},
		{"id": "mbx-admin", "address": "admin@domainanda.com", "status": "active", "usage": map[string]any{"messages_used": 2, "storage_used": 3000}},
	}}
	a.MailExtras["mo-src"] = map[string][]map[string]any{
		"aliases":     {{"id": "al1", "address": "sales@domainanda.com", "mailbox": map[string]any{"id": "mbx-info", "address": "info@domainanda.com"}}},
		"forwarders":  {{"id": "fw1", "destination": "bos@contoh-luar.com", "is_keep_copy_enabled": true, "mailbox": map[string]any{"id": "mbx-admin", "address": "admin@domainanda.com"}}},
		"catchalls":   {{"id": "ca1", "mailbox": map[string]any{"id": "mbx-info", "address": "info@domainanda.com"}}},
		"autoreplies": {{"id": "ar1", "subject": "Cuti", "body": "Sedang cuti", "mailbox": map[string]any{"id": "mbx-admin", "address": "admin@domainanda.com"}}},
	}
	a.OnMailboxPassword = func(addr, pw string) { srv.SetPassword(addr, pw) }
	a.OnMailboxCreate = func(addr, pw string) { srv.AddUser(addr, pw) }
	a.OnMailboxDelete = func(addr string) { srv.DeleteUser(addr) }
	a.Unlock()
	srv.AddUser("info@domainanda.com", MailOldInfo)
	srv.AddUser("admin@domainanda.com", MailOldAdmin)
	srv.Seed("info@domainanda.com", "INBOX",
		imaptest.Message{Flags: []string{`\Seen`}, Date: "01-Nov-2026 08:00:00 +0700", Body: []byte("Subject: Pesanan 1\r\n\r\nHalo")},
		imaptest.Message{Flags: []string{`\Flagged`}, Date: "02-Nov-2026 08:00:00 +0700", Body: append([]byte("Subject: Lampiran\r\n\r\n"), BigAttachment...)},
	)
	srv.AddUser("info@domainanda.com", MailOldInfo)
	srvCreate(srv, "info@domainanda.com", "Arsip Lama")
	srv.Seed("info@domainanda.com", "Arsip Lama", imaptest.Message{Flags: []string{`\Seen`}, Date: "01-Jan-2025 08:00:00 +0700", Body: []byte("Subject: Lama\r\n\r\nArsip")})
	srvCreate(srv, "admin@domainanda.com", "Sent")
	srv.Seed("admin@domainanda.com", "INBOX", imaptest.Message{Date: "03-Nov-2026 08:00:00 +0700", Body: []byte("Subject: Admin\r\n\r\nx")})
	srv.Seed("admin@domainanda.com", "Sent", imaptest.Message{Flags: []string{`\Seen`}, Date: "04-Nov-2026 08:00:00 +0700", Body: []byte("Subject: Terkirim\r\n\r\ny")})
}

func srvCreate(srv *imaptest.Server, addr, folder string) {
	srv.Seed(addr, folder)
}

func (e *Env) ActivateDestMail() {
	e.API.Lock()
	defer e.API.Unlock()
	e.API.MailOrdersFor[DstToken] = []map[string]any{{"id": "mo-dst", "status": "active", "domain": map[string]any{"id": "d2", "name": "domainanda.com"}, "plan": map[string]any{"name": "hostinger_free"}}}
}

func (e *Env) AddSites(n int) []string {
	e.T.Helper()
	var domains []string
	e.API.Lock()
	defer e.API.Unlock()
	for i := 1; i <= n; i++ {
		d := fmt.Sprintf("web%02d.com", i)
		domains = append(domains, d)
		unit := e.SrcUnit(d)
		withDB := i%3 == 0
		if withDB {
			db := fmt.Sprintf("u494983133_db%02d", i)
			pw := fmt.Sprintf("FAKE-skala-%02d-Sandi", i)
			write(e.T, filepath.Join(unit, "public_html", "index.php"), fmt.Sprintf(`<html><head><title>Web %02d</title><link href="/a.css" rel="stylesheet"><script src="/b.js"></script></head><body><img src="/c.png"></body></html>`, i))
			write(e.T, filepath.Join(unit, "public_html", "config.php"), "<?php")
			write(e.T, filepath.Join(unit, "public_html", "wp-config.php"), fmt.Sprintf("<?php\ndefine( 'DB_NAME', '%s' );\ndefine( 'DB_USER', '%s' );\ndefine( 'DB_PASSWORD', '%s' );\ndefine( 'DB_HOST', 'localhost' );\n", db, db, pw))
			os.MkdirAll(filepath.Join(unit, "public_html", "wp-content"), 0o755)
			os.MkdirAll(filepath.Join(unit, "public_html", "wp-includes"), 0o755)
			write(e.T, filepath.Join(unit, "public_html", "wp-content", "index.php"), "<?php")
			write(e.T, filepath.Join(unit, "public_html", "wp-includes", "index.php"), "<?php")
			write(e.T, filepath.Join(e.FakeDB, db+".sql"), fmt.Sprintf("CREATE TABLE `t` (\n  `id` int\n);\nINSERT INTO `t` VALUES (%d),(%d);\n", i, i+100))
			write(e.T, filepath.Join(e.FakeDB, db+".pwd"), pw)
			e.API.Databases[SrcAccount] = append(e.API.Databases[SrcAccount], map[string]any{"name": db, "user": db, "domain": d})
		} else {
			write(e.T, filepath.Join(unit, "public_html", "index.html"), fmt.Sprintf(`<html><head><title>Web %02d</title><link href="/a.css" rel="stylesheet"><script src="/b.js"></script></head><body><img src="/c.png"></body></html>`, i))
		}
		for _, f := range []string{"a.css", "b.js", "c.png"} {
			write(e.T, filepath.Join(unit, "public_html", f), f)
		}
		e.API.WebsitesFor[SrcToken] = append(e.API.WebsitesFor[SrcToken], hostinger.Website{Domain: d, Username: SrcAccount, OrderID: 101, VhostType: "addon", IsEnabled: true, RootDirectory: filepath.Join(unit, "public_html")})
		e.API.Portfolio = append(e.API.Portfolio, hostinger.PortfolioDomain{ID: int64(100 + i), Domain: d, Status: "active"})
		e.API.PHP[d] = "8.2"
	}
	return domains
}
