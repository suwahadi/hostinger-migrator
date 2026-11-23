package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"text/tabwriter"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/store"
)

func TestPaintOffForNonTerminal(t *testing.T) {
	a := newApp(Env{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if a.colorOut || a.colorErr || a.interactive {
		t.Fatal("buffer bukan terminal")
	}
	if a.color(toneOK, "x") != "x" || a.statusCell("AKTIF") != "AKTIF" {
		t.Fatal("tidak boleh ada kode ANSI")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("file biasa bukan terminal")
	}
}

func TestStatusCell(t *testing.T) {
	a := newApp(Env{Stdout: &bytes.Buffer{}})
	a.colorOut = true
	cases := map[string]string{
		"AKTIF": "92", "DITOLAK": "91", "BELUM_DIUJI": "93", "SUPPORTED": "92", "UNKNOWN": "93",
		"MANUAL": "93", "UNSUPPORTED": "91", "SSH_READY": "92", "SSH_GAGAL": "91", "BACKUP_RUNNING": "39",
		"VERIFIED/WAITING_MANUAL": "93", "RESTORE_RUNNING/FAILED": "91", "COMPLETED": "92",
		"BACKUP_RUNNING (percobaan 2)": "39",
	}
	for in, code := range cases {
		if got := a.statusCell(in); got != "\x1b["+code+"m"+in+"\x1b[0m" {
			t.Errorf("%s: %q", in, got)
		}
	}
}

var ansiCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestStatusColumnAligned(t *testing.T) {
	render := func(color bool) string {
		out := &bytes.Buffer{}
		a := newApp(Env{Stdout: out})
		a.colorOut = color
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, r := range [][2]string{{"NAMA", "STATUS"}, {"a", "AKTIF"}, {"bbbb", "DITOLAK"}, {"cc", "BACKUP_RUNNING"}} {
			fmt.Fprintf(tw, "%s\t%s\tx\n", r[0], a.statusCell(r[1]))
		}
		tw.Flush()
		return out.String()
	}
	plain, colored := render(false), render(true)
	if colored == plain {
		t.Fatal("versi berwarna harus memuat kode ANSI")
	}
	if got := ansiCode.ReplaceAllString(colored, ""); got != plain {
		t.Fatalf("kolom bergeser:\n%s\nvs\n%s", got, plain)
	}
}

func TestFinishPausesOnSubmenuError(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("\n"), Stdout: out, Stderr: errb})
	a.interactive = true
	if err := a.finish(errors.New("rusak"), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errb.String(), "ERROR: rusak") || !strings.Contains(out.String(), "Tekan ENTER untuk kembali ke menu.") {
		t.Fatal(out.String(), errb.String())
	}
	if err := a.finish(nil, false); err != nil {
		t.Fatal("tanpa error dan tanpa jeda tidak boleh membaca input")
	}
}

