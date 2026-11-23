package integration

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostinger-migrator/internal/cli"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/sshtest"
)

const (
	destToken  = "FAKE-integrasi-tujuan-eeee5555ffff6666"
	wpPassword = "FAKE-integrasi-wp-pass-9999"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func probeFor(unit string) string {
	switch unit {
	case "/home/u494983133/domains/domainanda.com":
		wp := "<?php\ndefine('DB_NAME', 'u494983133_wp');\ndefine('DB_USER', 'u494983133_wp');\ndefine('DB_PASSWORD', '" + wpPassword + "');\ndefine('DB_HOST', 'localhost');\n"
		return "SIZE=1288490188\nFILES=5321\nROOT public_html/\nPUB wp-config.php\nPUB wp-content/\nPUB wp-includes/\nFILE public_html/wp-config.php\n" + b64(wp) + "\n"
	case "/home/u494983133/domains/toko.id":
		return "SIZE=891289600\nFILES=12000\nROOT artisan\nROOT composer.json\nROOT public_html/\nPUB index.php\nFILE composer.json\n" + b64(`{"require":{"laravel/framework":"^11"}}`) + "\n"
	}
	return "ERR=tidak_ada\n"
}

func sshServerFor(t *testing.T, user string) *sshtest.Server {
	t.Helper()
	srv, err := sshtest.New(user)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		switch {
		case cmd == "echo ok; whoami; pwd":
			return sshtest.ExecResult{Stdout: "ok\n" + user + "\n/home/" + user + "\n"}
		case strings.HasPrefix(cmd, "cd '"):
			unit := strings.TrimPrefix(cmd, "cd '")
			out := probeFor(unit[:strings.Index(unit, "'")])
			if !strings.Contains(cmd, "SIZE=") {
				var keep []string
				for _, line := range strings.Split(out, "\n") {
					if !strings.HasPrefix(line, "SIZE=") && !strings.HasPrefix(line, "FILES=") {
						keep = append(keep, line)
					}
				}
				out = strings.Join(keep, "\n")
			}
			return sshtest.ExecResult{Stdout: out}
		case strings.HasPrefix(cmd, "for c in"):
			return sshtest.ExecResult{Stdout: "tar=ada\ngzip=ada\nmysqldump=ada\nmysql=ada\nsha256sum=ada\nwp=ada\n"}
		case strings.HasPrefix(cmd, "df -P"):
			return sshtest.ExecResult{Stdout: "/dev/sda1 107374182400 1073741824 106300440576 1% /home\n/dev/sda1 600000 12000 588000 2% /home\n"}
		}
		return sshtest.ExecResult{Exit: 127}
	}
	return srv
}

func (h *harness) setupSSH(account string, srv *sshtest.Server) {
	h.t.Helper()
	h.run(answers("LEWATI"), "ssh", "setup", account)
	h.authorizeMigratorKey(srv)
	code := h.run(answers("SELESAI", srv.Host(), strconv.Itoa(srv.Port()), ""), "ssh", "setup", account)
	if code != cli.ExitOK {
		h.t.Fatalf("ssh setup %s (%d): %s%s", account, code, h.stdout.String(), h.stderr.String())
	}
}

func newPlanHarness(t *testing.T, destPlan string) *harness {
	t.Helper()
	h := newHarness(t)
	h.srv.Allow(destToken)
	h.srv.OrdersFor[destToken] = []hostinger.Order{{ID: 201, Status: "active", Plan: hostinger.OrderPlan{Name: destPlan}}}
	h.srv.WebsitesFor[destToken] = []hostinger.Website{{Domain: "lama1.com", Username: "u73839202", OrderID: 201, VhostType: "main", IsEnabled: true}}
	h.addToken()
	if code := h.run(destToken+"\n", "account", "add", "--alias", "zyx", "--token-stdin"); code != cli.ExitOK {
		t.Fatalf("add tujuan: %s", h.stderr.String())
	}
	h.setupSSH("u494983133", sshServerFor(t, "u494983133"))
	h.setupSSH("u73839202", sshServerFor(t, "u73839202"))
	if code := h.run("", "audit", "zyx"); code != cli.ExitOK {
		t.Fatalf("audit tujuan: %s%s", h.stdout.String(), h.stderr.String())
	}
	return h
}

