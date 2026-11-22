package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostinger-migrator/internal/cli"
	"hostinger-migrator/internal/cutover"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/tests/testenv"
)

type runHarness struct {
	t      *testing.T
	e      *testenv.Env
	config string
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	e := testenv.New(t)
	cfg := filepath.Join(e.Root, "config.yaml")
	content := "storage:\n  data_dir: " + e.DataDir + "\napi:\n  base_url: " + e.API.URL + "\n  retry_maks: 3\n  rate_per_menit: 60000\n"
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return &runHarness{t: t, e: e, config: cfg}
}

func (h *runHarness) run(stdin string, args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	env := cli.Env{
		Stdin:          strings.NewReader(stdin),
		Stdout:         &h.stdout,
		Stderr:         &h.stderr,
		SecretsBackend: h.e.Backend,
		Redactor:       h.e.Redactor,
		LookupNS:       h.e.LookupNS,
		SpawnWorker: func(ctx context.Context, a *cli.App) error {
			return a.RunWorkerInProcess(ctx)
		},
		RunnerHook: func(d *pipeline.Deps) {
			td := h.e.Deps()
			d.Verifier = td.Verifier
			d.Sleep = td.Sleep
			d.ProvisionPoll = td.ProvisionPoll
			d.ProvisionTimeout = td.ProvisionTimeout
		},
		StageHook: func(s *cutover.Stage) {
			s.HTTPSScheme = "http"
		},
	}
	return cli.Execute(append([]string{"--config", h.config}, args...), env)
}

func TestRunCommandsEndToEnd(t *testing.T) {
	h := newRunHarness(t)
	h.e.API.SetFail("onboarding", 422)
	h.e.Prepare("domainanda.com", "mati.com")

	if code := h.run("", "start"); code != cli.ExitOK {
		t.Fatalf("start (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	code := h.run("", "status")
	out := h.stdout.String()
	if code != cli.ExitOK || !strings.Contains(out, "Worker: tidak berjalan") || !strings.Contains(out, "MENUNGGU_OPERATOR") || !strings.Contains(out, "menunggu MANUAL_CREATE_SITE") {
		t.Fatalf("status (%d):\n%s%s", code, out, h.stderr.String())
	}
	code = h.run("", "pending")
	out = h.stdout.String()
	if code != cli.ExitAction || !strings.Contains(out, "2 tindakan manual menunggu") || !strings.Contains(out, "migrator confirm MANUAL_CREATE_SITE domainanda.com --nilai") {
		t.Fatalf("pending (%d):\n%s", code, out)
	}
	if code := h.run("", "confirm", "MANUAL_TIDAK_ADA", "domainanda.com"); code != cli.ExitAction {
		t.Fatalf("kode salah harus exit 2, dapat %d", code)
	}

	h.e.API.SetFail("onboarding", 0)
	for _, d := range []string{"domainanda.com", "mati.com"} {
		code = h.run("", "confirm", "MANUAL_CREATE_SITE", d)
		if code != cli.ExitOK {
			t.Fatalf("confirm %s (%d): %s%s", d, code, h.stdout.String(), h.stderr.String())
		}
	}
	h.run("", "status")
	out = h.stdout.String()
	if !strings.Contains(out, "menunggu MANUAL_CREATE_SITE") {
		t.Fatalf("tanpa --nilai harus kembali menunggu:\n%s", out)
	}
	h.e.API.SetFail("onboarding", 0)
	h.e.Store.DB().Exec(`UPDATE manual_tasks SET status = 'MENUNGGU'`)
	h.e.Store.DB().Exec(`UPDATE items SET side_state = NULL, manual_code = NULL, last_error = NULL WHERE source_domain IN ('domainanda.com','mati.com')`)
	h.e.Store.DB().Exec(`DELETE FROM manual_tasks`)
	if code := h.run("", "resume"); code != cli.ExitOK {
		t.Fatalf("resume (%d): %s", code, h.stderr.String())
	}
	h.run("", "status")
	out = h.stdout.String()
	if !strings.Contains(out, "domainanda.com") || !strings.Contains(out, "VERIFIED") || !strings.Contains(out, "DUMMY_ONLY") {
		t.Fatalf("status akhir:\n%s%s", out, h.stderr.String())
	}
	code = h.run("", "status", "--item", "domainanda.com")
	out = h.stdout.String()
	for _, want := range []string{"Status      : VERIFIED", "Database    : u494983133_wp → u73839202_wp (dibuat true, diimport true)", "backup", "VERIFIKASI_HASIL", "PASS", "files.tar.gz"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail item tidak memuat %q", want)
		}
	}
	if t.Failed() {
		t.Log(out)
	}
	if code := h.run("", "pending"); code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Tidak ada tindakan manual") {
		t.Fatalf("pending akhir (%d): %s", code, h.stdout.String())
	}
	if code := h.run("", "stop"); code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Tidak ada worker") {
		t.Fatalf("stop: %s", h.stdout.String())
	}
	if code := h.run("", "retry"); code != cli.ExitError {
		t.Fatalf("retry tanpa opsi harus error, dapat %d", code)
	}
	if code := h.run("", "retry", "--failed"); code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Tidak ada website") {
		t.Fatalf("retry --failed: %s", h.stdout.String())
	}
	h.e.AssertNoSecretsOnDisk(testenv.WPPassword, testenv.LVPassword, testenv.SrcToken, testenv.DstToken)
}