func TestWizardInvalidChoiceInteractive(t *testing.T) {
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("x\n\n0\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	a.interactive, a.colorOut = true, true
	if err := a.runWizard(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Count(s, "\x1b[H\x1b[2J\x1b[3J") < 3 {
		t.Fatal("layar harus dibersihkan di awal, setelah pilihan, dan setelah jeda")
	}
	for _, want := range []string{"Tekan ENTER untuk kembali ke menu.", "\x1b[93mPilihan tidak dikenal.\x1b[0m", "\x1b[1;94mMENU UTAMA"} {
		if !strings.Contains(s, want) {
			t.Errorf("tidak memuat %q", want)
		}
	}
}

func TestWizardPipedInputHasNoClearOrPause(t *testing.T) {
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("x\n0\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	if err := a.runWizard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") || strings.Contains(out.String(), "Tekan ENTER untuk kembali ke menu.") {
		t.Fatal(out.String())
	}
}

func TestPauseEOFEndsWizard(t *testing.T) {
	a := newApp(Env{Stdin: strings.NewReader("x\n"), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	a.interactive = true
	if err := a.runWizard(context.Background()); !errors.Is(err, ErrInputClosed) {
		t.Fatal(err)
	}
}

func TestManualMenuEmptyPauses(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	a.store = st
	a.interactive = true
	if err := a.manualMenu(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tidak ada tindakan manual yang menunggu.", "Tekan ENTER untuk kembali ke menu."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tidak memuat %q", want)
		}
	}
}

func TestAccountListOneAccountPerLine(t *testing.T) {
	out := &bytes.Buffer{}
	a := newApp(Env{Stdout: out})
	a.writeAccountList([]account.Listing{
		{Credential: store.Credential{Alias: "sumber", LastStatus: "AKTIF", LastCheckedAt: "2026-11-23T02:39:03Z"},
			Accounts: []store.HostingAccount{{ID: "u111", Plan: "hostinger_premium", WebsiteCount: 1}, {ID: "u222", Plan: "hostinger_business_v3", WebsiteCount: 46}}},
		{Credential: store.Credential{Alias: "lama", LastStatus: "DITOLAK"}},
	})
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	want := [][]string{
		{"sumber", "AKTIF", "diuji", "2026-11-23", "02:39", "UTC"},
		{"u111", "Premium", "1", "web"},
		{"u222", "hostinger_business_v3", "46", "web"},
		{"lama", "DITOLAK", "belum", "diuji"},
		{"tidak", "ada", "akun", "hosting"},
	}
	if len(lines) != len(want) {
		t.Fatalf("jumlah baris %d:\n%s", len(lines), out.String())
	}
	for i, w := range want {
		if got := strings.Fields(lines[i]); !reflect.DeepEqual(got, w) {
			t.Errorf("baris %d: %q", i, got)
		}
	}
	if strings.Index(lines[0], "AKTIF") != strings.Index(lines[3], "DITOLAK") {
		t.Error("kolom status harus rata")
	}
}

func TestStartupWarningVisibleAfterClear(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "OneDrive")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("0\n"), Stdout: out, Stderr: out})
	a.configPath = filepath.Join(dir, "config.yaml")
	a.wizardMode, a.interactive, a.colorOut = true, true, true
	if err := a.open(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if err := a.runWizard(ctx); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	warn := strings.Index(s, "sinkronisasi cloud")
	if warn < 0 || strings.LastIndex(s, ansiClear) > warn {
		t.Fatalf("peringatan cloud-sync harus tampil setelah layar dibersihkan:\n%q", s)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addTestAccount(t *testing.T, st *store.Store, alias, account string) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertCredential(ctx, tx, alias); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceHostingAccounts(ctx, tx, alias, []store.HostingAccount{{ID: account, Alias: alias, Plan: "hostinger_premium"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryTitleAndWarningColored(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	addTestAccount(t, st, "xyz", "u111")
	if err := st.SaveInventory(ctx, &store.InventorySnapshot{AccountID: "u111", Alias: "xyz", Warnings: []string{"domain mati.com expired"}}); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	a := newApp(Env{Stdout: out, Stderr: &bytes.Buffer{}})
	a.store, a.colorOut = st, true
	if err := a.runInventory(ctx, "xyz", inventoryOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\x1b[1;94mINVENTARIS — token xyz\x1b[0m", "\x1b[93m    PERINGATAN: domain mati.com expired\x1b[0m"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tidak memuat %q:\n%s", want, out.String())
		}
	}
}

func TestChooseAliasInvalidChoiceYellow(t *testing.T) {
	st := openTestStore(t)
	addTestAccount(t, st, "xyz", "u111")
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("zz\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	a.store, a.colorOut = st, true
	if _, err := a.chooseAlias(context.Background(), "sumber"); !errors.Is(err, ErrInputClosed) {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[93mPilihan tidak dikenal.\x1b[0m") {
		t.Fatal(out.String())
	}
}

func TestPickRollbackInvalidChoiceYellow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := st.CreateBatch(ctx, store.Batch{SourceAlias: "a", SourceAccount: "u1", DestAlias: "b", DestAccount: "u2"}, []store.Item{{SourceDomain: "a.com", State: "CUTOVER_DONE"}}); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("9\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	a.store, a.colorOut = st, true
	if d, err := a.pickRollback(ctx); err != nil || d != "" {
		t.Fatal(d, err)
	}
	if !strings.Contains(out.String(), "\x1b[93mPilihan tidak dikenal.\x1b[0m") {
		t.Fatal(out.String())
	}
}

func TestAskInventoryOptions(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	addTestAccount(t, st, "xyz", "u111")
	addTestAccount(t, st, "abc", "u222")
	type want struct {
		opt inventoryOptions
		ok  bool
	}
	cases := map[string]want{
		"\n":          {inventoryOptions{}, true},
		"t\ny\n":      {inventoryOptions{}, true},
		"y\n\n\n":     {inventoryOptions{Refresh: true}, true},
		"y\n\ny\n":    {inventoryOptions{Refresh: true, Quick: true}, true},
		"Y\n2\nY\n":   {inventoryOptions{Refresh: true, Account: "u111", Quick: true}, true},
		"y\nu111\n\n": {inventoryOptions{Refresh: true, Account: "u111"}, true},
		"y\nu999\n":   {inventoryOptions{Refresh: true}, false},
	}
	for in, w := range cases {
		out := &bytes.Buffer{}
		a := newApp(Env{Stdin: strings.NewReader(in), Stdout: out, Stderr: &bytes.Buffer{}})
		a.store = st
		got, ok, err := a.askInventory(ctx)
		if err != nil || got != w.opt || ok != w.ok {
			t.Errorf("input %q = %+v %v %v, harus %+v %v", in, got, ok, err, w.opt, w.ok)
		}
		if strings.HasPrefix(strings.ToLower(in), "y") && !strings.Contains(out.String(), "2. u111") {
			t.Errorf("daftar akun tidak tampil: %s", out.String())
		}
	}
}
