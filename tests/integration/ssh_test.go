package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"hostinger-migrator/internal/cli"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/sshtest"
)

const sshPassword = "Sandi-SSH-Uji-4321"

func newSSHServer(t *testing.T) *sshtest.Server {
	t.Helper()
	srv, err := sshtest.New("u494983133")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		switch {
		case cmd == "echo ok; whoami; pwd":
			return sshtest.ExecResult{Stdout: "ok\nu494983133\n/home/u494983133\n"}
		case strings.HasPrefix(cmd, "for c in"):
			return sshtest.ExecResult{Stdout: "tar=ada\ngzip=ada\nmysqldump=ada\nmysql=ada\nsha256sum=ada\nwp=tidak\n"}
		case strings.HasPrefix(cmd, "df -P"):
			return sshtest.ExecResult{Stdout: "/dev/sda1 107374182400 1073741824 106300440576 1% /home\n/dev/sda1 600000 12000 588000 2% /home\n"}
		}
		return sshtest.ExecResult{Exit: 127}
	}
	return srv
}

func (h *harness) addToken() {
	h.t.Helper()
	if code := h.run(goodToken+"\n", "account", "add", "--alias", "xyz", "--token-stdin"); code != cli.ExitOK {
		h.t.Fatalf("add gagal: %s", h.stderr.String())
	}
}

func (h *harness) authorizeMigratorKey(srv *sshtest.Server) {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, "data", "ssh", "id_ed25519.pub"))
	if err != nil {
		h.t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		h.t.Fatal(err)
	}
	srv.Authorize(pub)
}

