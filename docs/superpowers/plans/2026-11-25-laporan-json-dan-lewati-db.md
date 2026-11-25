# Rencana Implementasi: laporan.json Valid dan Opsi Lewati Database

> **Untuk agent pelaksana:** wajib memakai superpowers:subagent-driven-development atau superpowers:executing-plans, tugas per tugas. Langkah memakai kotak centang (`- [ ]`).

**Tujuan:** `laporan.json` selalu JSON valid; operator bisa melewati database tak terpakai lewat `confirm MANUAL_DB_SOURCE <domain> --nilai lewati`.

**Arsitektur:** pembantu `(*logx.Redactor).JSON` dipakai inventaris dan laporan. Nilai `lewati` disimpan di tugas manual yang sudah ada; `pipeline.SkippedDatabases` menjadi satu sumber kebenaran untuk load item, cleanup, dan laporan.

**Teknologi:** Go 1.26+, pustaka standar. Tanpa dependency baru, tanpa perubahan skema.

**Spesifikasi:** [`docs/superpowers/specs/2026-11-25-laporan-json-dan-lewati-db-design.md`](../specs/2026-11-25-laporan-json-dan-lewati-db-design.md)

## Batasan Global

- Tanpa komentar di kode. Teks baru bahasa Indonesia tanpa em dash.
- Database bersumber tidak diketahui = `Source` kosong atau `"api"` (sama dengan rc9).
- Rahasia tetap tersaring di semua keluaran.
- Commit bahasa Indonesia, identitas `suwahadi`, tanpa anotasi AI. Push setelah operator setuju.

## Fokus Review

1. `confirm MANUAL_DB_SOURCE <domain> --nilai Lewati` (huruf besar) diterima sebagai `lewati`; `--nilai hapus` ditolak. Dipatok `TestConfirmDBSourceValue` (Tugas 2).
2. Website dengan dua database (satu diketahui, satu `api`) yang dilewati: database yang diketahui tetap ikut backup. Dipatok `TestSkippedDatabases` (Tugas 2).
3. `confirm` tanpa nilai (setelah pindai ulang) tidak melewati apa pun. Dipatok `TestSkippedDatabases` (Tugas 2).
4. Instruksi menyebut kasus "sudah memakai SSH" hanya bila inventaris akun memakai SSH. Dipatok `TestBackupManualUnknownSource` (Tugas 2).
5. `laporan.json` dengan baris SSH key (kunci `token`) valid. Dipatok `TestReportJSONValid` (Tugas 1).

---

### Tugas 1: Pembantu JSON bersama dan laporan.json valid

**File:** `internal/logx/redact.go`, `internal/logx/redact_json_test.go` (baru), `internal/inventory/inventory.go` (hapus `redactJSONStrings`/`redactValue`, pakai pembantu baru), `internal/report/report.go` (`Builder.Write` baris 431-433), `internal/report/report_json_test.go` (baru)

**Antarmuka:** `func (r *Redactor) JSON(data []byte) ([]byte, error)`: perilaku sama dengan `redactJSONStrings` rc9 (decode `UseNumber`, saring nilai string rekursif, encode indentasi dua spasi). `Builder.Write` memakai `b.Redactor.JSON` bila `Redactor` tidak nil, selain itu `logx.Global.JSON`.

- [ ] **Langkah 1: Tulis test yang gagal**
  - `TestRedactorJSON`: `r.Add("rahasiaDB123")`; `r.JSON([]byte(`{"token":"sumber","n":9920251125,"c":"x rahasiaDB123"}`))` → `json.Valid`, memuat `"token": "sumber"`, `9920251125`, tidak memuat `rahasiaDB123`.
  - `TestReportJSONValid`: `(&Builder{Redactor: logx.NewRedactor()}).Write(&Report{...dengan satu baris SSH key berisi field token "sumber"...}, t.TempDir(), map[string]bool{"json": true})` → file `laporan.json` `json.Valid`.
- [ ] **Langkah 2:** `go test ./internal/logx -run TestRedactorJSON; go test ./internal/report -run TestReportJSONValid` → gagal (kompilasi / JSON tidak valid).
- [ ] **Langkah 3:** implementasi; `TestWriteJSONStaysValid` (inventaris) tetap lulus.
- [ ] **Langkah 4:** `go test ./internal/logx ./internal/inventory ./internal/report` → lulus.
- [ ] **Langkah 5: Commit** `Laporan: laporan.json disaring per nilai sehingga selalu JSON valid` (sertakan `docs/superpowers` spec dan rencana ini).

---

### Tugas 2: Opsi lewati database

**File:** `internal/pipeline/manual.go` (`Confirm`), `internal/pipeline/pipeline.go` (`load`, `RunBackup`, `backupManual`), `internal/pipeline/backup_manual_test.go`, `internal/planner/planner.go` (pesan `KREDENSIAL_DB`), `internal/cli/wizard_menus.go` (`manualMenu`), `internal/cli/cleanup.go` (daftar lolos gerbang), `internal/report/report.go` (`website` catatan)