func TestPendingShowsDBPasswordOnlyWhenAsked(t *testing.T) {
	h := newRunHarness(t)
	h.e.API.SetFail("db-create", 422)
	h.e.Prepare("domainanda.com")
	h.run("", "start")
	code := h.run("", "pending")
	out := h.stdout.String()
	if code != cli.ExitAction || !strings.Contains(out, "MANUAL_CREATE_DB") || !strings.Contains(out, "Nama database: u73839202_wp") || strings.Contains(out, "Password u73839202_wp:") {
		t.Fatalf("pending (%d):\n%s", code, out)
	}
	h.run("", "pending", "--tampilkan-rahasia")
	if !strings.Contains(h.stdout.String(), "Password u73839202_wp:") {
		t.Fatalf("password harus tampil dengan --tampilkan-rahasia:\n%s", h.stdout.String())
	}
	h.e.API.SetFail("db-create", 0)
	pwLine := ""
	for _, l := range strings.Split(h.stdout.String(), "\n") {
		if strings.Contains(l, "Password u73839202_wp:") {
			pwLine = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
		}
	}
	h.e.API.OnCreateDB(testenv.DstAccount, "u73839202_wp", "u73839202_wp", pwLine)
	h.e.API.Lock()
	h.e.API.Databases[testenv.DstAccount] = append(h.e.API.Databases[testenv.DstAccount], map[string]any{"name": "u73839202_wp", "user": "u73839202_wp"})
	h.e.API.Unlock()
	if code := h.run("", "confirm", "MANUAL_CREATE_DB", "domainanda.com"); code != cli.ExitOK {
		t.Fatalf("confirm (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	if it := h.e.Item("domainanda.com"); it.State != pipeline.StateVerified {
		t.Fatalf("setelah DB manual = %s/%s %s", it.State, it.SideState, it.LastError)
	}
	h.e.AssertNoSecretsOnDisk(pwLine)
}

func TestCutoverAndRollbackCommands(t *testing.T) {
	h := newRunHarness(t)
	h.e.Prepare("domainanda.com")
	if code := h.run("", "start"); code != cli.ExitOK {
		t.Fatalf("start: %s", h.stderr.String())
	}
	if code := h.run("batal\n", "cutover"); code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Dibatalkan") {
		t.Fatalf("cutover batal: %s", h.stdout.String())
	}
	if it := h.e.Item("domainanda.com"); it.State != pipeline.StateVerified {
		t.Fatalf("tanpa CUTOVER tidak boleh jalan: %s", it.State)
	}
	code := h.run("CUTOVER\n", "cutover")
	out := h.stdout.String()
	if code != cli.ExitOK || !strings.Contains(out, "record A/AAAA diubah ke IP tujuan") || !strings.Contains(out, "Cutover disetujui untuk 1 website") {
		t.Fatalf("cutover (%d): %s%s", code, out, h.stderr.String())
	}
	h.run("", "pending")
	if !strings.Contains(h.stdout.String(), "MANUAL_SOURCE_RENAME") {
		t.Fatalf("harus menunggu ganti domain sumber: %s", h.stdout.String())
	}
	h.e.API.Lock()
	for i, w := range h.e.API.WebsitesFor[testenv.SrcToken] {
		if w.Domain == "domainanda.com" {
			h.e.API.WebsitesFor[testenv.SrcToken][i].Domain = "sementara.hostingersite.com"
		}
	}
	h.e.API.Unlock()
	if code := h.run("", "confirm", "MANUAL_SOURCE_RENAME", "domainanda.com", "--nilai", "sementara.hostingersite.com"); code != cli.ExitOK {
		t.Fatalf("confirm: %s%s", h.stdout.String(), h.stderr.String())
	}
	if it := h.e.Item("domainanda.com"); it.State != pipeline.StateCleanupReady {
		t.Fatalf("setelah cutover = %s/%s %s", it.State, it.SideState, it.LastError)
	}
	if code := h.run("tidak\n", "rollback", "domainanda.com"); code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Dibatalkan") {
		t.Fatalf("rollback batal: %s", h.stdout.String())
	}
	code = h.run("ROLLBACK\n", "rollback", "domainanda.com")
	out = h.stdout.String()
	if code != cli.ExitOK || !strings.Contains(out, "DNS dikembalikan") || !strings.Contains(out, "ganti domain kembali ke domainanda.com") {
		t.Fatalf("rollback (%d): %s%s", code, out, h.stderr.String())
	}
	if it := h.e.Item("domainanda.com"); it.State != pipeline.StateVerified {
		t.Fatalf("setelah rollback = %s", it.State)
	}
}