func answers(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestSSHSetupTestAndAudit(t *testing.T) {
	h := newHarness(t)
	srv := newSSHServer(t)
	h.addToken()
	port := strconv.Itoa(srv.Port())
	var outputs []string

	code := h.run(answers("LEWATI"), "ssh", "setup", "u494983133")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitAction || !strings.Contains(h.stdout.String(), "SSH key baru dibuat") {
		t.Fatalf("setup pertama (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"AKTIFKAN SSH — akun u494983133 (Premium, token: xyz)", "hPanel → Advanced → SSH Access", "Pastikan status SSH: Enabled", "ssh-ed25519 AAAA", " hostinger-migrator"} {
		if !strings.Contains(out, want) {
			t.Fatalf("instruksi tidak memuat %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "PRIVATE KEY") {
		t.Fatal("private key tidak boleh ditampilkan")
	}
	h.authorizeMigratorKey(srv)

	code = h.run(answers("SELESAI", srv.Host(), "abc", port, ""), "ssh", "setup", "u494983133")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitOK {
		t.Fatalf("setup (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	out = h.stdout.String()
	for _, want := range []string{"Memakai SSH key yang sudah ada", "Port harus angka", "Username SSH [u494983133]", "OK", "whoami u494983133 · home /home/u494983133 · SFTP OK · login dengan SSH key", "Fingerprint server SHA256:", "Status: SSH_READY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output setup tidak memuat %q:\n%s", want, out)
		}
	}

	code = h.run("", "ssh", "test", "u494983133")
	if code != cli.ExitOK || !strings.Contains(h.stdout.String(), "SSH_READY") {
		t.Fatalf("ssh test (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	code = h.run("", "ssh", "test")
	if code != cli.ExitAction || !strings.Contains(h.stdout.String(), "u597522494") {
		t.Fatalf("akun tanpa SSH harus membuat exit 2 (%d): %s", code, h.stdout.String())
	}

	code = h.run("", "audit", "xyz")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitOK {
		t.Fatalf("audit (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	out = h.stdout.String()
	for _, want := range []string{"AUDIT KAPABILITAS — token xyz", "Akun u494983133 (Premium)", "API_LIST_WEBSITES", "API_CREATE_WEBSITE", "SSH_TOOLS", "QUOTA", "Akun u597522494 (Single)", "UNKNOWN = belum terbukti"} {
		if !strings.Contains(out, want) {
			t.Fatalf("audit tidak memuat %q:\n%s", want, out)
		}
	}
	section := out[strings.Index(out, "Akun u494983133"):strings.Index(out, "Akun u597522494")]
	assertRow(t, section, "SSH ", "SUPPORTED")
	assertRow(t, section, "SSH_TOOLS", "SUPPORTED")
	assertRow(t, section, "API_CREATE_WEBSITE", "UNKNOWN")
	assertRow(t, section, "API_CHANGE_DOMAIN", "MANUAL")
	assertRow(t, section, "API_EMAIL_RESTORE", "UNSUPPORTED")

	if err := srv.RotateHostKey(); err != nil {
		t.Fatal(err)
	}
	code = h.run("", "ssh", "test", "u494983133")
	if code != cli.ExitAction || !strings.Contains(h.stdout.String(), "BLOCKED") || !strings.Contains(h.stdout.String(), "Fingerprint server SSH berubah") {
		t.Fatalf("fingerprint berubah (%d): %s", code, h.stdout.String())
	}
	code = h.run(answers("SELESAI", "", "", ""), "ssh", "setup", "u494983133")
	if code != cli.ExitAction || !strings.Contains(h.stdout.String(), "FINGERPRINT_BERUBAH") {
		t.Fatalf("setup tanpa --terima-host-baru harus tetap BLOCKED (%d): %s", code, h.stdout.String())
	}
	code = h.run(answers("SELESAI", "", "", ""), "ssh", "setup", "u494983133", "--terima-host-baru")
	if code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Status: SSH_READY") {
		t.Fatalf("terima host baru (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	h.assertNoLeak(goodToken, outputs...)
}

func assertRow(t *testing.T, section, code, status string) {
	t.Helper()
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, code) {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == status {
				return
			}
			t.Fatalf("baris %s: %q, harus %s", code, line, status)
		}
	}
	t.Fatalf("baris %s tidak ditemukan:\n%s", code, section)
}

func TestSSHPasswordFallback(t *testing.T) {
	h := newHarness(t)
	srv := newSSHServer(t)
	srv.SetPassword(sshPassword)
	h.addToken()
	port := strconv.Itoa(srv.Port())
	var outputs []string

	code := h.run(answers("SELESAI", srv.Host(), port, "", "LEWATI"), "ssh", "setup", "u494983133")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitAction || !strings.Contains(h.stdout.String(), "Login SSH ditolak") {
		t.Fatalf("key belum ditempel harus gagal autentikasi (%d): %s", code, h.stdout.String())
	}
	code = h.run(answers("PASSWORD", "", "", "", sshPassword), "ssh", "setup", "u494983133")
	outputs = append(outputs, h.stdout.String(), h.stderr.String())
	if code != cli.ExitOK || !strings.Contains(h.stdout.String(), "login dengan password") {
		t.Fatalf("fallback password (%d): %s%s", code, h.stdout.String(), h.stderr.String())
	}
	if _, err := h.backend.Get("hostinger-migrator", "ssh-password:u494983133"); err != nil {
		t.Fatal("password SSH harus tersimpan di Credential Manager")
	}
	code = h.run("", "ssh", "remove-password", "u494983133")
	if code != cli.ExitOK || !strings.Contains(h.stdout.String(), "sebelumnya login dengan password") {
		t.Fatalf("remove-password (%d): %s", code, h.stdout.String())
	}
	if _, err := h.backend.Get("hostinger-migrator", "ssh-password:u494983133"); err == nil {
		t.Fatal("password SSH harus terhapus")
	}
	h.assertNoLeak(sshPassword, outputs...)
}

func TestSSHSetupUnknownAccountAndNoAccounts(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", "ssh", "setup"); code != cli.ExitAction {
		t.Fatalf("tanpa akun harus exit 2, dapat %d", code)
	}
	h.addToken()
	if code := h.run("", "ssh", "setup", "u000"); code != cli.ExitAction || !strings.Contains(h.stderr.String(), "u494983133") {
		t.Fatalf("akun tidak dikenal (%d): %s", code, h.stderr.String())
	}
}

func TestAuditRevokedTokenExit2(t *testing.T) {
	h := newHarness(t)
	h.addToken()
	h.srv.Revoke(goodToken)
	code := h.run("", "audit")
	if code != cli.ExitAction {
		t.Fatalf("audit token dicabut harus exit 2, dapat %d: %s", code, h.stderr.String())
	}
	h.run("", "account", "list")
	if !strings.Contains(h.stdout.String(), store.CredentialRejected) {
		t.Fatalf("status token harus DITOLAK:\n%s", h.stdout.String())
	}
	h.assertNoLeak(goodToken, h.stdout.String())
}
