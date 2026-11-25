# Rencana Implementasi: Menu Cari Website

> **Untuk agent pelaksana:** wajib memakai superpowers:subagent-driven-development atau superpowers:executing-plans, tugas per tugas. Langkah memakai kotak centang (`- [ ]`).

**Tujuan:** menu utama 12 "Cari website" yang menemukan domain di akun dan token mana, dari inventaris lokal (akun terpindai) dan API read-only (akun belum terpindai).

**Arsitektur:** file baru `internal/cli/find.go`: pengumpulan data sekali per buka menu (`collectSites`), pencocokan murni (`matchFound`), dan loop menu (`findWebsiteMenu`). Pengambilan API disuntikkan sebagai fungsi agar bisa diuji tanpa jaringan.

**Teknologi:** Go 1.26+, pustaka standar. Tanpa dependency baru.

**Spesifikasi:** [`docs/superpowers/specs/2026-11-25-cari-website-design.md`](../specs/2026-11-25-cari-website-design.md)

## Batasan Global

- Tanpa komentar di kode. Teks bahasa Indonesia tanpa em dash, persis seperti spec bagian 2.
- API hanya read-only (`ReadOnlyCopy`), hanya untuk token yang punya akun tanpa inventaris.
- Warna dan layar bersih mengikuti D-052. Batas hasil memakai `maxResults` (30) yang sudah ada.
- Commit bahasa Indonesia, identitas `suwahadi`, tanpa anotasi AI. Push setelah operator setuju.

## Fokus Review

1. Akun yang sama di dua token: satu baris hasil dengan kedua alias, bukan dua baris. Dipatok `TestCollectSitesMergesAliases` (Tugas 1).
2. Akun berinventaris tidak dipanggil ke API. Dipatok `TestCollectSitesUsesAPIOnlyForUnscanned` (Tugas 1).
3. Token yang API-nya gagal: peringatan, pencarian tetap jalan. Dipatok `TestCollectSitesAPIFailureWarns` (Tugas 1).
4. ENTER kosong tidak keluar dari menu; `0` keluar. Dipatok `TestFindWebsiteMenu` (Tugas 1).
5. Input dari pipe (bukan terminal) tidak menampilkan kode ANSI. Sudah dijaga D-052 (`colorOut` dari `isTerminal`); tanpa test baru.

---

### Tugas 1: Menu Cari Website

**File:** `internal/cli/find.go` (baru), `internal/cli/find_test.go` (baru), `internal/cli/wizard.go` (`mainMenu` dan `switch` di `runWizard`)

**Antarmuka:**
- `type foundSite struct { Domain, Account, Vhost string; Aliases []string }`
- `func (a *App) collectSites(ctx context.Context, remote func(ctx context.Context, alias string) ([]hostinger.Website, error)) (sites []foundSite, localAccounts, apiAccounts int, warnings []string, err error)`:
  - semua akun dari `a.store.ListHostingAccounts(ctx, "")`; akun dianggap terpindai bila `GetInventoryRun(ctx, acc.ID, acc.Alias)` sukses untuk salah satu aliasnya;
  - akun terpindai: website dari `a.store.ListWebsites(ctx, acc.ID)`;
  - akun belum terpindai: per alias yang memilikinya, `remote(ctx, alias)` dipanggil **sekali per alias**, hasil disaring `Username == acc.ID`;
  - kegagalan `remote` untuk satu alias menjadi peringatan teks spec 2.6, bukan error;
  - pasangan (domain, akun) unik; alias digabung dan diurutkan.
- `func matchFound(sites []foundSite, query string) []foundSite`: substring domain tanpa membedakan huruf besar/kecil, urutan domain alfabetis.
- `func (a *App) findWebsiteMenu(ctx context.Context) error`: judul `CARI WEBSITE`, ringkasan sumber, peringatan, lalu loop sesuai spec 2.2, 2.4, 2.5, 2.7. Remote default: `tokenClient(alias)` → `ReadOnlyCopy().ListWebsites(ctx, hostinger.WebsiteFilter{})`.
- `mainMenu` ditambah `{"12", "Cari website"}`; `runWizard` case `"12"` → `findWebsiteMenu` dengan `pause = false` (menu punya loop sendiri).

- [ ] **Langkah 1: Tulis test yang gagal** di `internal/cli/find_test.go` (memakai `openTestStore`/`addTestAccount` yang sudah ada):
  - `TestCollectSitesMergesAliases`: akun `u1` terdaftar di alias `a` dan `b`, berinventaris dengan `x.com`; hasil satu `foundSite{Domain: "x.com", Account: "u1", Aliases: ["a", "b"]}`.
  - `TestCollectSitesUsesAPIOnlyForUnscanned`: `u1` (alias `a`) berinventaris, `u2` (alias `a`) belum; remote palsu mencatat panggilan dan mengembalikan website `u1` dan `u2`; hasil memuat website `u2` dari remote dan website `u1` dari lokal saja; `localAccounts == 1`, `apiAccounts == 1`, remote dipanggil tepat sekali untuk `a`.
  - `TestCollectSitesAPIFailureWarns`: remote mengembalikan error untuk alias `b`; peringatan memuat `Token b: daftar website tidak bisa diambil`; website lokal tetap ada.
  - `TestFindWebsiteMenu`: input `\nzzz\ngoldfish\n0\n`; output memuat `Tidak ada website yang cocok dengan "zzz".`, `(1). serene-papayawhip-goldfish-35.hostingersite.com ada di akun: u920879458 - hostinger-ilhamiahnure`, `[0] Kembali ke menu utama`; fungsi kembali tanpa error.
  - `TestMatchFoundLabels`: hasil `(subdomain)` untuk vhost `subdomain`, `(parked)` untuk `parked`, tanpa label untuk `main`/`addon` (lewat fungsi format baris yang dipakai menu).
- [ ] **Langkah 2:** `go test ./internal/cli -run 'TestCollectSites|TestFindWebsiteMenu|TestMatchFound' -v` → gagal kompilasi.
- [ ] **Langkah 3:** implementasi; README tabel menu dan contoh tampilan menu utama ditambah baris 12.
- [ ] **Langkah 4:** `test -z "$(gofmt -l .)" && go vet ./... && GOOS=linux go vet ./... && go test ./internal/cli` dan 16 test integrasi Windows → lulus (test integrasi yang memeriksa daftar menu utama disesuaikan bila memuat seluruh daftar).
- [ ] **Langkah 5: Commit** `Wizard: menu 12 Cari website (inventaris lokal + API untuk akun belum dipindai)` (sertakan spec dan rencana ini).

---

### Tugas 2: Dokumen dan rilis rc11

**File:** `docs/decisions.md` (D-059), `README.md` (versi 3 tempat), `CLAUDE.md` (status Fase 8), `dist/migrator.exe`, `dist/SHA256SUMS`

- [ ] **Langkah 1:** D-059 merangkum menu 12, sumber data gabungan, read-only, sekali per buka menu.
- [ ] **Langkah 2:** cek komentar kosong, build `1.0.0-rc11`, `sha256sum -c` OK.
- [ ] **Langkah 3: Commit lokal** `Rilis 1.0.0-rc11: menu Cari website`.
