package capability

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
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

const token = "FAKE-audit-token-1234567890abcdef"

type fixture struct {
	auditor *Auditor
	st      *store.Store
	api     *fakehostinger.Server
	ssh     *sshtest.Server
	svc     *sshx.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	api := fakehostinger.New(false, token)
	t.Cleanup(api.Close)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "migrator.db"))
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &sshx.Service{Store: st, Secrets: secrets.New(secrets.NewMemoryBackend(), logx.NewRedactor()), KeyDir: filepath.Join(t.TempDir(), "ssh"), Timeout: 5 * time.Second, Log: log}
	reg, _ := hostinger.DefaultRegistry()
	a := &Auditor{
		Store:    st,
		Registry: reg,
		SSH:      svc,
		Log:      log,
		NewClient: func(alias string) (*hostinger.Client, error) {
			return hostinger.NewClient(hostinger.Options{Token: token, BaseURL: api.URL, Limiter: hostinger.NewLimiter(60000)})
		},
	}
	return &fixture{auditor: a, st: st, api: api, ssh: sshSrv, svc: svc}
}

func (f *fixture) readySSH(t *testing.T, tools string) {
	t.Helper()
	ctx := context.Background()
	kp, err := f.svc.Keys()
	if err != nil {
		t.Fatal(err)
	}
	f.ssh.Authorize(kp.Signer.PublicKey())
	f.ssh.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		switch cmd {
		case "echo ok; whoami; pwd":
			return sshtest.ExecResult{Stdout: "ok\nu494983133\n/home/u494983133\n"}
		case toolsCommand:
			return sshtest.ExecResult{Stdout: tools}
		case quotaCommand:
			return sshtest.ExecResult{Stdout: "/dev/sda1 107374182400 1073741824 106300440576 1% /home\n/dev/sda1 600000 12000 588000 2% /home\n"}
		}
		return sshtest.ExecResult{Exit: 127}
	}
	res, err := f.svc.Check(ctx, store.SSHInfo{AccountID: "u494983133", Host: f.ssh.Host(), Port: f.ssh.Port(), User: "u494983133"}, false)
	if err != nil || !res.Ready() {
		t.Fatalf("ssh tidak siap: %+v %v", res, err)
	}
}

func byCode(rep Report) map[string]Result {
	m := map[string]Result{}
	for _, r := range rep.Results {
		m[r.Code] = r
	}
	return m
}

const allTools = "tar=ada\ngzip=ada\nmysqldump=ada\nmysql=ada\nsha256sum=ada\nwp=ada\n"

func TestAuditFullAccount(t *testing.T) {
	f := newFixture(t)
	f.readySSH(t, allTools)
	reps, err := f.auditor.AuditAlias(context.Background(), "xyz")
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 {
		t.Fatalf("laporan = %d", len(reps))
	}
	m := byCode(reps[0])
	want := map[string]string{
		"API_LIST_WEBSITES":   Supported,
		"API_DB_LIST":         Supported,
		"API_DNS_READ":        Supported,
		"API_DNS_SNAPSHOT":    Supported,
		"API_EMAIL_INVENTORY": Supported,
		"API_CREATE_WEBSITE":  Unknown,
		"API_FREE_SUBDOMAIN":  Unknown,
		"API_DB_CREATE":       Unknown,
		"API_DNS_WRITE":       Unknown,
		"API_SSL":             Unknown,
		"API_CHANGE_DOMAIN":   Manual,
		"API_SSH_KEY":         Manual,
		"API_EMAIL_RESTORE":   Unsupported,
		"SSH":                 Supported,
		"SSH_TOOLS":           Supported,
		"QUOTA":               Unknown,
	}
	for code, status := range want {
		if m[code].Status != status {
			t.Errorf("%s = %s (%s), harus %s", code, m[code].Status, m[code].Note, status)
		}
	}
	if !strings.Contains(m["API_DNS_READ"].Note, "domainanda.com") || !strings.Contains(m["API_DB_LIST"].Note, "1 database") {
		t.Fatalf("catatan kurang: %+v", m)
	}
	if !strings.Contains(m["QUOTA"].Note, "1,0 GB dari 100,0 GB") {
		t.Fatalf("quota = %s", m["QUOTA"].Note)
	}
	stored, _ := f.st.ListCapabilities(context.Background(), "u494983133")
	if len(stored) != len(reps[0].Results) {
		t.Fatalf("tersimpan %d dari %d", len(stored), len(reps[0].Results))
	}
	second := byCode(reps[1])
	if second["SSH"].Status != Unknown || !strings.Contains(second["SSH"].Note, "belum diatur") {
		t.Fatalf("akun tanpa SSH = %+v", second["SSH"])
	}
	if second["API_DNS_READ"].Status != Unknown {
		t.Fatalf("zone yang tidak ada di token harus UNKNOWN: %+v", second["API_DNS_READ"])
	}
}