**Antarmuka:**
- `func SkippedDatabases(ctx context.Context, st *store.Store, it *store.PipelineItem) ([]string, error)`: kosong kecuali tugas `MANUAL_DB_SOURCE` item berstatus confirmed dengan `Value` `lewati`; lalu nama database akun sumber batch (`GetBatch` → `ListDatabasesByAccount`) dengan `WebsiteDomain == it.SourceDomain` dan sumber tidak diketahui.
- `load()`: setelah mengisi `ic.Databases`, buang database yang namanya ada di `SkippedDatabases`.
- `Confirm`: untuk kode `MANUAL_DB_SOURCE`, nilai dipangkas; kosong diterima; `lewati` (tanpa membedakan huruf besar/kecil) disimpan sebagai `lewati` dan `AppendAudit(ctx, nil, "operator", "item.db_skipped", domain, "OK", <nama database dipisah koma>)`; nilai lain → error `nilai MANUAL_DB_SOURCE hanya boleh kosong atau 'lewati'`.
- `backupManual(err error, ic *ItemContext, inventoryUsedSSH bool) error`; `RunBackup` mengambil `SSHUsed` dari `GetInventoryRun(ctx, ic.Batch.SourceAccount, ic.Batch.SourceAlias)` (error → false). Teks:
  - baris 1 tetap;
  - baris 2: tanpa SSH = teks rc9; dengan SSH = `Inventaris akun %s sudah memakai SSH, tetapi tidak ada file konfigurasi website yang merujuk database ini. Kemungkinan database ini tidak dipakai (mis. sisa instalasi lama).`
  - langkah 1 dan 2 tetap; langkah 3 baru: `3. Bila database ini tidak dipakai website, lewati: migrator confirm MANUAL_DB_SOURCE %s --nilai lewati. Database yang dilewati tidak dipindah dan kemungkinan ikut terhapus saat cleanup website sumber.`
  - baris terakhir: `Bila website memakai database ini tetapi tindakan ini muncul lagi setelah pindai ulang, file konfigurasinya tidak dikenali tool; catat nama filenya dan laporkan ke pengembang.`
- Planner: pesan `KREDENSIAL_DB` ditambah ` Database yang tidak dipakai website bisa dilewati saat tindakan manual MANUAL_DB_SOURCE muncul.`
- Menu 6: untuk `MANUAL_DB_SOURCE`, isian opsional lewat `a.prompt.Line("Ketik lewati bila database tidak dipakai website, atau ENTER setelah pindai ulang: ")` (bukan `askDefault`, karena boleh kosong).
- Cleanup CLI: di bawah tiap website lolos gerbang yang `SkippedDatabases` tidak kosong: `a.say(toneDanger, "    database dilewati: <db> (kemungkinan ikut terhapus bersama website sumber)")`.
- Laporan: catatan website ditambah `database dilewati operator: <db>`.

- [ ] **Langkah 1: Tulis test yang gagal** di `internal/pipeline/backup_manual_test.go` (paket `pipeline`, memakai `store.Open` di folder sementara):
  - `TestConfirmDBSourceValue`: batch + item `a.com` + tugas manual `MANUAL_DB_SOURCE` terbuka; `Confirm(..., "hapus")` error; `Confirm(..., "Lewati")` sukses, tugas tersimpan dengan `Value == "lewati"`, audit memuat aksi `item.db_skipped`.
  - `TestSkippedDatabases`: akun sumber punya database `u1_ok` (sumber `.env`) dan `u1_x` (sumber `api`) untuk `a.com`; sebelum konfirmasi → kosong; setelah `Confirm` tanpa nilai → kosong; setelah `Confirm` `lewati` → `["u1_x"]`.
  - `TestBackupManualUnknownSource`: sesuaikan panggilan ke tanda tangan baru; dengan `inventoryUsedSSH` true instruksi memuat `sudah memakai SSH` dan `--nilai lewati`; dengan false memuat `dibuat sebelum SSH siap` dan `--nilai lewati`.
- [ ] **Langkah 2:** `go test ./internal/pipeline -run 'TestConfirmDBSourceValue|TestSkippedDatabases|TestBackupManualUnknownSource' -v` → gagal.
- [ ] **Langkah 3:** implementasi semua butir Antarmuka.
- [ ] **Langkah 4:** `test -z "$(gofmt -l .)" && go vet ./... && GOOS=linux go vet ./...`; `go test ./internal/pipeline -run 'TestConfirmDBSourceValue|TestSkippedDatabases|TestBackupManualUnknownSource'`; `go test ./internal/planner ./internal/cli ./internal/report ./internal/logx ./internal/inventory ./internal/backup`; 16 test integrasi Windows → semua lulus.
- [ ] **Langkah 5: Commit** `Opsi lewati database tak terpakai lewat confirm MANUAL_DB_SOURCE --nilai lewati`.

---

### Tugas 3: Dokumen dan rilis rc10

**File:** `docs/decisions.md` (D-058), `README.md` (versi 3 tempat; baris `MANUAL_DB_SOURCE` menyebut `--nilai lewati`), `CLAUDE.md` (status Fase 8), `dist/migrator.exe`, `dist/SHA256SUMS`

- [ ] **Langkah 1:** D-058: `laporan.json` disaring per nilai; opsi `lewati`, audit `item.db_skipped`, tampil di cleanup dan laporan, Hostinger kemungkinan menghapus database terhubung saat website sumber dihapus (belum teruji).
- [ ] **Langkah 2:** cek komentar kosong, build `1.0.0-rc10`, `sha256sum -c` OK.
- [ ] **Langkah 3: Commit lokal** `Rilis 1.0.0-rc10: laporan.json valid, opsi lewati database tak terpakai`.
