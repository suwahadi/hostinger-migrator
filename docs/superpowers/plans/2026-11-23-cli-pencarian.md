# Rencana Implementasi: Pencarian di Layar Pilihan Wizard

> **Untuk agent pelaksana:** wajib memakai superpowers:subagent-driven-development atau superpowers:executing-plans, tugas per tugas. Langkah memakai kotak centang (`- [ ]`).

**Tujuan:** layar pilih website sumber (menu 4) dan layar pilih akun (akun tujuan, menu 3, menu 2 SSH) bisa dicari berdasarkan sebagian domain atau id akun.

**Arsitektur:** fungsi pencocokan murni (tanpa store) di file baru `internal/cli/search.go`, dipakai oleh `selectWebsites` yang ditulis ulang dan oleh satu pemilih akun bersama `chooseAccountFrom`. Ketiga layar akun memanggil pemilih bersama itu dengan teks prompt dan fungsi deskripsi baris masing-masing.

**Teknologi:** Go 1.26+, pustaka standar (`strings`, `strconv`). Tanpa dependency baru.

**Spesifikasi:** [`docs/superpowers/specs/2026-11-23-cli-pencarian-design.md`](../specs/2026-11-23-cli-pencarian-design.md)

## Batasan Global

- Tanpa komentar di kode. Teks baru bahasa Indonesia tanpa em dash (teks lama yang sudah memuat `—` tidak diubah).
- Pencocokan: tanpa membedakan huruf besar/kecil, substring.
- Batas hasil tampil: `maxResults = 30`, untuk website dan akun.
- Batas daftar akun tampil penuh: `fullListMax = 20`.
- Pesan warna mengikuti D-052: hasil kosong dan nomor tidak valid kuning (`toneWarn`), `Terpilih (...)` hijau (`toneOK`).
- Perintah langsung (`--web`, `--akun-sumber`, `inventory --akun`) tidak berubah.
- Commit bahasa Indonesia, identitas `suwahadi`, tanpa anotasi AI. **Push hanya setelah operator menguji tampilan dan review akhir selesai.**

## Fokus Review

1. Kata kunci terlalu umum (mis. `.com`) cocok dengan ratusan website: hanya 30 tampil, `A` hanya memilih 30 itu. Dipatok `TestSelectWebsitesCapsResults` (Tugas 1).
2. Domain lengkap dengan huruf besar (`Toko.ID`) tetap terpilih. Dipatok `TestExactWebsites` (Tugas 1).
3. Akun tujuan dengan lebih dari 20 akun: kuota (panggilan API) hanya dihitung untuk baris yang tampil. Dipatok `TestChooseAccountDescribesOnlyShown` (Tugas 2).
4. Menu 3 dengan lebih dari 20 akun: ENTER tetap berarti semua akun. Dipatok `TestChooseAccountEmptyMeansAll` (Tugas 2).
5. Mengetik angka sebelum daftar apa pun tampil (lebih dari 20 akun): angka diperlakukan sebagai kata kunci, bukan nomor baris yang tidak terlihat. Dipatok `TestChooseAccountNumberWithoutListIsKeyword` (Tugas 2).

---

### Tugas 1: Pencarian dan pemilihan website sumber

**File:**
- Buat: `internal/cli/search.go`, `internal/cli/search_test.go`
- Ubah: `internal/cli/plan.go` (`runPlan` baris 127-131, `selectWebsites` baris 258-342)
- Ubah: `tests/integration/plan_test.go` (`TestPlanInteractiveWizard`)

**Antarmuka yang dihasilkan:**
- `const maxResults = 30`
- `type websiteHit struct { Account string; Site store.Website }`
- `func matchWebsites(listed []listedAccount, query string) []websiteHit`: urutan mengikuti `listed`, cocok bila domain atau id akun memuat `query`.
- `func exactWebsites(listed []listedAccount, input string) (hits []websiteHit, missing []string, exact bool)`: `exact` true bila `input` memuat koma atau satu-satunya nama sama persis dengan sebuah domain. Nama dipisah koma, dipangkas, dibandingkan tanpa membedakan huruf besar/kecil.
- `selectWebsites(listed []listedAccount, presetAccount string) (string, []string, error)`: tanda tangan tetap.

