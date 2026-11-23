# Rencana Implementasi: Layar Bersih dan Warna di Wizard CLI

> **Untuk agent pelaksana:** wajib memakai superpowers:subagent-driven-development atau superpowers:executing-plans, tugas per tugas. Langkah memakai kotak centang (`- [ ]`).

**Tujuan:** wizard membersihkan layar tiap pindah menu, menunggu ENTER setelah hasil tindakan, dan memberi warna semantik pada judul, sukses, bahaya, peringatan, serta status tabel.

**Arsitektur:** satu file baru `internal/cli/style.go` berisi deteksi terminal, fungsi warna, sel status, pembersih layar, dan jeda. `App` menyimpan tiga flag (`colorOut`, `colorErr`, `interactive`) yang dihitung sekali di `newApp`. Semua titik cetak memakai fungsi itu secara eksplisit. Windows mengaktifkan mode VT di `cmd/migrator/console_windows.go`.

**Teknologi:** Go 1.26+, `golang.org/x/term`, `github.com/mattn/go-isatty` (sudah di `go.sum`), `text/tabwriter`.

**Spesifikasi:** [`docs/superpowers/specs/2026-11-23-cli-layar-warna-design.md`](../specs/2026-11-23-cli-layar-warna-design.md)

## Batasan Global

- Tanpa komentar di kode (`//`, `/* */`). Pengecualian hanya direktif build Go yang benar-benar perlu.
- Teks baru tanpa em dash.
- Kode ANSI: judul `\x1b[1;94m`, sukses `\x1b[92m`, bahaya `\x1b[91m`, peringatan `\x1b[93m`, netral sel tabel `\x1b[39m`, reset `\x1b[0m`.
- Pembersih layar: `\x1b[H\x1b[2J\x1b[3J`.
- Teks jeda persis: `Tekan ENTER untuk kembali ke menu.`
- Warna hanya bila output `*os.File` dan terminal (`term.IsTerminal` atau `isatty.IsCygwinTerminal`) dan `ANSIDisabled == false`. Bersih layar dan jeda hanya bila stdin dan stdout terminal.
- Pesan dan prompt yang sudah ada tidak diubah teksnya, kecuali tata letak `account list`.
- Commit bahasa Indonesia, identitas `suwahadi`, tanpa anotasi AI. Push ke `main` hanya di Tugas 4.

## Fokus Review

1. Wizard diberi input dari pipe/script (stdin bukan terminal): tidak ada jeda yang memakan baris input. Dipatok `TestWizardPipedInputHasNoClearOrPause` (Tugas 2).
2. Sub-menu gagal (error dari store) lalu kembali ke menu utama: pesan ERROR harus sempat terbaca. Dipatok `TestFinishPausesOnSubmenuError` (Tugas 1).
3. Input berakhir (EOF) tepat saat jeda: wizard keluar bersih, bukan error. Dipatok `TestPauseEOFEndsWizard` (Tugas 2).
4. Output dialihkan ke file (`migrator status > hasil.txt`): tidak ada kode ANSI. Dipatok `TestPaintOffForNonTerminal` dengan file sementara (Tugas 1).
5. Menu 6 tanpa tindakan manual: pesan "Tidak ada tindakan manual yang menunggu." harus terbaca sebelum kembali ke menu utama. Dipatok `TestManualMenuEmptyPauses` (Tugas 2).

---

### Tugas 1: Inti warna dan deteksi terminal

**File:**
- Buat: `internal/cli/style.go`
- Buat: `internal/cli/style_test.go`
- Ubah: `internal/cli/app.go` (field `App`, `newApp`)
- Ubah: `internal/cli/prompt.go` (field `errColor`, PERINGATAN kuning)
- Ubah: `cmd/migrator/console_windows.go` (mode VT)
- Ubah: `go.mod` (`go mod tidy` memindahkan go-isatty ke dependency langsung)

