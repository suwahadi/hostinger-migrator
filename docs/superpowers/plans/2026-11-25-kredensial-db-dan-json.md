# Rencana Implementasi: Sumber Password Database dan JSON Inventaris

> **Untuk agent pelaksana:** wajib memakai superpowers:subagent-driven-development atau superpowers:executing-plans, tugas per tugas. Langkah memakai kotak centang (`- [ ]`).

**Tujuan:** item dengan sumber password database tak diketahui berhenti di tindakan manual `MANUAL_DB_SOURCE` (bukan diulang 8 kali), rencana memberi peringatan `KREDENSIAL_DB`, dan JSON inventaris tidak lagi rusak oleh penyaring rahasia.

**Arsitektur:** error bertanda `backup.ErrUnknownDBSource` dipetakan ke `ManualError` di pipeline; pemeriksaan baru di planner; nilai sepele diabaikan di `logx.Redactor.Add`, dan `writeJSON` menyaring per nilai string.

**Teknologi:** Go 1.26+, pustaka standar. Tanpa dependency baru.

**Spesifikasi:** [`docs/superpowers/specs/2026-11-25-kredensial-db-dan-json-design.md`](../specs/2026-11-25-kredensial-db-dan-json-design.md)

## Batasan Global

- Tanpa komentar di kode. Teks baru bahasa Indonesia tanpa em dash. Teks instruksi `MANUAL_DB_SOURCE` persis seperti spec bagian 4.
- Rahasia asli tetap tersaring di log, laporan, dan JSON (keamanan tidak boleh berkurang).
- Commit bahasa Indonesia, identitas `suwahadi`, tanpa anotasi AI. Push setelah operator setuju.

## Fokus Review

1. Website dengan dua database, satu sumber diketahui dan satu `api`: instruksi menyebut hanya database yang tidak diketahui. Dipatok `TestBackupManualUnknownSource` (Tugas 1).
2. Setelah `confirm` dan pindai ulang yang berhasil, backup jalan normal; bila masih tidak diketahui, tindakan dibuka ulang (perilaku runner yang sudah ada: `ReopenManualTask`). Tidak ada test baru; diperiksa reviewer.
3. Rencana dengan SSH sumber tidak siap tidak boleh memberi `KREDENSIAL_DB` (backup memakai `MANUAL_BACKUP`, tidak butuh password). Dipatok `TestPlanWarnsUnknownDBSource` (Tugas 2).
4. Password berupa angka (mis. `20251125`) yang kebetulan muncul di dalam angka ukuran JSON tidak merusak JSON. Dipatok `TestWriteJSONStaysValid` (Tugas 3).
5. Rahasia sungguhan (`rahasiaDB123`) di perintah cron tetap tersaring di JSON. Dipatok `TestWriteJSONStaysValid` (Tugas 3).

---

### Tugas 1: `MANUAL_DB_SOURCE` di worker

**File:**
- Ubah: `internal/backup/backup.go` (`ReadCredentials` baris 106-111, variabel error)
- Buat: `internal/backup/backup_test.go`
- Ubah: `internal/pipeline/pipeline.go` (`RunBackup` baris 574-596)
- Test: `internal/pipeline/pipeline_test.go`

**Antarmuka:**
- `var ErrUnknownDBSource = errors.New("sumber kredensial database tidak diketahui")` di `backup`; `ReadCredentials` mengembalikan `fmt.Errorf("%w: database %s; tidak bisa membuat dump otomatis", ErrUnknownDBSource, db.Name)`.
- `func backupManual(err error, ic *ItemContext) error` di `pipeline`: bila `errors.Is(err, backup.ErrUnknownDBSource)` kembalikan `&ManualError{Code: "MANUAL_DB_SOURCE", Instructions: ...}` (teks spec bagian 4; `<db>` = nama database di `ic.Databases` yang `Source` kosong atau `"api"`, dipisah koma; `<domain>` = `ic.Item.SourceDomain`; `<akun>` = `ic.Batch.SourceAccount`); selain itu kembalikan `err` apa adanya. `RunBackup` membungkus hasil `r.Backup.Run(...)` dengan fungsi ini.

- [ ] **Langkah 1: Tulis test yang gagal**

`internal/backup/backup_test.go` `TestReadCredentialsUnknownSource`: `(&Engine{}).ReadCredentials(ctx, nil, &store.Website{UnitPath: "/x"}, []store.DatabaseRow{{Name: "u1_db", Source: "api"}})` → `errors.Is(err, ErrUnknownDBSource)` dan pesan memuat `u1_db`; sama untuk `Source: ""`.