Alur `selectWebsites` (teks persis):
1. `Ketik sebagian domain atau id akun. ENTER = selesai.` lalu loop isian `Cari: `.
2. Kosong: kembalikan pilihan; bila belum ada, `Belum ada website dipilih.` dan ulang.
3. `exactWebsites` exact: tambah `hits`; untuk tiap `missing` cetak kuning `Domain %q tidak ditemukan.`
4. Selain itu `matchWebsites`. Nol hasil: kuning `Tidak ada website yang cocok dengan %q.` Ada hasil: `Hasil %q (%d):`, tampil maksimal 30 baris `%2d. %s  %-*s  %s · %s%s` (id akun, domain dirata, `typeLabel`, `sizeLabel`, `flags`), bila lebih dari 30 kuning `Menampilkan 30 dari %d. Persempit kata kunci.` Lalu isian `Pilih nomor (pisah koma), A = semua hasil, ENTER = cari lagi: `: `A` = semua baris tampil, nomor di luar baris tampil = kuning `Pilihan %q tidak valid.` dan tanya ulang, kosong = kembali ke `Cari:`.
5. Penambahan memakai aturan satu akun yang sudah ada (akun pertama mengikat, termasuk `presetAccount`; akun lain diabaikan dengan pesan lama `Satu batch hanya untuk satu akun sumber (%s). Pilihan dari akun lain diabaikan.`), lalu hijau `Terpilih (%s): %s`.

`runPlan`: ganti cetak semua akun (`Ditemukan %d akun ...` + loop `printAccountInventory`) menjadi `\nToken %s: %d akun, %d website.\n` (jumlah `Units` semua akun).

- [ ] **Langkah 1: Tulis test yang gagal** di `internal/cli/search_test.go` dengan data tiruan `listed` (u111: `toko.id`, `domainanda.com`; u222: `tokoku.net`, `contoh.com`)

```go
func TestMatchWebsites(t *testing.T)
```
`matchWebsites(listed, "TOKO")` = `toko.id`, `tokoku.net` (urut); `"u222"` = `tokoku.net`, `contoh.com`; `"zzz"` = kosong.

```go
func TestExactWebsites(t *testing.T)
```
`"Toko.ID"` → exact, hit `toko.id`; `"toko.id, kedai.id"` → exact, hit `toko.id`, missing `["kedai.id"]`; `"toko"` → tidak exact.

```go
func TestSelectWebsitesSearchAndPick(t *testing.T)
```
Input `zzz`, `toko`, `9`, `1`, `tokoku.net`, `u111`, `A`, ``(kosong). Hasil: akun `u111`, domain `[toko.id domainanda.com]`. Output memuat `Tidak ada website yang cocok dengan "zzz".`, `Hasil "toko" (2):`, `Pilihan "9" tidak valid.`, `Satu batch hanya untuk satu akun sumber (u111)`, `Terpilih (u111): toko.id, domainanda.com`.

```go
func TestSelectWebsitesCapsResults(t *testing.T)
```
Satu akun dengan 45 website `web01.com`..`web45.com`; input `web`, `A`, ``. Output memuat `Menampilkan 30 dari 45.`; hasil 30 domain.

- [ ] **Langkah 2: Jalankan, pastikan gagal**

Jalankan: `go test ./internal/cli -run 'TestMatchWebsites|TestExactWebsites|TestSelectWebsites' -v`
Harapan: gagal kompilasi (`matchWebsites` belum ada).

- [ ] **Langkah 3: Implementasi `search.go` dan tulis ulang `selectWebsites` serta header `runPlan`**

- [ ] **Langkah 4: Sesuaikan `TestPlanInteractiveWizard`**