**Antarmuka yang dihasilkan:**
- `var ANSIDisabled bool`
- `type tone int` dengan konstanta `toneNone`, `toneTitle`, `toneOK`, `toneDanger`, `toneWarn`
- `func isTerminal(v any) bool`
- `func paint(on bool, t tone, s string) string`
- `func statusTone(s string) tone`
- `func (a *App) color(t tone, s string) string`
- `func (a *App) say(t tone, s string)`
- `func (a *App) statusCell(s string) string`
- `func (a *App) printError(msg string)` (meredaksi `msg` lewat `a.redactor`, mencetak `ERROR: ...` ke stderr)
- `func (a *App) clearScreen()`
- `func (a *App) pause() error`
- `func (a *App) finish(err error, pause bool) error` (menggantikan `wizardAction`)
- Field `App`: `colorOut`, `colorErr`, `interactive bool`. Field `Prompter`: `errColor bool`.

- [ ] **Langkah 1: Tulis test yang gagal** di `internal/cli/style_test.go`

```go
func TestPaintOffForNonTerminal(t *testing.T) {
	a := newApp(Env{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if a.colorOut || a.colorErr || a.interactive { t.Fatal("buffer bukan terminal") }
	if a.color(toneOK, "x") != "x" || a.statusCell("AKTIF") != "AKTIF" { t.Fatal("tidak boleh ada kode ANSI") }
	f, _ := os.CreateTemp(t.TempDir(), "out")
	defer f.Close()
	if isTerminal(f) { t.Fatal("file biasa bukan terminal") }
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
		if got := a.statusCell(in); got != "\x1b["+code+"m"+in+"\x1b[0m" { t.Errorf("%s: %q", in, got) }
	}
}

func TestStatusColumnAligned(t *testing.T)
```
`TestStatusColumnAligned`: tulis tabel `tabwriter` (padding 2) berisi baris `a\tAKTIF\tx`, `bbbb\tDITOLAK\ty`, `cc\tBACKUP_RUNNING\tz` dengan `colorOut=true` dan `false`; setelah kode `\x1b\[[0-9;]*m` dibuang, kedua hasil harus sama persis.

```go
func TestFinishPausesOnSubmenuError(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("\n"), Stdout: out, Stderr: errb})
	a.interactive = true
	if err := a.finish(errors.New("rusak"), false); err != nil { t.Fatal(err) }
	if !strings.Contains(errb.String(), "ERROR: rusak") || !strings.Contains(out.String(), "Tekan ENTER untuk kembali ke menu.") { t.Fatal(out.String(), errb.String()) }
	if err := a.finish(nil, false); err != nil { t.Fatal("tanpa error dan tanpa jeda tidak boleh membaca input") }
}
```

- [ ] **Langkah 2: Jalankan test, pastikan gagal**

Jalankan: `go test ./internal/cli -run 'TestPaintOff|TestStatusCell|TestStatusColumn|TestFinish' -v`
Harapan: gagal kompilasi (`newApp` belum punya field, fungsi belum ada).

- [ ] **Langkah 3: Implementasi `style.go`**

- `paint`: bila `on`, bungkus `s` dengan kode tone + reset; `toneTitle` = `1;94`, `toneNone` = `39`.
- `statusTone`: pecah `strings.TrimSpace(s)` dengan `strings.FieldsFunc` pada `/`, spasi, `(`, `)`. Prioritas merah > kuning > hijau > netral.
  - Merah: `DITOLAK`, `ERROR`, `UNSUPPORTED`, `SSH_GAGAL`, `BLOCKED`, `FAILED`, `FAIL`
  - Kuning: `BELUM_DIUJI`, `UNKNOWN`, `MANUAL`, `WAITING_MANUAL`, `SSL_PENDING`, `WARN`, `DUMMY_ONLY`
  - Hijau: `AKTIF`, `SUPPORTED`, `SSH_READY`, `VERIFIED`, `PASS`, `SSL_ACTIVE`, `CUTOVER_DONE`, `EMAIL_DONE`, `NO_EMAIL`, `CLEANUP_READY`, `CLEANED`, `COMPLETED`
- `statusCell`: selalu membungkus (termasuk netral `39`) bila `colorOut`, agar setiap sel kolom berukuran byte sama.
- `isTerminal`: `*os.File` saja; `!ANSIDisabled && (term.IsTerminal(int(fd)) || isatty.IsCygwinTerminal(fd))`.
- `pause`: bila `!interactive` kembalikan `nil`; selain itu `a.prompt.Line("Tekan ENTER untuk kembali ke menu. ")`.
- `finish`: `ErrInputClosed` diteruskan; error lain dicetak lewat `printError`; jeda bila `pause || err != nil`.
- `newApp`: `colorOut = isTerminal(env.Stdout)`, `colorErr = isTerminal(env.Stderr)`, `interactive = colorOut && isTerminal(env.Stdin)`, `prompt.errColor = colorErr`.
- `prompt.go`: baris `PERINGATAN: terminal ini ...` dibungkus `paint(p.errColor, toneWarn, ...)`.
- `app.go`: peringatan `config.CloudSyncWarning` dibungkus `paint(a.colorErr, toneWarn, ...)`.