func (h *harness) batches() []store.Batch {
	h.t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(h.dir, "data", "migrator.db"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer st.Close()
	bs, _ := st.ListBatches(context.Background())
	return bs
}

func TestInventoryCommand(t *testing.T) {
	h := newPlanHarness(t, "hostinger_business")
	code := h.run("", "inventory", "xyz")
	out := h.stdout.String()
	if code != cli.ExitOK {
		t.Fatalf("inventory (%d): %s%s", code, out, h.stderr.String())
	}
	for _, want := range []string{
		"Memindai inventaris token xyz (read-only)",
		"INVENTARIS — token xyz",
		"[1] u494983133  (Premium) · SSH SSH_READY",
		"/home/u494983133/domains/domainanda.com  WordPress · 1,2 GB · DB 1 · Mailbox 2 · PHP 8.2",
		"subdomain/parked ikut folder ini: blog.domainanda.com",
		"database: u494983133_wp (< 1 MB, public_html/wp-config.php)",
		"database: u494983133_wp",
		"/home/u494983133/domains/toko.id",
		"Laravel · 850,0 MB",
		"[NS EKSTERNAL]",
		"[2] u597522494  (Single) · SSH BELUM_DIATUR",
		"/home/u597522494/domains/contoh.com",
		"Statis · ?",
		"[EXPIRED]",
		"Cron job: 1",
		"PERINGATAN: SSH akun u597522494 berstatus BELUM_DIATUR",
		"Akun u494983133: 2 website ditemukan, dipindai 2 sekaligus.",
		"/2] domainanda.com  WordPress  1,2 GB",
		"Pemindaian token xyz selesai dalam",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inventaris tidak memuat %q", want)
		}
	}
	if t.Failed() {
		t.Log(out)
	}
	h.run("", "inventory", "xyz")
	if strings.Contains(h.stdout.String(), "Memindai") {
		t.Fatal("tanpa --refresh inventaris tersimpan harus dipakai ulang")
	}
	h.run("", "inventory", "xyz", "--refresh")
	if !strings.Contains(h.stdout.String(), "Memindai") {
		t.Fatal("--refresh harus memindai ulang")
	}
	h.run("", "inventory", "xyz", "--refresh", "--cepat")
	if quick := h.stdout.String(); !strings.Contains(quick, "scan cepat tanpa hitung ukuran") || !strings.Contains(quick, "WordPress · ? ·") {
		t.Fatalf("--cepat harus melewati hitung ukuran:\n%s", quick)
	}
	if h.srv.Writes() != 0 {
		t.Fatalf("inventaris mengirim %d request tulis", h.srv.Writes())
	}
	h.assertNoLeak(wpPassword, out, h.stderr.String())
	h.assertNoLeak(goodToken, out)
}