Input baru: `4`, `1`, `zzz`, `toko`, `9`, `1`, `contoh.com`, `u494983133`, `A`, ``, `2`, `1`, `LANJUT`, `YAKIN`, `0`. Harapan teks: ganti `Ditemukan 2 akun di token xyz:`, `[1] u494983133  (Premium)`, `1. /home/u494983133/domains/domainanda.com`, prompt lama, `Pilihan "9.9" tidak valid`, `ketik A<nomor akun>` dengan `Token xyz: 2 akun, 3 website.`, `Tidak ada website yang cocok dengan "zzz".`, `Hasil "toko" (1):`, `Pilihan "9" tidak valid.`; `Terpilih (u494983133): toko.id, domainanda.com`; sisanya tetap. Bila test memeriksa domain batch, sesuaikan dengan `toko.id`, `domainanda.com`.

- [ ] **Langkah 5: Jalankan test, pastikan lulus**

Jalankan: `go test ./internal/cli && go test ./tests/integration -run 'TestPlan' -v`
Harapan: semua lulus.

- [ ] **Langkah 6: Commit**

```bash
git add internal/cli/search.go internal/cli/search_test.go internal/cli/plan.go tests/integration/plan_test.go docs/superpowers
git commit -m "Wizard: cari website sumber berdasarkan domain atau akun"
```

---

### Tugas 2: Pemilih akun dengan pencarian

**File:**
- Ubah: `internal/cli/search.go`, `internal/cli/search_test.go`
- Ubah: `internal/cli/plan.go` (`chooseDestAccount`), `internal/cli/wizard.go` (`pickAccount`, `askInventory`)

**Antarmuka:**
- Dipakai: `maxResults`, pola teks Tugas 1.
- Dihasilkan:
  - `const fullListMax = 20`
  - `type accountChoice struct { ID string; Domains []string }`
  - `func matchAccounts(items []accountChoice, query string) []accountChoice`: cocok bila id atau salah satu domain memuat `query`.
  - `func (a *App) chooseAccountFrom(items []accountChoice, prompt string, allowEmpty bool, describe func(id string) string) (string, error)`

Perilaku `chooseAccountFrom`:
- Daftar tampil saat ini: semua item bila `len(items) <= 20`, selain itu kosong dan cetak `Ketik sebagian id akun atau domain untuk mencari.`
- Baris: `%2d. %s  %s` (id, `describe(id)`), ditambah `  ` + sampai 3 domain yang cocok bila baris berasal dari pencarian domain.
- Loop isian `prompt`:
  - kosong: `allowEmpty` → `("", nil)`; selain itu kuning `Belum ada akun dipilih.`
  - nomor dalam daftar tampil → id itu
  - sama persis dengan id → id itu
  - selain itu `matchAccounts`: nol hasil → kuning `Tidak ada akun yang cocok dengan %q.`; ada hasil → `Hasil %q (%d):`, maksimal 30 baris (lebih → kuning `Menampilkan 30 dari %d. Persempit kata kunci.`), daftar tampil diganti hasil ini.
- `describe` hanya dipanggil untuk baris yang dicetak.

Pemakaian:

| Layar | Header yang sudah ada | `prompt` | `allowEmpty` | `describe` |
|---|---|---|---|---|
| `chooseDestAccount` | `Ditemukan %d akun di token %s:` | `Pilih salah satu (nomor, id, atau kata kunci): ` | false | `(%s) — %s` dengan `PlanLabel` dan label kuota (`DestQuota`) seperti sekarang |
| `askInventory` | (tanpa header) | `Akun yang dipindai ulang (nomor, id, atau kata kunci; ENTER = semua): ` | true | `(%s, token %s)` |
| `pickAccount` | (tanpa header) | `Pilih akun (nomor, id, atau kata kunci; ENTER = batal): ` | true | `(%s, SSH %s)` |

Domain untuk tiap akun dari `a.store.ListWebsites(ctx, id)`. `askInventory` tetap hanya bertanya bila akun lebih dari 1; pesan `Akun tidak ditemukan. Pemindaian dibatalkan.` tidak dipakai lagi karena isian tidak cocok kini memicu pencarian ulang.

- [ ] **Langkah 1: Tulis test yang gagal**

```go
func TestMatchAccounts(t *testing.T)
```
Item u111 (`toko.id`), u222 (`kedai.id`): `"TOKO"` → u111; `"u22"` → u222.

