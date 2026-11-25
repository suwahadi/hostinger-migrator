package planner

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/fakehostinger"
)

const (
	srcToken = "FAKE-src-token-1234567890abcdef"
	dstToken = "FAKE-dst-token-1234567890abcdef"
)

type fixture struct {
	pl  *Planner
	st  *store.Store
	api *fakehostinger.Server
}

func boolp(b bool) *bool { return &b }

func newFixture(t *testing.T, destPlan string) *fixture {
	t.Helper()
	ctx := context.Background()
	api := fakehostinger.New(false, srcToken, dstToken)
	t.Cleanup(api.Close)
	api.Websites = append(api.Websites,
		hostinger.Website{Domain: "lama1.com", Username: "u73839202", VhostType: "main", IsEnabled: true},
		hostinger.Website{Domain: "lama2.com", Username: "u73839202", VhostType: "addon", IsEnabled: true},
		hostinger.Website{Domain: "sub.lama1.com", Username: "u73839202", VhostType: "subdomain", IsEnabled: true},
	)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetClock(func() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) })
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		for _, a := range []string{"xyz", "zyx"} {
			if err := st.InsertCredential(ctx, tx, a); err != nil {
				return err
			}
		}
		if err := st.ReplaceHostingAccounts(ctx, tx, "xyz", []store.HostingAccount{{ID: "u494983133", Plan: "hostinger_premium"}}); err != nil {
			return err
		}
		return st.ReplaceHostingAccounts(ctx, tx, "zyx", []store.HostingAccount{{ID: "u73839202", Plan: destPlan}})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u494983133", "u73839202"} {
		st.SaveSSH(ctx, store.SSHInfo{AccountID: id, Host: "1.2.3.4", Port: 65002, User: id, Fingerprint: "SHA256:x", Status: store.SSHReady, Auth: store.SSHAuthKey})
	}
	snap := &store.InventorySnapshot{AccountID: "u494983133", Alias: "xyz", SSHUsed: true, Websites: []store.Website{
		{Domain: "domainanda.com", UnitPath: "/home/u494983133/domains/domainanda.com", VhostType: "main", SiteType: "WordPress", TypeConfidence: 0.95, SizeBytes: 1 << 30, FileCount: 100, SizeSource: store.SizeFromSSH, DomainStatus: store.DomainActive, IsEnabled: true, PHPVersion: "8.2", NSExternal: boolp(false)},
		{Domain: "blog.domainanda.com", UnitPath: "/home/u494983133/domains/domainanda.com/public_html/blog", VhostType: "subdomain", IncludedIn: "domainanda.com", DomainStatus: store.DomainActive, IsEnabled: true, SizeBytes: -1, FileCount: -1},
		{Domain: "toko.id", UnitPath: "/home/u494983133/domains/toko.id", VhostType: "addon", SiteType: "Laravel", SizeBytes: 500 << 20, FileCount: 10, SizeSource: store.SizeFromSSH, DomainStatus: store.DomainActive, IsEnabled: true, NSExternal: boolp(true), Nameservers: []string{"ana.ns.cloudflare.com"}},
		{Domain: "mati.com", UnitPath: "/home/u494983133/domains/mati.com", VhostType: "addon", SiteType: "Statis", SizeBytes: 1 << 20, FileCount: 3, SizeSource: store.SizeFromSSH, DomainStatus: store.DomainExpired, IsEnabled: true},
	}, Databases: []store.DatabaseRow{
		{WebsiteDomain: "domainanda.com", Name: "u494983133_wp", SizeBytes: 50 << 20, InAPI: true, Source: "public_html/wp-config.php"},
		{WebsiteDomain: "toko.id", Name: "u494983133_toko", SizeBytes: 10 << 20, Source: ".env"},
	}, Mailboxes: []store.MailboxRow{{Domain: "domainanda.com", Address: "info@domainanda.com", MessageCount: 10}}}
	if err := st.SaveInventory(ctx, snap); err != nil {
		t.Fatal(err)
	}
	st.SaveCapabilities(ctx, "u73839202", []store.Capability{
		{Code: "QUOTA", Status: "UNKNOWN", DetailJSON: `{"disk_sisa_byte": 107374182400}`},
		{Code: "API_DB_LIST", Status: "SUPPORTED", DetailJSON: `{"jumlah_database": 2}`},
		{Code: "API_CREATE_WEBSITE", Status: "UNKNOWN"},
	})
	pl := &Planner{
		Store: st,
		Now:   func() time.Time { return time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC) },
		NewClient: func(alias string) (*hostinger.Client, error) {
			tok := srcToken
			if alias == "zyx" {
				tok = dstToken
			}
			return hostinger.NewClient(hostinger.Options{Token: tok, BaseURL: api.URL, Limiter: hostinger.NewLimiter(60000)})
		},
	}
	return &fixture{pl: pl, st: st, api: api}
}

func req(domains ...string) Request {
	return Request{SourceAlias: "xyz", SourceAccount: "u494983133", Domains: domains, DestAlias: "zyx", DestAccount: "u73839202"}
}