- [ ] **Langkah 4: Mode VT di Windows** (`cmd/migrator/console_windows.go`)

Di `init`, setelah set code page: aktifkan `windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING` untuk `STD_OUTPUT_HANDLE` dan `STD_ERROR_HANDLE`. Bila `GetConsoleMode` gagal, handle bukan konsol (pipe mintty atau file) dan dibiarkan. Bila `SetConsoleMode` gagal, set `cli.ANSIDisabled = true`.

- [ ] **Langkah 5: `go mod tidy`, jalankan test, pastikan lulus**

Jalankan: `go mod tidy && go test ./internal/cli -v`
Harapan: semua lulus. `go.mod` memuat `github.com/mattn/go-isatty v0.0.24` tanpa `// indirect`.

- [ ] **Langkah 6: Commit**

```bash
git add go.mod go.sum cmd/migrator/console_windows.go internal/cli/style.go internal/cli/style_test.go internal/cli/app.go internal/cli/prompt.go
git commit -m "CLI: inti warna, deteksi terminal, mode VT Windows"
```

---

### Tugas 2: Alur layar wizard

**File:**
- Ubah: `internal/cli/wizard.go` (`runWizard`, `tokenMenu`, `sshMenu`, `banner`, hapus `wizardAction`)
- Ubah: `internal/cli/wizard_menus.go` (`manualMenu`, `emailMenu`, `workerMenu`)
- Test: `internal/cli/style_test.go`

**Antarmuka yang dipakai:** `clearScreen`, `pause`, `finish`, `say`, `color` dari Tugas 1.

Pola setiap loop menu: tampilkan menu → baca pilihan → `0` (atau ENTER di sub-menu yang sudah menerimanya) kembali → `clearScreen` → jalankan → `finish(err, pause)` → `clearScreen` → ulang.
- Menu utama: `pause=true` untuk 3, 4, 5, 7, 9, 10; `pause=false` untuk sub-menu 1, 2, 6, 8, 11.
- Semua pilihan di dalam sub-menu: `pause=true`.
- Pilihan tidak dikenal: `say(toneWarn, "Pilihan tidak dikenal.")` lalu `finish(nil, true)`.
- `runWizard` memanggil `clearScreen` lalu `banner` sekali sebelum loop.
- Judul menu dicetak `say(toneTitle, ...)`. Banner: baris `INFORMASI PROGRAM` biru tebal.
- `manualMenu`: layar dibersihkan di awal tiap putaran sebelum daftar. Antara memilih nomor dan prompt `Yakin ... (y/T)` **tidak** dibersihkan (daftar tetap terbaca). Setelah hasil konfirmasi, setelah `R` (password), dan pada pilihan tidak dikenal: jeda. Daftar kosong: cetak pesannya lalu `pause` sebelum `return`.
- Prompt konfirmasi manual `Yakin %s untuk %s sudah selesai dikerjakan? (y/T): ` hijau, pesan `%s untuk %s dikonfirmasi` hijau, ERROR lewat `printError`.

- [ ] **Langkah 1: Tulis test yang gagal**

