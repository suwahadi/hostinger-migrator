# Rencana Implementasi: Hapus Pemeriksaan Kapasitas Tujuan

> **Untuk agent pelaksana:** wajib memakai superpowers:subagent-driven-development atau superpowers:executing-plans, tugas per tugas. Langkah memakai kotak centang (`- [ ]`).

**Tujuan:** planner tidak lagi memeriksa, menampilkan, atau menolak rencana karena `KUOTA_WEBSITE`, `DISK_TUJUAN`, `KUOTA_DATABASE`; layar akun tujuan tanpa label kuota dan tanpa panggilan API per akun.

**Arsitektur:** penghapusan di `internal/planner/planner.go` (blok kuota, `diskCheck`, `dbQuotaCheck`, tipe `Quota`, field `Planner.Quotas` dan `Plan.DestQuota`, konstanta `DiskFactor`). Daftar website tujuan untuk `BENTROK_DOMAIN` tetap dibaca lewat API dengan fungsi yang diperkecil. Config tetap menerima `kuota_paket` demi `config.yaml` lama.

**Teknologi:** Go 1.26+, tanpa dependency baru.

**Spesifikasi:** [`docs/superpowers/specs/2026-11-24-hapus-cek-kapasitas-design.md`](../specs/2026-11-24-hapus-cek-kapasitas-design.md)

## Batasan Global

- Tanpa komentar di kode. Teks baru bahasa Indonesia tanpa em dash.
- `config.Config.KuotaPaket` (kunci `kuota_paket`) tetap ada di struktur dan validasinya, tetapi tidak dipakai kode lain.
- Pemeriksaan lain di planner tidak berubah.
- Commit bahasa Indonesia, identitas `suwahadi`, tanpa anotasi AI. Push hanya setelah operator setuju.

## Fokus Review

1. `config.yaml` lama yang masih memuat `kuota_paket` tetap terbaca. Dipatok kasus override di `TestKuotaPaketDefaultAndOverride` (Tugas 1).
2. Akun tujuan dengan paket kecil dan disk `df` kecil: rencana diterima, tanpa `LANJUT` karena kapasitas. Dipatok `TestPlanIgnoresCapacity` dan `TestPlanDryRunIgnoresCapacity` (Tugas 1).
3. Wizard menu 4 yang sebelumnya selalu meminta `LANJUT` (peringatan kuota database tak diketahui) kini mungkin tidak meminta `LANJUT`; skrip input `TestPlanInteractiveWizard` harus mengikuti. Dipatok test itu sendiri (Tugas 1).
4. Bentrok domain di akun tujuan tetap terdeteksi walau pembacaan kuota hilang. Dipatok `TestPlanDomainConflict` yang sudah ada (Tugas 1, tetap lulus).
5. Batch lama yang menyimpan `DestQuota` di `PlanJSON`: aman karena `PlanJSON` hanya ditulis, tidak dibaca ulang ke `Plan` (diperiksa, tanpa test baru).

---

### Tugas 1: Hapus pemeriksaan kapasitas dan label kuota

**File:**
- Ubah: `internal/planner/planner.go`, `internal/planner/planner_test.go`
- Ubah: `internal/cli/plan.go` (`planner()` baris 49, `chooseDestAccount` describe)
- Ubah: `tests/testenv/env.go:438`
- Ubah: `configs/default.yaml` (hapus blok `kuota_paket`), `internal/config/config_test.go`
- Ubah: `tests/integration/plan_test.go`

**Antarmuka yang dihasilkan:**
- `func (pl *Planner) destSites(ctx context.Context, alias string, acc store.HostingAccount) ([]hostinger.Website, error)` menggantikan `DestQuota` (hanya daftar website tujuan, untuk `BENTROK_DOMAIN`).
- `planner.Planner` tanpa field `Quotas`; `planner.Plan` tanpa field `DestQuota`.
- Label akun tujuan: `(%s) · %d web` dengan `PlanLabel(acc.Plan)` dan `acc.WebsiteCount` dari store.

- [ ] **Langkah 1: Tulis test yang gagal**

`internal/planner/planner_test.go`:
- `TestPlanIgnoresCapacity` (menggantikan `TestPlanRejectedWhenQuotaInsufficient`, `TestPlanUnknownPlanQuotaIsWarning`, `TestPlanDiskInsufficient`): tujuan paket `hostinger_single` dengan 1 website terpakai dan `QUOTA` `disk_sisa_byte` 1048576; hasil `p.Rejected()` false, dan tidak ada check berkode `KUOTA_WEBSITE`, `DISK_TUJUAN`, `KUOTA_DATABASE`.
- `TestPlanHappyPathAndSave`: hapus asersi tiga kode kapasitas.