`internal/pipeline/pipeline_test.go` `TestBackupManualUnknownSource`: `ic := &ItemContext{Item: &store.PipelineItem{SourceDomain: "a.com"}, Batch: &store.Batch{SourceAccount: "u874"}, Databases: []store.DatabaseRow{{Name: "u874_ok", Source: "public_html/wp-config.php"}, {Name: "u874_x", Source: "api"}}}`; `backupManual(fmt.Errorf("%w: x", backup.ErrUnknownDBSource), ic)` → `*ManualError` dengan `Code == "MANUAL_DB_SOURCE"`, `Instructions` memuat `u874_x`, `a.com`, `migrator inventory --akun u874 --refresh`, `migrator confirm MANUAL_DB_SOURCE a.com`, dan tidak memuat `u874_ok`. `backupManual(errors.New("lain"), ic)` mengembalikan error yang sama.

- [ ] **Langkah 2: Jalankan, pastikan gagal**

Jalankan: `go test ./internal/backup -run TestReadCredentialsUnknownSource -v; go test ./internal/pipeline -run TestBackupManualUnknownSource -v`
Harapan: gagal kompilasi (`ErrUnknownDBSource`, `backupManual` belum ada).

- [ ] **Langkah 3: Implementasi**

- [ ] **Langkah 4: Jalankan test, pastikan lulus**

Jalankan: perintah Langkah 2.
Harapan: kedua test PASS (test pipeline lain yang memakai server SSH tiruan boleh gagal di Windows, D-050).

- [ ] **Langkah 5: Commit**

```bash
git add internal/backup internal/pipeline docs/superpowers
git commit -m "Worker: sumber password database tak diketahui menjadi tindakan manual MANUAL_DB_SOURCE"
```

---

### Tugas 2: Peringatan `KREDENSIAL_DB` di rencana

**File:** `internal/planner/planner.go` (setelah blok `SSH_SUMBER`, baris 308-320), `internal/planner/planner_test.go`

**Antarmuka:** pemeriksaan kode `KREDENSIAL_DB`, status `CheckWarn`, hanya bila `srcSSH.Status == store.SSHReady` dan ada item tidak `Blocked` yang punya database dengan `Source` kosong atau `"api"`. Pesan:
`Sumber password database belum diketahui: <domain> (<db>), ... . Jalankan 'migrator inventory --akun <akun sumber> --refresh' sebelum migrasi; tanpa itu backup berhenti di tindakan manual MANUAL_DB_SOURCE.`

- [ ] **Langkah 1: Tulis test yang gagal** `TestPlanWarnsUnknownDBSource`

Fixture `hostinger_business`; set `Source` database `u494983133_toko` ke `"api"` (lewat inventaris fixture). Build `req("domainanda.com", "toko.id")`: `check(p, "KREDENSIAL_DB").Status == CheckWarn`, pesan memuat `toko.id (u494983133_toko)` dan `--akun u494983133 --refresh`, rencana tidak ditolak. Lalu SSH sumber diubah ke `SSH_GAGAL`: `check(p, "KREDENSIAL_DB").Code == ""`.

Jalankan juga `TestPlanHappyPathAndSave` dan pastikan fixture lain tidak memicu peringatan bila semua `Source` diketahui (sesuaikan fixture bila database fixture belum punya `Source`).

- [ ] **Langkah 2: Jalankan, pastikan gagal**

Jalankan: `go test ./internal/planner -run TestPlanWarnsUnknownDBSource -v`
Harapan: FAIL (`KREDENSIAL_DB` belum ada).

- [ ] **Langkah 3: Implementasi**

- [ ] **Langkah 4: Jalankan pemeriksaan**

Jalankan: `go test ./internal/planner && go test ./tests/integration -run 'TestPlan' -v`
Harapan: lulus. Bila test integrasi rencana kini meminta `LANJUT`, periksa apakah harness memang punya database bersumber `api`; sesuaikan skrip input hanya bila peringatan itu benar.

- [ ] **Langkah 5: Commit**

```bash
git add internal/planner tests/integration
git commit -m "Rencana: peringatan KREDENSIAL_DB untuk database yang sumber password-nya belum diketahui"
```

---

### Tugas 3: JSON inventaris tidak rusak oleh penyaring

**File:** `internal/logx/redact.go` (`Add`), `internal/logx/logx_test.go`, `internal/inventory/inventory.go` (`writeJSON` baris 728-733), `internal/inventory/inventory_test.go`