```go
func TestWizardInvalidChoiceInteractive(t *testing.T) {
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("x\n\n0\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	a.interactive, a.colorOut = true, true
	if err := a.runWizard(context.Background()); err != nil { t.Fatal(err) }
	s := out.String()
	if strings.Count(s, "\x1b[H\x1b[2J\x1b[3J") < 3 { t.Fatal("layar harus dibersihkan di awal, setelah pilihan, dan setelah jeda") }
	for _, want := range []string{"Tekan ENTER untuk kembali ke menu.", "\x1b[93mPilihan tidak dikenal.\x1b[0m", "\x1b[1;94mMENU UTAMA"} {
		if !strings.Contains(s, want) { t.Errorf("tidak memuat %q", want) }
	}
}

func TestWizardPipedInputHasNoClearOrPause(t *testing.T) {
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("x\n0\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	if err := a.runWizard(context.Background()); err != nil { t.Fatal(err) }
	if strings.Contains(out.String(), "\x1b[") || strings.Contains(out.String(), "Tekan ENTER untuk kembali ke menu.") { t.Fatal(out.String()) }
}

func TestPauseEOFEndsWizard(t *testing.T) {
	a := newApp(Env{Stdin: strings.NewReader("x\n"), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	a.interactive = true
	if err := a.runWizard(context.Background()); !errors.Is(err, ErrInputClosed) { t.Fatal(err) }
}

func TestManualMenuEmptyPauses(t *testing.T)
```
`TestManualMenuEmptyPauses`: `a.store` dari `store.Open(ctx, filepath.Join(t.TempDir(), "m.db"))` (tutup di akhir), `a.interactive = true`, stdin `"\n"`. `manualMenu` mengembalikan `nil`, output memuat `Tidak ada tindakan manual yang menunggu.` dan `Tekan ENTER untuk kembali ke menu.`

- [ ] **Langkah 2: Jalankan test, pastikan gagal**

Jalankan: `go test ./internal/cli -run 'TestWizard|TestPauseEOF|TestManualMenu' -v`
Harapan: `TestWizardInvalidChoiceInteractive` dan `TestManualMenuEmptyPauses` gagal (belum ada bersih layar/jeda).

- [ ] **Langkah 3: Implementasi alur di `wizard.go` dan `wizard_menus.go` sesuai pola di atas**

- [ ] **Langkah 4: Jalankan test, pastikan lulus**

Jalankan: `go test ./internal/cli -v`
Harapan: semua lulus.

- [ ] **Langkah 5: Commit**

```bash
git add internal/cli/wizard.go internal/cli/wizard_menus.go internal/cli/style_test.go
git commit -m "Wizard: layar bersih tiap pindah menu dan jeda ENTER setelah hasil"
```

---

### Tugas 3: Warna pesan, prompt, status tabel, dan tata letak `account list`

**File:**
- Ubah: `internal/cli/account.go`, `audit.go`, `ssh.go`, `run.go`, `plan.go`, `cleanup.go`, `cutover.go`, `email.go`, `inventory.go`, `root.go`
- Test: `internal/cli/style_test.go`

**Antarmuka yang dipakai:** `color`, `say`, `statusCell`, `printError` dari Tugas 1.
**Antarmuka yang dihasilkan:** `func (a *App) writeAccountList(items []account.Listing)` (murni, tanpa store; `accountList` memanggilnya).

Pemetaan per file:

| File | Hijau | Merah | Kuning | Biru tebal | Sel status |
|---|---|---|---|---|---|
| `account.go` | `OK`, `Token %q tersimpan ...` | `GAGAL`, prompt `Ketik %s untuk konfirmasi` (hapus token), `Token %q dihapus ...`, saran `Token %q DITOLAK ...` | | | status alias |
| `audit.go` | | | | | kolom STATUS |
| `ssh.go` | `OK` | `GAGAL` | prompt `Ketik ULANG ... LEWATI` | | kolom STATUS (2 baris) |
| `run.go` | | | | | kolom STATUS |
| `plan.go` | `DRY-RUN selesai ...`, prompt `Ketik YAKIN`, `Batch %s tersimpan ...` | `RENCANA DITOLAK ...` (2) | prompt `Ketik LANJUT`, baris daftar peringatan planner | `RINGKASAN RENCANA`, `URUTAN LANGKAH ...` | |
| `cleanup.go` | | prompt `Ketik HAPUS`, judul `Lolos gerbang (... DIHAPUS ...)`, `Sumber %s (DUMMY_ONLY) dihapus.` | | | |
| `cutover.go` | `Cutover disetujui ...` | prompt `Ketik ROLLBACK` | prompt `Ketik CUTOVER` | | |
| `email.go` | `Pemindahan email disetujui ...` | prompt `Ketik HAPUS EMAIL` | | | |

Semua `fmt.Fprintln(a.env.Stderr, "ERROR: "+a.redactor.String(x))` di `audit.go`, `cleanup.go`, `inventory.go`, `plan.go`, `root.go` diganti `a.printError(x)`.