`tests/integration/plan_test.go`:
- `TestPlanDryRunIgnoresCapacity` (menggantikan `TestPlanDryRunRejectedWhenQuotaInsufficient`): `newPlanHarness(t, "hostinger_single")`, perintah yang sama; exit `cli.ExitOK`; output memuat `RINGKASAN RENCANA`, `DRY-RUN selesai`; tidak memuat `KUOTA_WEBSITE`, `DISK_TUJUAN`, `KUOTA_DATABASE`, `RENCANA DITOLAK`; tidak ada batch dan tidak ada tulis ke Hostinger.
- `TestPlanInteractiveWizard`: harapan `1. u73839202  (Business) — sisa kuota website: 99` menjadi `1. u73839202  (Business) · 1 web`; buang input `LANJUT` bila tidak ada peringatan lain (lihat output).

`internal/config/config_test.go` `TestKuotaPaketDefaultAndOverride`: bawaan `KuotaPaket` kosong; override dari file tetap terbaca; nilai negatif tetap ditolak.

- [ ] **Langkah 2: Jalankan, pastikan gagal**

Jalankan: `go test ./internal/planner ./internal/config && go test ./tests/integration -run 'TestPlan'`
Harapan: `TestPlanIgnoresCapacity`, `TestPlanDryRunIgnoresCapacity`, `TestKuotaPaketDefaultAndOverride`, `TestPlanInteractiveWizard` gagal.

- [ ] **Langkah 3: Implementasi penghapusan sesuai Antarmuka**

- [ ] **Langkah 4: Jalankan pemeriksaan**

Jalankan: `test -z "$(gofmt -l .)" && go vet ./... && GOOS=linux go vet ./... && go test ./internal/planner ./internal/config ./internal/cli && go test ./tests/integration -run '^(TestTokenNeverStoredInDBOrLogs|TestRemoveRequiresConfirmation|TestWizardFlow|TestWizardStopsOnEOF|TestHelpAndErrorsInIndonesian|TestVersionNeedsNoConfig|TestDefaultConfigCreatedOnFirstRun|TestSSHSetupTestAndAudit|TestSSHPasswordFallback|TestSSHSetupUnknownAccountAndNoAccounts|TestAuditRevokedTokenExit2|TestInventoryCommand|TestPlanDryRunIgnoresCapacity|TestPlanDryRunThenSave|TestPlanInteractiveWizard|TestPlanDomainConflictBlocked)$'`
Harapan: semua lulus (16 test integrasi).

- [ ] **Langkah 5: Commit**

```bash
git add internal/planner internal/cli/plan.go internal/config/config_test.go configs/default.yaml tests docs/superpowers
git commit -m "Planner: hapus pemeriksaan kapasitas tujuan (kuota website, disk, database)"
```

---

### Tugas 2: Dokumen dan rilis rc8

**File:** `docs/decisions.md` (D-056), `README.md` (versi 3 tempat; bagian config tanpa `kuota_paket`; sebutan pemeriksaan kuota/disk di alur), `docs/uji-lapangan.md` (B3, B4), `CLAUDE.md` (status Fase 8), `dist/migrator.exe`, `dist/SHA256SUMS`

- [ ] **Langkah 1: D-056**: tiga pemeriksaan kapasitas dihapus atas keputusan operator; menggantikan aturan kapasitas di PRD 8.6, D-011, D-025, D-029; `kuota_paket` diterima tetapi tidak dipakai; label akun tujuan tanpa kuota; konsekuensi kegagalan saat migrasi berjalan dan `retry`.
- [ ] **Langkah 2: `docs/uji-lapangan.md`**: B3 menjadi "`plan --dry-run` ke akun tujuan paket kecil atau disk hampir penuh → rencana diterima, tanpa pemeriksaan kapasitas"; B4 menjadi "Migrasi ke akun yang kuotanya benar-benar penuh → item FAILED atau checkpoint manual, lalu `retry` setelah kapasitas ditambah".
- [ ] **Langkah 3: Cek komentar dan em dash, build rc8**

Jalankan: `grep -rnE '^\s*(//|/\*)' --include='*.go' . | grep -vE '//go:(build|embed|generate)'` lalu `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X hostinger-migrator/internal/cli.Version=1.0.0-rc8" -o dist/migrator.exe ./cmd/migrator && (cd dist && sha256sum -t migrator.exe > SHA256SUMS && sha256sum -c SHA256SUMS)`
Harapan: grep kosong, `migrator.exe: OK`.

- [ ] **Langkah 4: Commit lokal**

```bash
git add docs README.md CLAUDE.md dist/migrator.exe dist/SHA256SUMS
git commit -m "Rilis 1.0.0-rc8: tanpa pemeriksaan kapasitas tujuan di rencana"
```