**Antarmuka:**
- `Redactor.Add` mengabaikan nilai yang (setelah `TrimSpace`, tanpa membedakan huruf besar/kecil) sama dengan `true`, `false`, `null`, `none`, atau diawali `${`.
- `func redactJSONStrings(data []byte, r *logx.Redactor) ([]byte, error)` di `inventory`: decode dengan `json.Decoder.UseNumber`, saring hanya nilai string secara rekursif, encode ulang dengan indentasi dua spasi. `writeJSON` memakai fungsi ini menggantikan penyaringan teks mentah. Urutan kunci menjadi alfabetis.

- [ ] **Langkah 1: Tulis test yang gagal**

`logx_test.go` `TestRedactorIgnoresLiterals`: `r := NewRedactor()`; `Add("false")`, `Add("NULL")`, `Add("${PUSHER_KEY}")`, `Add("rahasiaDB123")`; `r.String("a=false b=null c=${PUSHER_KEY} d=rahasiaDB123")` == `"a=false b=null c=${PUSHER_KEY} d=[DISEMBUNYIKAN]"`.

`inventory_test.go` `TestWriteJSONStaysValid`: `r := logx.NewRedactor()`; `r.Add("20251125")`; `r.Add("rahasiaDB123")`; `Scanner{DataDir: t.TempDir(), Redactor: r}.writeJSON(&store.InventorySnapshot{Alias: "x", AccountID: "u1", Websites: []store.Website{{Domain: "a.com", SizeBytes: 9920251125}}, Cron: []store.CronRow{{Schedule: "* * * * *", Command: "mysqldump -prahasiaDB123 db"}}})`; file hasil `json.Valid`, memuat `9920251125`, tidak memuat `rahasiaDB123`, memuat `[DISEMBUNYIKAN]`.

- [ ] **Langkah 2: Jalankan, pastikan gagal**

Jalankan: `go test ./internal/logx -run TestRedactorIgnoresLiterals -v; go test ./internal/inventory -run TestWriteJSONStaysValid -v`
Harapan: keduanya FAIL.

- [ ] **Langkah 3: Implementasi**

- [ ] **Langkah 4: Jalankan pemeriksaan**

Jalankan: `test -z "$(gofmt -l .)" && go vet ./... && GOOS=linux go vet ./... && go test ./internal/logx ./internal/inventory ./internal/planner ./internal/backup ./internal/cli && go test ./internal/pipeline -run TestBackupManualUnknownSource && go test ./tests/integration -run '^(TestTokenNeverStoredInDBOrLogs|TestRemoveRequiresConfirmation|TestWizardFlow|TestWizardStopsOnEOF|TestHelpAndErrorsInIndonesian|TestVersionNeedsNoConfig|TestDefaultConfigCreatedOnFirstRun|TestSSHSetupTestAndAudit|TestSSHPasswordFallback|TestSSHSetupUnknownAccountAndNoAccounts|TestAuditRevokedTokenExit2|TestInventoryCommand|TestPlanDryRunIgnoresCapacity|TestPlanDryRunThenSave|TestPlanInteractiveWizard|TestPlanDomainConflictBlocked)$'`
Harapan: semua lulus.

- [ ] **Langkah 5: Commit**

```bash
git add internal/logx internal/inventory
git commit -m "Inventaris: JSON tidak lagi rusak oleh penyaring rahasia; nilai sepele tidak dianggap rahasia"
```

---

### Tugas 4: Dokumen dan rilis rc9

**File:** `docs/decisions.md` (D-057), `README.md` (versi 3 tempat; baris `MANUAL_DB_SOURCE` di tabel tindakan manual), `CLAUDE.md` (status Fase 8), `dist/migrator.exe`, `dist/SHA256SUMS`

- [ ] **Langkah 1:** D-057 merangkum ketiga perbaikan dan bahwa JSON lama yang rusak dibuat ulang saat akun dipindai ulang.
- [ ] **Langkah 2:** cek komentar (`grep -rnE '^\s*(//|/\*)' --include='*.go' . | grep -vE '//go:(build|embed|generate)'` kosong), build `1.0.0-rc9`, `sha256sum -c` OK.
- [ ] **Langkah 3: Commit lokal**

```bash
git add docs README.md CLAUDE.md dist/migrator.exe dist/SHA256SUMS
git commit -m "Rilis 1.0.0-rc9: MANUAL_DB_SOURCE, peringatan KREDENSIAL_DB, JSON inventaris valid"
```