func TestInventoryResumeAndSingleAccount(t *testing.T) {
	h := newPlanHarness(t, "hostinger_business")
	if code := h.run("", "inventory", "xyz"); code != cli.ExitOK {
		t.Fatalf("inventory: %s%s", h.stdout.String(), h.stderr.String())
	}
	st, err := store.Open(context.Background(), filepath.Join(h.dir, "data", "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DELETE FROM inventory_runs WHERE account_id = ?`, "u597522494"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if code := h.run("", "inventory", "xyz"); code != cli.ExitOK {
		t.Fatalf("lanjutan: %s%s", h.stdout.String(), h.stderr.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "Melanjutkan inventaris token xyz (read-only): 1 akun belum dipindai, 1 akun sudah ada dan dilewati (u494983133).") {
		t.Fatalf("pesan lanjutan tidak tampil:\n%s", out)
	}
	if strings.Contains(out, "Akun u494983133:") || !strings.Contains(out, "Akun u597522494: 1 website ditemukan") {
		t.Fatalf("akun yang sudah dipindai tidak boleh diulang:\n%s", out)
	}
	if !strings.Contains(out, "[1] u494983133") || !strings.Contains(out, "[2] u597522494") {
		t.Fatalf("inventaris lengkap harus tetap tampil:\n%s", out)
	}

	h.run("", "inventory", "xyz")
	if strings.Contains(h.stdout.String(), "Memindai") || strings.Contains(h.stdout.String(), "Melanjutkan") {
		t.Fatal("setelah lengkap tidak boleh memindai lagi")
	}

	if code := h.run("", "inventory", "--akun", "u494983133"); code != cli.ExitOK {
		t.Fatalf("--akun: %s%s", h.stdout.String(), h.stderr.String())
	}
	out = h.stdout.String()
	if !strings.Contains(out, "Memindai ulang akun u494983133 dari token xyz (read-only)") || !strings.Contains(out, "Akun u494983133: 2 website ditemukan") || strings.Contains(out, "Akun u597522494:") {
		t.Fatalf("--akun harus memindai satu akun saja:\n%s", out)
	}
	if strings.Contains(out, "INVENTARIS — token zyx") {
		t.Fatalf("--akun hanya menampilkan token pemilik akun:\n%s", out)
	}

	if code := h.run("", "inventory", "--akun", "u000"); code != cli.ExitAction || !strings.Contains(h.stderr.String(), "akun hosting u000 tidak terdaftar") {
		t.Fatalf("akun tidak dikenal harus ditolak (%d): %s", code, h.stderr.String())
	}
	if h.srv.Writes() != 0 {
		t.Fatalf("inventaris mengirim %d request tulis", h.srv.Writes())
	}
}

func TestPlanDryRunRejectedWhenQuotaInsufficient(t *testing.T) {
	h := newPlanHarness(t, "hostinger_single")
	code := h.run("", "plan", "--dry-run", "--sumber", "xyz", "--web", "domainanda.com,toko.id", "--tujuan", "zyx", "--akun-tujuan", "u73839202")
	out := h.stdout.String()
	if code != cli.ExitAction {
		t.Fatalf("kuota kurang harus exit 2, dapat %d\n%s%s", code, out, h.stderr.String())
	}
	for _, want := range []string{"RINGKASAN RENCANA", "Tujuan : zyx / u73839202 (Single)", "[GAGAL]      KUOTA_WEBSITE", "Sisa kuota website tujuan 0 (kuota 1, terpakai 1), dibutuhkan 2", "URUTAN LANGKAH", "RENCANA DITOLAK (dry-run)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output tidak memuat %q", want)
		}
	}
	if t.Failed() {
		t.Log(out)
	}
	if len(h.batches()) != 0 || h.srv.Writes() != 0 {
		t.Fatal("dry-run tidak boleh menyimpan batch atau menulis ke Hostinger")
	}
}