func TestAuditNeverMarksUntestedAsSupported(t *testing.T) {
	f := newFixture(t)
	f.readySSH(t, allTools)
	reps, err := f.auditor.AuditAlias(context.Background(), "xyz")
	if err != nil {
		t.Fatal(err)
	}
	tested := map[string]bool{"API_LIST_WEBSITES": true, "API_DB_LIST": true, "API_DNS_READ": true, "API_DNS_SNAPSHOT": true, "API_EMAIL_INVENTORY": true, "SSH": true, "SSH_TOOLS": true}
	for _, rep := range reps {
		for _, r := range rep.Results {
			if r.Status == Supported && !tested[r.Code] {
				t.Errorf("%s/%s ditandai SUPPORTED tanpa uji nyata", rep.AccountID, r.Code)
			}
			if r.Status == Supported && !strings.HasPrefix(r.Note, "Teruji") {
				t.Errorf("%s SUPPORTED tanpa bukti uji: %s", r.Code, r.Note)
			}
		}
	}
	for _, c := range contractChecks {
		if res := f.auditor.contractOnly(c); res.Status != Unknown {
			t.Errorf("%s dari kontrak saja harus UNKNOWN", c.code)
		}
	}
}

func TestAuditMissingToolsIsManual(t *testing.T) {
	f := newFixture(t)
	f.readySSH(t, "tar=ada\ngzip=ada\nmysqldump=tidak\nmysql=ada\nsha256sum=ada\nwp=tidak\n")
	reps, _ := f.auditor.AuditAlias(context.Background(), "xyz")
	m := byCode(reps[0])
	if m["SSH_TOOLS"].Status != Manual || !strings.Contains(m["SSH_TOOLS"].Note, "mysqldump") || !strings.Contains(m["SSH_TOOLS"].Note, "wp-cli tidak ada") {
		t.Fatalf("tools = %+v", m["SSH_TOOLS"])
	}
}

func TestAuditMailForbiddenIsUnknown(t *testing.T) {
	f := newFixture(t)
	f.api.MailStatus = 403
	reps, err := f.auditor.AuditAlias(context.Background(), "xyz")
	if err != nil {
		t.Fatal(err)
	}
	if m := byCode(reps[0]); m["API_EMAIL_INVENTORY"].Status != Unknown {
		t.Fatalf("mail = %+v", m["API_EMAIL_INVENTORY"])
	}
}

func TestAuditRevokedTokenAborts(t *testing.T) {
	f := newFixture(t)
	f.api.Revoke(token)
	_, err := f.auditor.AuditAlias(context.Background(), "xyz")
	if !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("err = %v", err)
	}
}

func TestAuditSSHHostKeyChangedBlocks(t *testing.T) {
	f := newFixture(t)
	f.readySSH(t, allTools)
	if err := f.ssh.RotateHostKey(); err != nil {
		t.Fatal(err)
	}
	reps, err := f.auditor.AuditAlias(context.Background(), "xyz")
	if err != nil {
		t.Fatal(err)
	}
	m := byCode(reps[0])
	if m["SSH"].Status != Unknown || !strings.Contains(m["SSH"].Note, "fingerprint") {
		t.Fatalf("ssh = %+v", m["SSH"])
	}
	info, _ := f.st.GetSSH(context.Background(), "u494983133")
	if info.Status != store.SSHBlocked {
		t.Fatalf("status = %s", info.Status)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{500: "500 B", 1536: "1,5 KB", 1073741824: "1,0 GB"}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("%d = %s, harus %s", in, got, want)
		}
	}
}