Tata letak `writeAccountList` (contoh dengan `colorOut=false`):
```
sumber  AKTIF    diuji 2026-11-23 02:39 UTC
  u111  Premium                1 web
  u222  hostinger_business_v3  46 web
lama    DITOLAK  belum diuji
  tidak ada akun hosting
```
- Alias dipadatkan ke panjang alias terpanjang, status ke panjang status terpanjang **sebelum** diwarnai, dipisah 2 spasi.
- `LastCheckedAt` RFC3339 ditampilkan `2006-01-02 15:04 UTC`; kosong = `belum diuji`; gagal parse = nilai mentah.
- Akun: satu `tabwriter` per alias, baris `  %s\t%s\t%d web`, paket lewat `PlanLabel`.
- Saran untuk token `DITOLAK` tetap dicetak setelah daftar.

- [ ] **Langkah 1: Tulis test yang gagal**

```go
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
	for i, w := range want {
		if got := strings.Fields(lines[i]); !reflect.DeepEqual(got, w) { t.Errorf("baris %d: %q", i, got) }
	}
	if strings.Index(lines[0], "AKTIF") != strings.Index(lines[3], "DITOLAK") { t.Error("kolom status harus rata") }
}
```

- [ ] **Langkah 2: Jalankan test, pastikan gagal**

Jalankan: `go test ./internal/cli -run TestAccountList -v`
Harapan: gagal kompilasi (`writeAccountList` belum ada).

- [ ] **Langkah 3: Implementasi pemetaan warna dan `writeAccountList`**

- [ ] **Langkah 4: Jalankan pemeriksaan**

Jalankan: `gofmt -l . && go vet ./... && go test ./internal/... 2>&1 | grep -E '^(ok|FAIL)'`
Harapan: `gofmt` kosong, `vet` bersih, `internal/cli` dan paket yang berjalan di Windows (lihat D-050) `ok`.

- [ ] **Langkah 5: Commit**

```bash
git add internal/cli
git commit -m "CLI: warna pesan, prompt konfirmasi, status tabel; account list satu akun per baris"
```

---

### Tugas 4: Dokumen, rilis rc4, verifikasi visual

**File:**
- Ubah: `docs/decisions.md` (D-052), `README.md` (versi 3 tempat + satu kalimat tentang warna/layar di bagian 2), `CLAUDE.md` (status Fase 8)
- Ubah: `dist/migrator.exe`, `dist/SHA256SUMS`

- [ ] **Langkah 1: D-052** berisi: go-isatty dipakai langsung untuk mintty; aturan warna/layar (terminal saja, kode terang + angka kontras, VT Windows, `ANSIDisabled`); `account list` satu akun per baris.

- [ ] **Langkah 2: Cek tanpa komentar dan em dash**

Jalankan: `grep -rnE '^\s*(//|/\*)' --include='*.go' . | grep -vE '//go:(build|embed|generate)'` dan `grep -rn '—' internal/cli/style.go internal/cli/style_test.go`
Harapan: keduanya kosong.

- [ ] **Langkah 3: Build rc4**

Jalankan: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X hostinger-migrator/internal/cli.Version=1.0.0-rc4" -o dist/migrator.exe ./cmd/migrator && (cd dist && sha256sum -t migrator.exe > SHA256SUMS && sha256sum -c SHA256SUMS)`
Harapan: `migrator.exe: OK`.

- [ ] **Langkah 4: Verifikasi visual read-only**

Salin `dist/migrator.exe`, `dist/config.yaml`, `dist/data` ke folder sementara. Buka jendela konsol Windows nyata di folder itu, kirim tombol (menu 1 → 2 → ENTER → 0 → 5 → ENTER → x → ENTER → 0), ambil tangkapan layar di tiap layar. Juga `account test <alias>` (GET saja). Periksa: layar bersih tiap pindah, warna judul/status/peringatan, kolom rata, `account list` satu akun per baris. Tidak ada operasi tulis ke Hostinger, `dist/data` tidak disentuh.

- [ ] **Langkah 5: Commit dan push**

```bash
git add docs README.md CLAUDE.md dist/migrator.exe dist/SHA256SUMS
git commit -m "Rilis 1.0.0-rc4: layar bersih dan warna di wizard"
git push origin main
```