func check(p *Plan, code string) Check {
	for _, c := range p.Checks {
		if c.Code == code {
			return c
		}
	}
	return Check{}
}

func TestPlanHappyPathAndSave(t *testing.T) {
	f := newFixture(t, "hostinger_business")
	ctx := context.Background()
	p, err := f.pl.Build(ctx, req("domainanda.com", "toko.id", "mati.com"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Rejected() {
		t.Fatalf("ditolak: %+v", p.Failures())
	}
	if p.DBCount != 2 || p.MailboxCount != 1 || p.TotalSize != (1<<30)+(500<<20)+(1<<20)+(60<<20) {
		t.Fatalf("total = %d db %d mail %d", p.TotalSize, p.DBCount, p.MailboxCount)
	}
	wp := p.Items[0]
	if len(wp.Included) != 1 || wp.Included[0] != "blog.domainanda.com" {
		t.Fatalf("subdomain ikut = %v", wp.Included)
	}
	joined := strings.Join(wp.Steps, "\n")
	for _, want := range []string{"tar seluruh folder /home/u494983133/domains/domainanda.com", "mysqldump", "MANUAL_CREATE_SITE", "header Host: domainanda.com", "MANUAL_SOURCE_RENAME", "Email: 1 mailbox"} {
		if !strings.Contains(joined, want) {
			t.Errorf("langkah tidak memuat %q", want)
		}
	}
	if !strings.Contains(strings.Join(p.Items[1].Steps, "\n"), "nameserver eksternal") || !strings.Contains(strings.Join(p.Items[1].Notes, " "), "Nameserver eksternal") {
		t.Fatalf("NS eksternal: %+v", p.Items[1])
	}
	mati := p.Items[2]
	if mati.ExpectedFinal != FinalDummyOnly || !strings.Contains(mati.Steps[len(mati.Steps)-1], "DUMMY_ONLY") || check(p, "DUMMY_ONLY").Status != CheckOK {
		t.Fatalf("dummy only = %+v", mati)
	}
	if f.api.Writes() != 0 {
		t.Fatal("Build tidak boleh mengirim request tulis")
	}
	if bs, _ := f.st.ListBatches(ctx); len(bs) != 0 {
		t.Fatal("Build tidak boleh menyimpan batch")
	}
	id, err := f.pl.Save(ctx, p)
	if err != nil || id != "MIG-20261122-001" {
		t.Fatalf("id = %s err = %v", id, err)
	}
	b, items, _ := f.st.GetBatch(ctx, id)
	if b.Status != store.BatchPlanned || len(items) != 3 || items[2].ExpectedFinal != FinalDummyOnly || items[0].State != store.ItemPlanned {
		t.Fatalf("batch = %+v items = %+v", b, items)
	}
	p2, _ := f.pl.Build(ctx, req("domainanda.com"))
	if check(p2, "BATCH_LAIN").Status != CheckWarn || !p2.Items[0].Blocked {
		t.Fatalf("website di batch aktif harus BLOCKED: %+v", p2.Checks)
	}
}

func TestPlanIgnoresCapacity(t *testing.T) {
	f := newFixture(t, "hostinger_single")
	ctx := context.Background()
	f.st.SaveCapabilities(ctx, "u73839202", []store.Capability{{Code: "QUOTA", Status: "UNKNOWN", DetailJSON: `{"disk_sisa_byte": 1048576}`}})
	p, err := f.pl.Build(ctx, req("domainanda.com"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Rejected() {
		t.Fatalf("kapasitas tujuan tidak boleh menolak rencana: %+v", p.Failures())
	}
	for _, code := range []string{"KUOTA_WEBSITE", "DISK_TUJUAN", "KUOTA_DATABASE"} {
		if c := check(p, code); c.Code != "" {
			t.Errorf("%s masih diperiksa: %+v", code, c)
		}
	}
}

func TestPlanDomainConflict(t *testing.T) {
	f := newFixture(t, "hostinger_business")
	f.api.Websites = append(f.api.Websites, hostinger.Website{Domain: "toko.id", Username: "u73839202", VhostType: "addon"})
	p, _ := f.pl.Build(context.Background(), req("domainanda.com", "toko.id"))
	if c := check(p, "BENTROK_DOMAIN"); c.Status != CheckWarn || !strings.Contains(c.Message, "toko.id") {
		t.Fatalf("bentrok = %+v", c)
	}
	if !p.Items[1].Blocked || p.Items[0].Blocked || len(p.ActiveItems()) != 1 {
		t.Fatalf("item = %+v", p.Items)
	}
	id, err := f.pl.Save(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	_, items, _ := f.st.GetBatch(context.Background(), id)
	if items[1].State != store.ItemBlocked || !strings.Contains(items[1].LastError, "tidak akan ditimpa") {
		t.Fatalf("item tersimpan = %+v", items[1])
	}
	p2, _ := f.pl.Build(context.Background(), req("toko.id"))
	if check(p2, "BENTROK_DOMAIN").Status != CheckFail || !p2.Rejected() {
		t.Fatalf("semua bentrok harus ditolak: %+v", p2.Checks)
	}
}

func TestPlanSSHChecks(t *testing.T) {
	f := newFixture(t, "hostinger_business")
	ctx := context.Background()
	f.st.SaveSSH(ctx, store.SSHInfo{AccountID: "u494983133", Status: store.SSHFailed})
	p, _ := f.pl.Build(ctx, req("domainanda.com"))
	if c := check(p, "SSH_SUMBER"); c.Status != CheckWarn || !p.Items[0].ManualBackup || !strings.Contains(p.Items[0].Steps[0], "MANUAL_BACKUP") {
		t.Fatalf("ssh sumber = %+v %+v", c, p.Items[0].Steps)
	}
	f.st.SaveSSH(ctx, store.SSHInfo{AccountID: "u73839202", Status: store.SSHNotConfigured})
	p, _ = f.pl.Build(ctx, req("domainanda.com"))
	if c := check(p, "SSH_TUJUAN"); c.Status != CheckFail || !p.Rejected() {
		t.Fatalf("ssh tujuan = %+v", c)
	}
}

func TestPlanInvalidSelectionAndNoInventory(t *testing.T) {
	f := newFixture(t, "hostinger_business")
	ctx := context.Background()
	p, _ := f.pl.Build(ctx, req("blog.domainanda.com", "tidakada.com"))
	c := check(p, "PILIHAN_WEBSITE")
	if c.Status != CheckFail || !strings.Contains(c.Message, "ikut folder domainanda.com") || !strings.Contains(c.Message, "tidak ada di inventaris") {
		t.Fatalf("pilihan = %+v", c)
	}
	r := req("x.com")
	r.SourceAlias, r.SourceAccount, r.DestAlias, r.DestAccount = "zyx", "u73839202", "xyz", "u494983133"
	p, _ = f.pl.Build(ctx, r)
	if check(p, "INVENTARIS").Status != CheckFail || !p.Rejected() {
		t.Fatalf("tanpa inventaris = %+v", p.Checks)
	}
	if _, err := f.pl.Build(ctx, Request{SourceAlias: "xyz", SourceAccount: "u000", DestAlias: "zyx", DestAccount: "u73839202"}); err == nil {
		t.Fatal("akun tidak dikenal harus error")
	}
}

func TestPlanRejectedTokenAndSameAccount(t *testing.T) {
	f := newFixture(t, "hostinger_business")
	ctx := context.Background()
	f.st.WithTx(ctx, func(tx *sql.Tx) error {
		return f.st.UpdateCredentialStatus(ctx, tx, "zyx", store.CredentialRejected, "401")
	})
	p, _ := f.pl.Build(ctx, req("domainanda.com"))
	if check(p, "TOKEN_TUJUAN").Status != CheckFail {
		t.Fatalf("token = %+v", p.Checks)
	}
	r := Request{SourceAlias: "xyz", SourceAccount: "u494983133", Domains: []string{"domainanda.com"}, DestAlias: "xyz", DestAccount: "u494983133"}
	p, _ = f.pl.Build(ctx, r)
	if check(p, "AKUN_BERBEDA").Status != CheckFail {
		t.Fatalf("akun sama = %+v", p.Checks)
	}
}

func TestPlanWarnsUnknownDBSource(t *testing.T) {
	f := newFixture(t, "hostinger_business")
	ctx := context.Background()
	if p, _ := f.pl.Build(ctx, req("domainanda.com", "toko.id")); check(p, "KREDENSIAL_DB").Code != "" {
		t.Fatalf("semua sumber diketahui, tidak boleh ada peringatan: %+v", check(p, "KREDENSIAL_DB"))
	}
	if _, err := f.st.DB().ExecContext(ctx, "UPDATE databases SET source = 'api' WHERE name = 'u494983133_toko'"); err != nil {
		t.Fatal(err)
	}
	p, err := f.pl.Build(ctx, req("domainanda.com", "toko.id"))
	if err != nil {
		t.Fatal(err)
	}
	c := check(p, "KREDENSIAL_DB")
	if c.Status != CheckWarn || !strings.Contains(c.Message, "toko.id (u494983133_toko)") || !strings.Contains(c.Message, "--akun u494983133 --refresh") || strings.Contains(c.Message, "domainanda.com") {
		t.Fatalf("peringatan = %+v", c)
	}
	if p.Rejected() {
		t.Fatalf("peringatan tidak boleh menolak: %+v", p.Failures())
	}
	f.st.SaveSSH(ctx, store.SSHInfo{AccountID: "u494983133", Host: "1.2.3.4", Port: 65002, User: "u494983133", Fingerprint: "SHA256:x", Status: store.SSHFailed, Auth: store.SSHAuthKey})
	if p, _ := f.pl.Build(ctx, req("domainanda.com", "toko.id")); check(p, "KREDENSIAL_DB").Code != "" {
		t.Fatalf("SSH sumber tidak siap: backup manual, tidak butuh password: %+v", check(p, "KREDENSIAL_DB"))
	}
}
