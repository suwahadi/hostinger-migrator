package integration

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostinger-migrator/internal/cli"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/tests/fakehostinger"
)

const (
	goodToken = "FAKE-integrasi-token-aaaa1111bbbb2222"
	badToken  = "FAKE-integrasi-salah-cccc3333dddd4444"
)

type harness struct {
	t       *testing.T
	dir     string
	config  string
	srv     *fakehostinger.Server
	backend *secrets.MemoryBackend
	spawned int
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func fakeNS(ctx context.Context, domain string) ([]string, error) {
	switch domain {
	case "domainanda.com":
		return []string{"ns1.dns-parking.com", "ns2.dns-parking.com"}, nil
	case "toko.id":
		return []string{"ana.ns.cloudflare.com", "bob.ns.cloudflare.com"}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := fakehostinger.New(true, goodToken)
	srv.EchoTokenInError = true
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	content := "storage:\n  data_dir: ./data\napi:\n  base_url: " + srv.URL + "\n  retry_maks: 1\n  rate_per_menit: 6000\n"
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, dir: dir, config: cfg, srv: srv, backend: secrets.NewMemoryBackend()}
}

func (h *harness) run(stdin string, args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	env := cli.Env{
		Stdin:          strings.NewReader(stdin),
		Stdout:         &h.stdout,
		Stderr:         &h.stderr,
		SecretsBackend: h.backend,
		HTTPClient:     h.srv.Client(),
		Redactor:       logx.NewRedactor(),
		LookupNS:       fakeNS,
		SpawnWorker: func(ctx context.Context, a *cli.App) error {
			h.spawned++
			return nil
		},
	}
	return cli.Execute(append([]string{"--config", h.config}, args...), env)
}

func (h *harness) assertNoLeak(secret string, outputs ...string) {
	h.t.Helper()
	for _, o := range outputs {
		if strings.Contains(o, secret) {
			h.t.Fatalf("token bocor ke output terminal:\n%s", o)
		}
	}
	filepath.Walk(filepath.Join(h.dir, "data"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			h.t.Fatal(rerr)
		}
		if bytes.Contains(data, []byte(secret)) {
			h.t.Fatalf("token bocor ke file %s", p)
		}
		return nil
	})
}

func TestTokenNeverStoredInDBOrLogs(t *testing.T) {
	h := newHarness(t)
	var outputs []string

	code := h.run(badToken+"\n", "account", "add", "--alias", "salah", "--token-stdin")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitAction {
		t.Fatalf("token salah harus exit 2, dapat %d\n%s%s", code, h.stdout.String(), h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "token ditolak") {
		t.Fatalf("pesan token ditolak tidak muncul: %s", h.stderr.String())
	}

	code = h.run(goodToken+"\n", "account", "add", "--alias", "xyz", "--token-stdin")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitOK {
		t.Fatalf("token benar harus exit 0, dapat %d\n%s%s", code, h.stdout.String(), h.stderr.String())
	}
	for _, want := range []string{"Memvalidasi token... OK", "Ditemukan 2 akun di token xyz", "u494983133  (Premium) · 2 website", "u597522494  (Single) · 1 website"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Fatalf("output tidak memuat %q:\n%s", want, h.stdout.String())
		}
	}

	code = h.run("", "account", "list")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitOK || !strings.Contains(h.stdout.String(), "xyz") || !strings.Contains(h.stdout.String(), "AKTIF") {
		t.Fatalf("list salah (%d):\n%s", code, h.stdout.String())
	}

	h.srv.Revoke(goodToken)
	code = h.run("", "account", "test", "xyz")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitAction {
		t.Fatalf("token dicabut harus exit 2, dapat %d\n%s", code, h.stderr.String())
	}
	code = h.run("", "account", "list")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if !strings.Contains(h.stdout.String(), "DITOLAK") {
		t.Fatalf("status DITOLAK tidak tampil:\n%s", h.stdout.String())
	}

	stored, err := h.backend.Get(secrets.Service, "xyz")
	if err != nil || stored != goodToken {
		t.Fatal("token harus tetap ada di Credential Manager")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "data", "migrator.db")); err != nil {
		t.Fatal("database harus dibuat")
	}
	logs, _ := filepath.Glob(filepath.Join(h.dir, "data", "logs", "*.log"))
	if len(logs) == 0 {
		t.Fatal("file log harus dibuat")
	}
	h.assertNoLeak(goodToken, outputs...)
	h.assertNoLeak(badToken, outputs...)
}

func TestRemoveRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	if code := h.run(goodToken+"\n", "account", "add", "--alias", "xyz", "--token-stdin"); code != cli.ExitOK {
		t.Fatalf("add gagal: %s", h.stderr.String())
	}
	h.run("bukan\n", "account", "remove", "xyz")
	if !strings.Contains(h.stdout.String(), "Dibatalkan") || h.backend.Len() != 1 {
		t.Fatal("konfirmasi salah harus membatalkan")
	}
	if code := h.run("xyz\n", "account", "remove", "xyz"); code != cli.ExitOK || h.backend.Len() != 0 {
		t.Fatalf("hapus gagal (%d): %s", code, h.stderr.String())
	}
	if code := h.run("", "account", "remove", "xyz", "--ya"); code != cli.ExitAction {
		t.Fatalf("alias tidak ada harus exit 2, dapat %d", code)
	}
}

func TestWizardFlow(t *testing.T) {
	h := newHarness(t)
	input := strings.Join([]string{"9", "7", "1", "1", goodToken, "wizard", "2", "0", "0"}, "\n") + "\n"
	code := h.run(input)
	out := h.stdout.String()
	if code != cli.ExitOK {
		t.Fatalf("wizard exit %d: %s", code, h.stderr.String())
	}
	for _, want := range []string{"HOSTINGER MIGRATION MANAGER", "1. Kelola API token", "Pilihan tidak dikenal", "belum tersedia pada versi ini", "KELOLA API TOKEN", "Ditemukan 2 akun di token wizard", "Sampai jumpa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("wizard tidak memuat %q:\n%s", want, out)
		}
	}
	h.assertNoLeak(goodToken, out, h.stderr.String())
}

func TestWizardStopsOnEOF(t *testing.T) {
	h := newHarness(t)
	if code := h.run(""); code != cli.ExitOK {
		t.Fatalf("EOF harus keluar dengan rapi, dapat %d", code)
	}
}

func TestHelpAndErrorsInIndonesian(t *testing.T) {
	h := newHarness(t)
	h.run("", "--help")
	help := h.stdout.String()
	for _, banned := range []string{"Usage:", "Flags:", "Available Commands", "Global Flags", "help for"} {
		if strings.Contains(help, banned) {
			t.Fatalf("bantuan masih berbahasa Inggris (%q):\n%s", banned, help)
		}
	}
	if !strings.Contains(help, "Penggunaan:") || !strings.Contains(help, "Perintah:") {
		t.Fatalf("bantuan tidak berbahasa Indonesia:\n%s", help)
	}
	h.run("", "account", "add", "--help")
	if strings.Contains(h.stdout.String(), "Flags:") || !strings.Contains(h.stdout.String(), "Opsi:") {
		t.Fatalf("bantuan subperintah salah:\n%s", h.stdout.String())
	}
	if code := h.run("", "tidakada"); code != cli.ExitError || !strings.Contains(h.stderr.String(), "perintah tidak dikenal") {
		t.Fatalf("perintah tidak dikenal (%d): %s", code, h.stderr.String())
	}
	if code := h.run("", "account", "test"); code != cli.ExitError || !strings.Contains(h.stderr.String(), "membutuhkan 1 argumen") {
		t.Fatalf("argumen kurang (%d): %s", code, h.stderr.String())
	}
	if code := h.run("", "account", "list", "--salah"); code != cli.ExitError || !strings.Contains(h.stderr.String(), "opsi tidak dikenal") {
		t.Fatalf("opsi tidak dikenal (%d): %s", code, h.stderr.String())
	}
}

func TestVersionNeedsNoConfig(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.config)
	if code := h.run("", "version"); code != cli.ExitOK || !strings.Contains(h.stdout.String(), "migrator ") {
		t.Fatalf("version gagal: %s", h.stderr.String())
	}
	if _, err := os.Stat(h.config); err == nil {
		t.Fatal("version tidak boleh membuat config")
	}
}

func TestDefaultConfigCreatedOnFirstRun(t *testing.T) {
	h := newHarness(t)
	os.Remove(h.config)
	h.run("", "account", "list")
	data, err := os.ReadFile(h.config)
	if err != nil || !strings.Contains(string(data), "rate_per_menit: 60") {
		t.Fatalf("config bawaan tidak dibuat: %v", err)
	}
	if strings.Contains(strings.ToLower(string(data)), "token:") {
		t.Fatal("config tidak boleh berisi token")
	}
}