func TestPlanDryRunThenSave(t *testing.T) {
	h := newPlanHarness(t, "hostinger_business")
	args := []string{"plan", "--sumber", "xyz", "--akun-sumber", "u494983133", "--web", "domainanda.com,toko.id", "--tujuan", "zyx", "--akun-tujuan", "u73839202"}
	code := h.run("", append(args, "--dry-run")...)
	out := h.stdout.String()
	if code != cli.ExitOK {
		t.Fatalf("dry-run (%d): %s%s", code, out, h.stderr.String())
	}
	for _, want := range []string{
		"Sumber : xyz / u494983133 (2 website)",
		"Perkiraan backup:",
		"Database: 1 · Mailbox: 2",
		"[OK]         KUOTA_WEBSITE",
		"Sisa kuota website tujuan 99, dibutuhkan 2",
		"[OK]         DISK_TUJUAN",
		"[OK]         SSH_TUJUAN",
		"[PERINGATAN] KUOTA_DATABASE",
		"tar seluruh folder /home/u494983133/domains/domainanda.com",
		"header Host: domainanda.com",
		"Ubah DNS di nameserver eksternal: checkpoint manual",
		"DRY-RUN selesai",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run tidak memuat %q", want)
		}
	}
	if t.Failed() {
		t.Log(out)
	}
	if len(h.batches()) != 0 {
		t.Fatal("dry-run tidak boleh menyimpan batch")
	}
	code = h.run(answers("LANJUT", "tidak"), args...)
	if code != cli.ExitOK || !strings.Contains(h.stdout.String(), "Dibatalkan") || len(h.batches()) != 0 {
		t.Fatalf("tanpa YAKIN harus batal (%d): %s", code, h.stdout.String())
	}
	code = h.run(answers("LANJUT", "YAKIN"), args...)
	out = h.stdout.String()
	if code != cli.ExitOK || !strings.Contains(out, "Ketik YAKIN") || !strings.Contains(out, "tersimpan dengan status PLANNED (2 website)") {
		t.Fatalf("simpan (%d): %s%s", code, out, h.stderr.String())
	}
	if h.spawned != 1 {
		t.Fatalf("worker harus dimulai setelah YAKIN, dipanggil %d kali", h.spawned)
	}
	bs := h.batches()
	if len(bs) != 1 || !strings.HasPrefix(bs[0].ID, "MIG-") || bs[0].SourceAccount != "u494983133" || bs[0].DestAccount != "u73839202" {
		t.Fatalf("batch = %+v", bs)
	}
	if h.srv.Writes() != 0 {
		t.Fatal("membuat rencana tidak boleh menulis ke Hostinger")
	}
}

func TestPlanInteractiveWizard(t *testing.T) {
	h := newPlanHarness(t, "hostinger_business")
	input := answers(
		"4",
		"1",
		"9.9",
		"A",
		"A1",
		"1.1,2.1",
		"",
		"2",
		"1",
		"LANJUT",
		"YAKIN",
		"0",
	)
	code := h.run(input)
	out := h.stdout.String()
	if code != cli.ExitOK {
		t.Fatalf("wizard (%d): %s%s", code, out, h.stderr.String())
	}
	for _, want := range []string{
		"Token sumber:",
		"Ditemukan 2 akun di token xyz:",
		"[1] u494983133  (Premium)",
		"1. /home/u494983133/domains/domainanda.com",
		"Pilih: [nomor akun.nomor web, pisah koma] / [A] semua / [ENTER] lanjut",
		"Pilihan \"9.9\" tidak valid",
		"ketik A<nomor akun>",
		"Terpilih (u494983133): domainanda.com, toko.id",
		"Satu batch hanya untuk satu akun sumber",
		"Token tujuan:",
		"1. u73839202  (Business) — sisa kuota website: 99",
		"RINGKASAN RENCANA",
		"tersimpan dengan status PLANNED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("wizard tidak memuat %q", want)
		}
	}
	if t.Failed() {
		t.Log(out)
	}
	if len(h.batches()) != 1 {
		t.Fatal("batch harus tersimpan")
	}
}

func TestPlanDomainConflictBlocked(t *testing.T) {
	h := newPlanHarness(t, "hostinger_business")
	h.srv.WebsitesFor[destToken] = append(h.srv.WebsitesFor[destToken], hostinger.Website{Domain: "toko.id", Username: "u73839202", OrderID: 201, VhostType: "addon"})
	code := h.run("", "plan", "--dry-run", "--sumber", "xyz", "--web", "domainanda.com,toko.id", "--tujuan", "zyx", "--akun-tujuan", "u73839202")
	out := h.stdout.String()
	if code != cli.ExitOK || !strings.Contains(out, "toko.id  Laravel") || !strings.Contains(out, "BLOCKED: Domain toko.id sudah ada di akun tujuan; tidak akan ditimpa") {
		t.Fatalf("bentrok (%d): %s", code, out)
	}
}