```go
func TestChooseAccountSmallListByNumberAndId(t *testing.T)
```
3 item, input `2` → id kedua; input `u333` → u333; output memuat ketiga baris.

```go
func TestChooseAccountDescribesOnlyShown(t *testing.T)
```
25 item `u100`..`u124`, `domains` `site%d.com`; input `site107`, `1`. Hasil `u107`; `describe` dipanggil tepat 1 kali; output tidak memuat `u100`.

```go
func TestChooseAccountEmptyMeansAll(t *testing.T)
```
25 item, `allowEmpty` true, input `` → `("", nil)`.

```go
func TestChooseAccountNumberWithoutListIsKeyword(t *testing.T)
```
25 item, input `5`, lalu `` (allowEmpty true). Output memuat `Hasil "5"` (id yang memuat 5) dan tidak mengembalikan item ke-5 tanpa terlihat; hasil `""`.

- [ ] **Langkah 2: Jalankan, pastikan gagal**

Jalankan: `go test ./internal/cli -run 'TestMatchAccounts|TestChooseAccount' -v`
Harapan: gagal kompilasi.

- [ ] **Langkah 3: Implementasi `chooseAccountFrom` dan sambungkan ke tiga layar**

- [ ] **Langkah 4: Jalankan pemeriksaan**

Jalankan: `test -z "$(gofmt -l .)" && go vet ./... && GOOS=linux go vet ./... && go test ./internal/cli && go test ./tests/integration -run '^(TestTokenNeverStoredInDBOrLogs|TestRemoveRequiresConfirmation|TestWizardFlow|TestWizardStopsOnEOF|TestHelpAndErrorsInIndonesian|TestVersionNeedsNoConfig|TestDefaultConfigCreatedOnFirstRun|TestSSHSetupTestAndAudit|TestSSHPasswordFallback|TestSSHSetupUnknownAccountAndNoAccounts|TestAuditRevokedTokenExit2|TestInventoryCommand|TestPlanDryRunRejectedWhenQuotaInsufficient|TestPlanDryRunThenSave|TestPlanInteractiveWizard|TestPlanDomainConflictBlocked)$'`
Harapan: semua lulus. Test integrasi lain yang gagal di Windows hanya karena D-050.

- [ ] **Langkah 5: Commit**

```bash
git add internal/cli
git commit -m "Wizard: cari akun tujuan, akun pindai ulang, dan akun SSH"
```

---

### Tugas 3: Dokumen dan rilis rc7

**File:** `docs/decisions.md` (D-055), `README.md` (versi 3 tempat; alur menu 4 di tabel langkah dan contoh), `docs/uji-lapangan.md` (baris uji pencarian), `CLAUDE.md` (status Fase 8), `dist/migrator.exe`, `dist/SHA256SUMS`

- [ ] **Langkah 1: D-055** berisi: cari-lalu-pilih, substring tanpa huruf besar/kecil, batas 30 hasil, `A` hanya hasil tampil, domain lengkap berkoma langsung terpilih, daftar akun penuh bila ≤ 20, kuota akun tujuan hanya untuk baris tampil.
- [ ] **Langkah 2: Cek tanpa komentar dan em dash di teks baru**

Jalankan: `grep -rnE '^\s*(//|/\*)' --include='*.go' . | grep -vE '//go:(build|embed|generate)'` dan `grep -n '—' internal/cli/search.go internal/cli/search_test.go`
Harapan: kosong.

- [ ] **Langkah 3: Build rc7**

Jalankan: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X hostinger-migrator/internal/cli.Version=1.0.0-rc7" -o dist/migrator.exe ./cmd/migrator && (cd dist && sha256sum -t migrator.exe > SHA256SUMS && sha256sum -c SHA256SUMS)`
Harapan: `migrator.exe: OK`.

- [ ] **Langkah 4: Commit lokal** (push menunggu uji tampilan operator dan review akhir)

```bash
git add docs README.md CLAUDE.md dist/migrator.exe dist/SHA256SUMS
git commit -m "Rilis 1.0.0-rc7: pencarian domain dan akun di layar pilihan wizard"
```
