# PRD — Hostinger Migration Manager (`migrator`)

| Info | Nilai |
|---|---|
| Versi dokumen | 1.0 |
| Tanggal | 22 November 2026 |
| Bahasa | Indonesia (dokumen, CLI, dan laporan) |
| Pelaksana coding | Claude Code |
| Status | Disetujui operator — siap implementasi bertahap |

> **Untuk Claude Code:** baca bagian [19. Aturan Kerja Agent](#19-aturan-kerja-agent-untuk-claude-code) sebelum menulis kode apa pun. Kerjakan per fase (bagian 15). Jangan lompat fase sebelum Definition of Done fase sebelumnya lulus.

---

## 1. Latar Belakang & Masalah

Website klien tersebar di banyak akun hPanel Hostinger yang terpisah (masing-masing punya login dan API token sendiri). Pengelolaan jadi berantakan. Dibutuhkan alat CLI yang memindahkan website beserta layanan terkait (file, database, DNS, SSL, email) dari satu akun Hostinger ke akun Hostinger lain secara otomatis, aman, bisa dilanjutkan setelah gangguan, dan menghasilkan laporan lengkap.

## 2. Tujuan & Non-Tujuan

### 2.1 Tujuan
1. Mendaftarkan banyak API token (masing-masing dengan alias) dan menemukan akun hosting (`uXXXXXXXX`) di balik setiap token.
2. Menginventarisasi website, root path, database, domain, DNS, dan email.
3. Memindahkan website ke akun tujuan lewat domain dummy (`*.hostingersite.com` atau free subdomain sejenis), menguji, lalu melakukan cutover domain.
4. Memindahkan email (backup → hapus di lama → buat di baru → restore).
5. Berjalan di background, auto-resume setelah internet putus, aplikasi tertutup, atau Windows restart.
6. Tidak melewatkan website dengan domain expired/disabled: tetap diekspor dan direstore ke domain dummy.
7. Laporan akhir lengkap (terminal, HTML, JSON, CSV) dalam bahasa Indonesia.

### 2.2 Non-Tujuan (v1)
- Cloud Hosting, Agency Hosting, VPS.
- Memindahkan kepemilikan/registrar domain antar akun.
- Dashboard web, multi-user.
- Provider selain Hostinger.
- Memperbaiki website sumber yang sudah rusak.
- Enkripsi backup lokal.

## 3. Keputusan Terkunci

| # | Area | Keputusan |
|---|---|---|
| 1 | Cakupan | Shared Hosting saja (Single, Premium, Business). 20–100 website. Multi-akun, token terpisah. Windows 10/11 via Git Bash. |
| 2 | Jalur akses | Hybrid: API resmi untuk inventaris, provisioning, DNS, SSL, email. SSH/SFTP untuk file & database. Paket tanpa SSH → checkpoint manual (operator unduh backup dari hPanel). |
| 3 | Uji sebelum cutover | Backup lokal → restore ke domain dummy → uji. Metode uji dipilih otomatis per tipe web (lihat 8.10). |
| 4 | Cutover domain | Domain di sumber diganti ke subdomain sementara (situs lama tetap hidup untuk rollback) → domain dipasang di tujuan. DNS diubah memakai token sumber karena registrar tetap di akun lama. |
| 5 | Email | Backup via API → hapus layanan email lama → buat mailbox di tujuan (checkpoint manual bila perlu) → restore via API, password lama tetap berlaku. IMAP hanya cadangan. |
| 6 | Downtime | Bebas. Cukup satu backup final sebelum cutover. |
| 7 | Domain expired/disabled | Tetap diekspor dan direstore ke domain dummy. Status akhir `DUMMY_ONLY`. |
| 8 | Penghapusan sumber | Satu konfirmasi per batch, hanya untuk website yang lolos semua syarat. |
| 9 | Backup lokal | Tanpa enkripsi. Folder backup disarankan di luar OneDrive/Google Drive. |
| 10 | Laporan | Terminal, HTML, JSON, CSV — bahasa Indonesia. |
| 11 | Output | PRD Markdown ini, untuk Claude Code. |
| 12 | SSH key | Satu pasangan key ed25519 dibuat tool, public key yang sama ditempel di hPanel semua akun. Pengingat penghapusan key di akhir. Cadangan: password SSH. |

## 4. Fakta & Batasan Hostinger API

Hal berikut sudah diketahui dari dokumentasi resmi dan **wajib diverifikasi ulang dari `openapi.json` terbaru** saat implementasi:

1. Base URL `https://developers.hostinger.com`, path berbentuk `/api/<produk>/v<versi>/...` (contoh `/api/hosting/v1/websites`). Autentikasi `Authorization: Bearer <token>`.
2. Respons `202 Accepted` berarti proses asinkron — lakukan polling, jangan kirim ulang.
3. Melebihi batas rate menghasilkan `429 Too Many Requests`. Beberapa akun memiliki batas khusus.
4. Pagination memakai `page`, `per_page`, dan objek `meta`.
5. **Peringatan penting:** banyak operasi lengkap (upload file, import arsip, database, SSL, PHP, change domain) terdapat di namespace `/api/agency-hosting/...`. Namespace itu **tidak boleh dianggap berlaku untuk Shared Hosting**. Untuk Shared Hosting, setiap operasi harus dibuktikan oleh Capability Audit (8.3).
6. Belum ditemukan endpoint download/export file website. Karena itu ekspor file dan database dilakukan via SSH.
7. Ada endpoint snapshot DNS (`/api/dns/v1/snapshots/{domain}`) yang dipakai untuk rollback DNS.
8. Mail API (`api.mail.hostinger.com`) terpisah dari Hostinger API dan bisa memakai token berbeda.

Sumber rujukan: `https://developers.hostinger.com/openapi/openapi.json`, `https://docs.hostinger.com/llms.txt`, repo `github.com/hostinger/api`, `github.com/hostinger/api-cli`, `github.com/hostinger/api-mcp-server`.

## 5. Arsitektur & Tech Stack

### 5.1 Prinsip stack: simpel, ringan, fungsional
Hasil akhir satu file `migrator.exe`. Tidak butuh Python, Node.js, Docker, WSL, OpenSSH, atau MySQL client di Windows. Perintah `tar`, `mysqldump`, `mysql`, dan `wp` dijalankan **di server** lewat SSH.

| Kebutuhan | Pilihan |
|---|---|
| Bahasa | Go (versi stabil terbaru) |
| CLI | `spf13/cobra` + prompt sederhana (`bufio`) |
| State | SQLite via `modernc.org/sqlite` (pure Go, tanpa CGO), mode WAL |
| HTTP | stdlib `net/http` |
| SSH/SFTP | `golang.org/x/crypto/ssh` + `github.com/pkg/sftp` |
| Penyimpanan token | Windows Credential Manager via `github.com/zalando/go-keyring` |
| Log | stdlib `log/slog` (JSON ke file, teks ke terminal) |
| Laporan | stdlib `html/template`, `encoding/json`, `encoding/csv` |
| Checksum | stdlib `crypto/sha256` |
| Config | file YAML kecil (`gopkg.in/yaml.v3`) |

Dilarang menambah dependency lain tanpa alasan tertulis di `docs/decisions.md`.

### 5.2 Gambaran komponen

```
Git Bash  →  migrator.exe (CLI + wizard)
                 │
                 ├── worker (proses background terpisah)
                 │     ├── hostinger/  (API client + rate limiter per token)
                 │     ├── sshx/       (SSH exec + SFTP)
                 │     ├── pipeline/   (state machine per website)
                 │     └── email/      (adapter API email + IMAP cadangan)
                 │
                 └── SQLite (state, checkpoint, audit) + folder backup lokal
```

### 5.3 Struktur proyek

```
hostinger-migrator/
├── cmd/migrator/main.go
├── internal/
│   ├── cli/            # perintah cobra & wizard
│   ├── config/
│   ├── store/          # SQLite, migrasi skema, repository
│   ├── secrets/        # go-keyring + SSH key
│   ├── hostinger/      # client, ratelimit, contracts/
│   ├── sshx/
│   ├── capability/
│   ├── inventory/
│   ├── detector/
│   ├── planner/
│   ├── backup/
│   ├── restore/
│   ├── verify/
│   ├── cutover/        # domain, DNS, SSL
│   ├── email/
│   ├── cleanup/
│   ├── pipeline/       # state machine & runner
│   ├── worker/         # proses background, lock, autostart
│   └── report/
├── internal/hostinger/contracts/*.yaml   # endpoint terverifikasi
├── migrations/sqlite/*.sql
├── configs/default.yaml
├── docs/decisions.md
├── tests/{fixtures,integration}
├── CLAUDE.md
└── go.mod
```

Folder data runtime (default, bisa diubah):

```
<folder-kerja>/
├── migrator.exe
├── data/
│   ├── migrator.db
│   ├── ssh/id_ed25519        # private key, hanya user Windows saat ini
│   ├── backups/<batch>/<domain>/
│   ├── logs/
│   └── reports/<batch>/
└── config.yaml
```

---

## 6. Alur Pengguna (Wizard Utama)

Operator cukup menjalankan `./migrator.exe`. Wizard mengikuti skenario asli operator:

```
HOSTINGER MIGRATION MANAGER
1. Kelola API token
2. Siapkan SSH key
3. Scan akun (inventaris)
4. Buat rencana & mulai migrasi
5. Status migrasi
6. Tindakan manual tertunda
7. Laporan
0. Keluar
Pilih:
```

Alur migrasi (menu 4):

```
Masukkan API token sumber: ********
Nama token ini: xyz
Memvalidasi token... OK

Ditemukan 2 akun di token xyz:

[1] u494983133  (Premium)
    1. /home/u494983133/domains/domainanda.com        WordPress · 1,2 GB
    2. /home/u494983133/domains/toko.id               Laravel   · 850 MB
[2] u597522494  (Single)
    1. /home/u597522494/domains/contoh.com            Statis    · 40 MB  [EXPIRED]

Pilih: [nomor akun.nomor web, pisah koma] / [A] semua / [ENTER] lanjut

Masukkan API token tujuan: ********
Nama token ini: zyx
Ditemukan 2 akun di token zyx:
1. u73839202  (Business) — sisa kuota website: 47
2. u72999109  (Premium)  — sisa kuota website: 3
Pilih salah satu: 1

RINGKASAN RENCANA
Sumber : xyz / u494983133 (2 website)
Tujuan : zyx / u73839202
Perkiraan backup: 2,1 GB · Database: 2 · Mailbox: 6
Pemeriksaan: kuota OK · SSH sumber OK · SSH tujuan OK

Konfirmasi: Anda yakin memindahkan domain dan layanan terkait
di akun xyz u494983133 ke tujuan zyx u73839202? Ketik YAKIN:
```

Setelah `YAKIN`, worker background dimulai dan terminal boleh ditutup. Token yang sudah pernah disimpan tidak diminta ulang; wizard menawarkan memilih alias yang ada.

---

## 7. Konsep Identitas

- **Alias token** (`xyz`): satu API token. Disimpan di Windows Credential Manager dengan service `hostinger-migrator`, key = alias.
- **Akun hosting** (`u494983133`): ditemukan dari token. Satu token bisa memiliki banyak akun hosting.
- **Website**: satu domain/website di dalam akun hosting.
- **Batch**: satu sesi migrasi (satu sumber → satu tujuan, banyak website). ID: `MIG-YYYYMMDD-NNN`.

---

## 8. Spesifikasi Modul

### 8.1 Account Manager
- `account add`: input token (tidak di-echo), alias unik, validasi dengan endpoint read-only, discovery akun hosting, simpan.
- `account list | test <alias> | remove <alias>`: `remove` hanya menghapus credential lokal, tidak menyentuh Hostinger.
- Token tidak pernah ditulis ke SQLite, log, laporan, atau config.
- Deteksi token dicabut/kedaluwarsa (401/403) → item terkait menjadi `BLOCKED` dengan pesan jelas, bukan `FAILED`.

### 8.2 Setup SSH Key
1. Saat pertama kali, tool membuat pasangan key ed25519 di `data/ssh/` (pure Go). Izin file private key dibatasi ke user Windows saat ini (ACL). Private key tidak pernah ditampilkan.
2. Untuk setiap akun hosting (sumber dan tujuan), tool menampilkan:
   ```
   AKTIFKAN SSH — akun u494983133
   1. hPanel → Advanced → SSH Access
   2. Pastikan status SSH: Enabled
   3. Klik "Add SSH Key", tempel baris berikut:

   ssh-ed25519 AAAAC3Nz...xyz hostinger-migrator

   Setelah selesai, ketik: SELESAI
   ```
3. Host/IP, port, dan username diambil dari API bila tersedia. Jika tidak, operator diminta menyalinnya dari halaman SSH Access. **Port tidak boleh di-hardcode ke 22.**
4. Tes koneksi (`ssh exec: echo ok; whoami; pwd`). Hasil: `SSH_READY`, atau pesan penyebab (SSH belum aktif, key salah tempel, paket tidak mendukung SSH, host salah).
5. Cadangan: password SSH (diminta saat itu, disimpan di Credential Manager, bisa dihapus setelah selesai).
6. Bila Capability Audit menemukan endpoint resmi untuk menambah SSH key, langkah 2 dilakukan otomatis.
7. Simpan `known_hosts` per akun. Jika fingerprint berubah, hentikan dan minta konfirmasi.

### 8.3 Capability Audit
Dijalankan per akun hosting sebelum rencana dibuat. Setiap operasi diuji dengan cara **read-only atau tidak merusak**, hasilnya disimpan di tabel `capabilities`:

| Kode | Operasi | Cara uji |
|---|---|---|
| `API_LIST_WEBSITES` | List website | Panggil endpoint list |
| `API_CREATE_WEBSITE` | Buat website | Cek keberadaan endpoint di contract registry untuk Shared; uji nyata hanya di Fase 4 pada akun uji |
| `API_FREE_SUBDOMAIN` | Generate subdomain gratis | Sama seperti di atas |
| `API_CHANGE_DOMAIN` | Ganti domain website | Contract registry |
| `API_DB_CREATE` | Buat database | Contract registry |
| `API_DNS_READ/WRITE` | DNS zone | Baca zone; tulis hanya saat cutover |
| `API_DNS_SNAPSHOT` | Snapshot DNS | Baca daftar snapshot |
| `API_SSL` | Pasang SSL | Contract registry |
| `API_EMAIL_*` | Backup/hapus/buat/restore email | Contract registry + uji pada mailbox uji |
| `SSH` | SSH + SFTP | Tes koneksi |
| `SSH_TOOLS` | `tar`, `gzip`, `mysqldump`, `mysql`, `wp`, `sha256sum` di server | `command -v` |
| `QUOTA` | Sisa kuota website, disk, inode, database | API metrics bila ada, `df`/`du` via SSH |

Status: `SUPPORTED`, `MANUAL` (ada fallback manual), `UNSUPPORTED`, `UNKNOWN`. **`UNKNOWN` tidak boleh dianggap `SUPPORTED`.**

### 8.4 Inventory
Per akun hosting, kumpulkan:
- Website dari API (id, domain, tipe, status).
- Root path via SSH: unit backup adalah **seluruh folder** `/home/<u>/domains/<domain>/` (bukan hanya `public_html`), karena Laravel dan aplikasi lain sering menaruh kode di luar `public_html`.
- Subdomain dan addon/parked domain (folder `public_html/<sub>` atau entri terpisah).
- Ukuran folder (`du -sb`) dan jumlah file.
- Database yang dipakai (dari file konfigurasi aplikasi, lihat 8.5) dan ukurannya.
- DNS records (bila token memiliki zone tersebut).
- Mailbox per domain (via API email).
- Cron job via `crontab -l` (dicatat; dipindahkan manual atau via API bila tersedia).
- Status domain: aktif, expired, disabled, NS eksternal.

Output: SQLite + `data/inventory/<alias>-<akun>.json`. `inventory` **tidak pernah mengubah apa pun**.

### 8.5 Detector Tipe Website
Deteksi berbasis beberapa sinyal, hasil berupa tipe + tingkat keyakinan:

| Tipe | Sinyal | Sumber kredensial DB |
|---|---|---|
| WordPress | `wp-config.php` + `wp-content/` + `wp-includes/` | `DB_NAME/USER/PASSWORD/HOST` di `wp-config.php` |
| Laravel | `artisan` + `composer.json` berisi `laravel/framework` | `.env` (`DB_*`) |
| CodeIgniter | `system/` + `application/` atau `app/Config/` | `database.php` / `.env` |
| Joomla | `configuration.php` + `administrator/` | `configuration.php` |
| PHP native | file `.php`, tanpa framework dikenal | tidak otomatis → tanya operator bila ada DB |
| Statis | `index.html`, tanpa `.php` | tidak ada |
| Node.js | `package.json` + script `start` + konfigurasi Node di hPanel/API | `.env` |
| Lainnya | — | generik: backup/restore apa adanya |

`package.json` saja **tidak** berarti Node.js (bisa sekadar build frontend). Hasil tidak pernah mengubah struktur file.

### 8.6 Planner
- Pilih sumber (alias + akun + website) dan tujuan (alias + akun).
- Cek: sisa kuota website tujuan ≥ jumlah website dipilih; disk tujuan ≥ total ukuran × 1,3; kuota database; domain tidak bentrok di akun tujuan; SSH kedua sisi siap.
- Gagal cek → rencana ditolak dengan alasan jelas. Kuota `UNKNOWN` → peringatan + konfirmasi operator.
- Hasil: batch berstatus `PLANNED` + ringkasan seperti contoh di bagian 6.
- `--dry-run`: menjalankan semua cek dan menampilkan urutan langkah tanpa operasi tulis.

### 8.7 Backup Engine (via SSH, streaming)
Tanpa membuat file sementara di server (hemat kuota):

```
# file
ssh: tar -czf - -C /home/<u>/domains/<domain> .   →  stream ke  backups/.../files.tar.gz
# database (per DB)
ssh: mysqldump --single-transaction --quick --routines --triggers --events \
     --default-character-set=utf8mb4 -h <host> -u <user> -p<pass> <db> | gzip -c
     →  stream ke  backups/.../db-<nama>.sql.gz
```
- Password DB dikirim lewat `MYSQL_PWD` di environment sesi SSH, bukan argumen baris perintah, dan tidak dicatat di log.
- Setelah selesai: SHA-256 lokal, `gzip -t` lokal, hitung entri arsip; bandingkan jumlah file dengan inventaris (toleransi kecil untuk file cache).
- Simpan `manifest.json` per website: ukuran, jumlah file, checksum, tabel + jumlah baris per tabel (dari `information_schema`), waktu, versi migrator.
- Resume: jika stream putus, ulangi artefak tersebut dari awal (artefak dianggap atomik); artefak yang sudah tervalidasi tidak diulang.
- Paket tanpa SSH → checkpoint manual `MANUAL_BACKUP`: operator mengunduh backup file + DB dari hPanel ke folder yang ditunjukkan tool, lalu tool memvalidasi.

### 8.8 Provisioning Tujuan & Domain Dummy
1. Buat website baru di akun tujuan memakai subdomain gratis Hostinger (endpoint generate free subdomain bila `SUPPORTED`).
2. Bila API tidak mendukung pembuatan website di Shared Hosting → checkpoint manual `MANUAL_CREATE_SITE`: tool menampilkan langkah hPanel, operator memasukkan nama dummy yang dibuat, tool memverifikasi lewat API + SSH.
3. Operasi asinkron: polling sampai selesai, timeout dapat diatur.
4. **Idempotensi:** sebelum membuat, cek apakah website dummy untuk item ini sudah ada (dicatat di SQLite dan dicocokkan via API). Timeout tidak boleh langsung dibuat ulang.
5. Catat `dest_website_id`, `dummy_domain`, root path tujuan.

### 8.9 Restore
1. Upload `files.tar.gz` via SFTP ke `/home/<u>/.migrator/<item>/` (resume per artefak, cek checksum di server dengan `sha256sum`).
2. Ekstrak ke folder website dummy dengan struktur identik (termasuk file tersembunyi). Isi bawaan folder dummy (mis. `default.php`) dipindah ke `.migrator/` dulu, tidak langsung dihapus.
3. Buat database tujuan (API bila `SUPPORTED`, jika tidak checkpoint manual `MANUAL_CREATE_DB` dengan nama/user/password yang disarankan tool). Ingat prefix `u<akun>_` pada nama DB Hostinger.
4. Import: `gunzip -c dump.sql.gz | mysql ...` via SSH. Validasi jumlah tabel dan jumlah baris per tabel terhadap manifest.
5. Perbarui kredensial DB di file konfigurasi (`wp-config.php`, `.env`, dll.) — **edit nilai saja**, simpan salinan asli sebagai `<file>.migrator-orig`.
6. Samakan versi PHP dengan sumber (API bila didukung, jika tidak checkpoint manual).
7. Hapus file sementara di `.migrator/` setelah verifikasi lulus.

### 8.10 Verifikasi
Metode dipilih otomatis per tipe:

| Tipe | Metode utama | Metode cadangan |
|---|---|---|
| Statis, Node.js | GET langsung ke URL dummy | — |
| WordPress, Laravel, PHP, CMS lain | Request HTTP ke **IP server tujuan** dengan header `Host: <domain asli>` (tanpa mengubah DB) | WordPress: `wp search-replace` (aman untuk data serialized) ke domain dummy, dicatat agar dikembalikan saat cutover |

Pemeriksaan:
- Status HTTP 200 atau redirect yang wajar (bukan ke domain lain/halaman default Hostinger).
- Tidak ada tanda error: `Error establishing a database connection`, `Whoops`, `Fatal error`, `500`, `Index of /`.
- Judul halaman (`<title>`) sama dengan situs sumber (bila sumber masih bisa diakses).
- Minimal 3 asset (CSS/JS/gambar) dari halaman utama berstatus 200.
- Query sederhana ke DB tujuan berhasil.
- `error_log` baru di folder tujuan diperiksa.

Hasil: `PASS`, `WARN` (lulus dengan catatan), `FAIL`. Hanya `PASS`/`WARN` yang boleh lanjut cutover. Website `DUMMY_ONLY` (domain expired/disabled) cukup lulus pemeriksaan file + DB + respons dasar.

### 8.11 Cutover Domain, DNS, SSL
Hanya untuk website berstatus `VERIFIED` dan domain aktif. Urutan per website:

1. **Backup final** (keputusan 6): ulangi 8.7 + 8.9 untuk file dan DB, lalu verifikasi singkat.
2. **Snapshot DNS** zone domain (token sumber) → simpan JSON lokal + catat ID snapshot API.
3. **Lepas domain di sumber**: ganti domain website sumber ke subdomain sementara (API bila didukung, jika tidak checkpoint manual `MANUAL_SOURCE_RENAME`). Situs lama tetap hidup untuk rollback.
4. **Pasang domain di tujuan**: ganti domain website dummy ke domain asli (atau tambahkan), sesuai kemampuan API; jika tidak, checkpoint manual. **Wajib diverifikasi di Fase 4:** apakah folder website ikut berpindah saat domain diganti.
5. **Perbarui DNS** di zone domain (token sumber, karena registrar tetap di akun lama): A/AAAA ke IP tujuan; MX/SPF/DKIM baru menunggu tahap email. Record lain (CNAME pihak ketiga, TXT verifikasi, SRV, CAA) **dipertahankan**. Domain dengan NS eksternal → checkpoint manual berisi daftar record yang harus diubah.
6. Kembalikan URL bila tadi memakai `wp search-replace`.
7. **SSL**: pasang via API (bila didukung) setelah DNS mengarah; polling dengan batas waktu; jika belum aktif → status `SSL_PENDING` (tidak memblokir email, tetapi memblokir cleanup kecuali dikecualikan operator).
8. Verifikasi akhir via domain asli (HTTP & HTTPS, redirect HTTP→HTTPS).

Rollback (sebelum cleanup): kembalikan DNS dari snapshot, kembalikan domain ke website sumber.

### 8.12 Email
Dikerjakan per domain **setelah** website domain tersebut selesai cutover (atau langsung untuk domain tanpa website).

1. **Inventaris**: mailbox, alias, forwarder, catch-all, kuota, jumlah pesan per folder.
2. **Backup via API** (jalur yang sudah pernah dipakai operator; password ikut terbawa). Simpan ke `backups/<batch>/<domain>/email/<mailbox>/` beserta `manifest.json` (jumlah pesan per folder, ukuran).
3. **Validasi backup**: jumlah pesan di backup = jumlah di sumber (toleransi pesan baru yang masuk selama proses).
4. **Konfirmasi hapus email lama** — operasi destruktif sebelum tujuan siap, jadi butuh konfirmasi tersendiri (sekali per batch, menampilkan daftar domain + ringkasan backup).
5. Hapus layanan email domain di akun sumber.
6. **Buat di tujuan**: via API bila didukung; jika tidak checkpoint manual `MANUAL_EMAIL_SETUP` berisi daftar mailbox yang harus dibuat. Operator mengetik `migrator confirm EMAIL <domain>`; tool memverifikasi keberadaan mailbox via API.
7. **Restore via API**, lalu buat ulang alias/forwarder.
8. **DNS email** (token sumber): MX, SPF, DKIM, DMARC sesuai nilai dari layanan email tujuan; record lain dipertahankan.
9. **Verifikasi**: jumlah pesan per folder cocok dengan manifest; login/akses mailbox via API berhasil.
10. Cadangan IMAP (backup `IMAP FETCH` ke `.eml`, restore `IMAP APPEND`) hanya bila jalur API gagal — membutuhkan password mailbox dari operator.

> Endpoint persis untuk backup/restore email **wajib dikonfirmasi** dari dokumentasi resmi dan dari langkah yang pernah dipakai operator sebelum Fase 6 dimulai (lihat bagian 18).

### 8.13 Cleanup Sumber
Gerbang per website (semua wajib benar):
- Backup lokal file + DB valid dan dapat dibaca.
- Verifikasi tujuan `PASS`/`WARN`, cutover selesai, DNS mengarah ke tujuan.
- SSL aktif **atau** dikecualikan operator.
- Email domain `EMAIL_DONE` atau domain tanpa email.
- Tidak ada langkah yang masih berjalan.

Alur: `migrator cleanup` → tampilkan daftar website yang lolos dan yang tertahan (beserta alasan) → satu konfirmasi `HAPUS` untuk seluruh batch → hapus website dan database di akun sumber (API atau checkpoint manual). **Domain/registrar tidak pernah dihapus.** Website `DUMMY_ONLY` tidak dihapus dari sumber kecuali operator memilihnya secara eksplisit.

### 8.14 Laporan
Dibuat kapan saja (`migrator report`), juga otomatis saat batch selesai. File di `data/reports/<batch>/`: `laporan.html`, `laporan.json`, `laporan.csv`, plus ringkasan terminal.

Isi:
- Ringkasan batch: sumber, tujuan, waktu mulai/selesai, jumlah sukses/gagal/tertahan/manual.
- Tabel website: domain asal → tujuan (domain asli atau URL dummy), tipe, ukuran, status file/DB/verifikasi/DNS/SSL/email, catatan.
- Daftar website `DUMMY_ONLY` beserta URL dummy-nya (contoh: *domain mati → `xxx.hostingersite.com`*).
- Tabel email: mailbox, jumlah pesan sumber vs tujuan, status.
- DNS sebelum dan sesudah per domain.
- Tindakan manual yang masih tertunda.
- Lokasi backup lokal.
- **Daftar akun yang masih memasang SSH key migrator** + pengingat menghapusnya di hPanel.
- Daftar error lengkap dengan saran tindakan.

Status di laporan harus jujur: website tidak boleh ditulis "Selesai" bila email/SSL/DNS belum terverifikasi.

---

## 9. State Machine per Website

```
DISCOVERED → PLANNED → BACKUP_RUNNING → BACKUP_DONE
→ DEST_PROVISIONING → DEST_READY → RESTORE_RUNNING → RESTORE_DONE
→ VERIFYING → VERIFIED
   ├─(domain expired/disabled)→ DUMMY_ONLY  [akhir]
   └→ CUTOVER_RUNNING → CUTOVER_DONE → SSL_ACTIVE | SSL_PENDING
      → EMAIL_RUNNING → EMAIL_DONE | NO_EMAIL
      → CLEANUP_READY → CLEANED → COMPLETED
```

Status samping (bisa terjadi di tahap mana pun):
- `WAITING_MANUAL` — menunggu checkpoint manual (kode checkpoint dicatat).
- `BLOCKED` — butuh keputusan operator (token dicabut, domain bentrok, kuota habis).
- `FAILED` — gagal setelah retry habis; bisa diulang dengan `migrator retry`.

Aturan:
- Setiap transisi ditulis dalam satu transaksi SQLite bersama hasil validasi langkahnya. Status hanya maju setelah validasi lulus.
- Kegagalan satu website tidak menghentikan website lain.
- Status batch dihitung dari semua item.

## 10. Recovery, Retry, Idempotensi

| Kondisi | Tindakan |
|---|---|
| Internet putus / timeout | Retry dengan exponential backoff + jitter (maks 8 kali), lalu `FAILED` |
| HTTP 429 | Hormati `Retry-After`; rate limiter per token (default konservatif 60 request/menit, menyesuaikan header) |
| HTTP 5xx | Retry terbatas |
| HTTP 401/403 | `BLOCKED` (token bermasalah) |
| Worker mati / Windows restart | Saat `migrator start`/`resume`, langkah berstatus `*_RUNNING` diperiksa ulang lalu dilanjutkan dari checkpoint terakhir |
| Upload/download putus | Ulangi artefak itu saja |
| Import DB putus | Kosongkan DB tujuan (drop semua tabel) lalu import ulang |
| Operasi create timeout | Cek dulu apakah resource sudah terbentuk; jangan buat ganda |
| Fingerprint SSH berubah | `BLOCKED` |

Konkurensi default: 2 website paralel, 1 transfer besar per website, 1 import DB per akun tujuan.

### 10.1 Worker background (Windows)
- `migrator start` menjalankan proses worker terpisah (detached) lalu kembali ke prompt. Menutup Git Bash tidak menghentikan worker.
- Lock: tabel `worker_lock` dengan PID + lease (diperbarui tiap 30 detik, kedaluwarsa 2 menit) mencegah dua worker berjalan.
- `migrator stop` menghentikan dengan rapi (menyelesaikan langkah kecil yang sedang berjalan, menandai langkah besar untuk diulang).
- `migrator autostart on|off`: mendaftarkan/menghapus tugas Windows Task Scheduler (`schtasks.exe`) agar worker berjalan saat login.

## 11. Skema SQLite (ringkas)

| Tabel | Isi utama |
|---|---|
| `credentials` | alias, tanggal dibuat, status terakhir (tanpa token) |
| `hosting_accounts` | id `uXXX`, alias, paket, info SSH (host, port, user, fingerprint), status SSH |
| `capabilities` | akun, kode operasi, status, catatan, waktu uji |
| `websites` | akun, domain, root path, tipe, ukuran, status domain, id API |
| `databases` | website, nama, user, host, ukuran |
| `mailboxes` | domain, alamat, kuota, jumlah pesan |
| `dns_snapshots` | domain, JSON record, id snapshot API, waktu |
| `batches` | id batch, sumber, tujuan, status, waktu |
| `items` | batch, website sumber, website tujuan, dummy domain, state, retry, error terakhir |
| `steps` | item, nama langkah, status, percobaan, checkpoint JSON, waktu, error |
| `artifacts` | item, jenis, path lokal, ukuran, sha256, tervalidasi |
| `verifications` | item, jenis cek, hasil, detail |
| `manual_tasks` | item, kode checkpoint, instruksi, status, dikonfirmasi pada |
| `approvals` | batch, jenis (hapus email / cleanup), daftar item, waktu |
| `audit_log` | waktu, aktor (worker/operator), aksi, target, hasil — tanpa rahasia |
| `worker_lock` | pid, lease_until |

Pengaturan: `PRAGMA journal_mode=WAL; foreign_keys=ON; busy_timeout=5000;`. Migrasi skema bernomor di `migrations/sqlite/`.

## 12. Perintah CLI

```
migrator                       # wizard
migrator account add|list|test|remove
migrator ssh setup [akun]      # generate key + wizard tempel + tes
migrator ssh test [akun]
migrator audit [alias]         # capability audit
migrator inventory [alias] [--refresh]
migrator plan [--dry-run]
migrator start | stop | status | resume
migrator retry [--failed] [--item <domain>]
migrator pending               # daftar tindakan manual
migrator confirm <KODE> <domain>
migrator cutover [--batch <id>]
migrator cleanup [--batch <id>]
migrator report [--batch <id>] [--html|--json|--csv]
migrator autostart on|off
migrator version
```

Semua teks CLI dalam bahasa Indonesia. Exit code: 0 sukses, 1 error, 2 butuh tindakan operator.

## 13. Konfigurasi Default (`config.yaml`)

```yaml
storage:
  data_dir: ./data
api:
  base_url: https://developers.hostinger.com
  timeout_detik: 60
  rate_per_menit: 60
  retry_maks: 8
worker:
  website_paralel: 2
  import_db_paralel_per_akun: 1
backup:
  retensi_hari: 30
  final_sync_sebelum_cutover: true
verifikasi:
  timeout_http_detik: 30
  min_asset_dicek: 3
ssl:
  batas_tunggu_menit: 60
cleanup:
  wajib_ssl_aktif: true
```

Token dan password tidak pernah ada di file ini.

## 14. Keamanan & Integritas Data

1. Token API & password SSH cadangan → Windows Credential Manager.
2. Private key SSH hanya bisa dibaca user Windows saat ini.
3. Log dan laporan disaring: token, password DB, password mailbox, isi `.env` tidak boleh muncul (uji otomatis wajib).
4. Backup tidak dienkripsi (keputusan 9). Tool menampilkan peringatan bila folder data berada di dalam folder OneDrive/Google Drive/Dropbox.
5. Ekstraksi arsip di sisi lokal (bila ada) dilindungi dari path traversal dan symlink keluar folder.
6. Operasi destruktif hanya tiga: hapus email lama, ganti domain sumber, cleanup — semuanya lewat konfirmasi dan tercatat di `audit_log`.
7. Backup baru tidak menimpa backup lama; disimpan per batch dan dibersihkan sesuai retensi.
8. SSH host key diverifikasi (TOFU + simpan fingerprint).

## 15. Fase Implementasi untuk Claude Code

Setiap fase diakhiri dengan Definition of Done (DoD). Jangan mulai fase berikutnya sebelum DoD lulus dan dilaporkan ke operator.

### Fase 0 — Riset kontrak API (tanpa kode produksi)
- Unduh `openapi.json` resmi, petakan endpoint yang berlaku untuk **Shared Hosting** (bukan agency-hosting) ke `internal/hostinger/contracts/*.yaml`.
- Tandai tiap operasi di bagian 8.3: tersedia / tidak / belum jelas.
- Kumpulkan dokumentasi API email dan cocokkan dengan langkah yang pernah dipakai operator.
- **DoD:** dokumen `docs/api-shared-hosting.md` berisi tabel operasi + endpoint + sumber; daftar pertanyaan untuk operator.

### Fase 1 — Fondasi
- Proyek Go, SQLite + migrasi skema, config, logging dengan redaksi rahasia, Credential Manager, API client + rate limiter + retry, `account add/list/test/remove`.
- **DoD:** build `migrator.exe` jalan di Git Bash; token valid/invalid terdeteksi; token tidak ada di DB/log (dibuktikan test).

### Fase 2 — SSH & Capability Audit
- Generate key, wizard tempel, tes koneksi, known_hosts, fallback password; `audit`.
- **DoD:** di akun uji Single dan Premium/Business, audit menghasilkan tabel kapabilitas yang akurat.

### Fase 3 — Inventory, Detector, Planner
- **DoD:** `inventory` menampilkan root path, tipe, ukuran, DB, mailbox untuk semua website akun uji; `plan --dry-run` menolak rencana bila kuota kurang.

### Fase 4 — Backup, Provisioning, Restore, Verifikasi (satu website WordPress non-produksi)
- Termasuk worker background, state machine, resume.
- **DoD:** satu website WordPress uji berpindah ke dummy di akun tujuan, verifikasi `PASS`, dan proses tetap selesai benar saat dimatikan paksa di tengah backup dan di tengah restore. Perilaku folder saat ganti domain terdokumentasi.

### Fase 5 — Tipe lain + Cutover
- Laravel, PHP native, statis, Node.js; snapshot DNS, ganti domain sumber, pasang domain tujuan, DNS, SSL, rollback.
- **DoD:** satu domain uji ter-cutover penuh dan bisa di-rollback; satu website expired berakhir `DUMMY_ONLY`.

### Fase 6 — Email
- **DoD:** satu domain uji dengan 2 mailbox (salah satunya berisi lampiran besar) berpindah, jumlah pesan cocok, password lama tetap bisa login.

### Fase 7 — Cleanup & Laporan
- **DoD:** cleanup hanya menghapus item yang lolos gerbang; laporan HTML/JSON/CSV lengkap termasuk daftar akun yang masih memasang SSH key.

### Fase 8 — Uji skala & rilis
- Batch 10–20 website non-kritis, uji gangguan (putus internet, restart Windows, token dicabut).
- **DoD:** semua kriteria di bagian 16 lulus; `README.md` berbahasa Indonesia untuk operator.

## 16. Kriteria Penerimaan

| ID | Kriteria |
|---|---|
| AC-01 | Banyak token dengan alias bisa didaftarkan; token tidak pernah muncul di DB, log, laporan |
| AC-02 | Semua akun hosting di balik token ditemukan |
| AC-03 | Inventaris menampilkan root path, tipe, ukuran, DB, mailbox |
| AC-04 | SSH key satu untuk semua akun berfungsi; fallback password berfungsi |
| AC-05 | Capability Audit tidak pernah menandai `UNKNOWN` sebagai `SUPPORTED` |
| AC-06 | `--dry-run` tidak mengubah apa pun |
| AC-07 | Backup file + DB tervalidasi checksum dan jumlah baris |
| AC-08 | Struktur file tujuan identik dengan sumber (termasuk file tersembunyi) |
| AC-09 | Website expired/disabled tetap dipindah ke dummy |
| AC-10 | Verifikasi otomatis memilih metode sesuai tipe |
| AC-11 | Cutover mempertahankan record DNS pihak ketiga dan bisa di-rollback |
| AC-12 | Email pindah dengan jumlah pesan cocok dan password lama berlaku |
| AC-13 | Checkpoint manual menghentikan item, item lain tetap jalan, lanjut setelah `confirm` |
| AC-14 | Resume setelah worker dimatikan paksa / Windows restart tanpa duplikasi resource |
| AC-15 | Cleanup hanya untuk item yang lolos gerbang, dengan satu konfirmasi per batch |
| AC-16 | Laporan lengkap dan jujur dalam bahasa Indonesia |
| AC-17 | Berjalan di Git Bash Windows 10 dan 11 tanpa instalasi tambahan selain Git for Windows |

## 17. Kasus Uji Wajib

- Website domain expired; website disabled.
- Website tanpa DB; website dengan 2 DB.
- WordPress besar (>2 GB, banyak file kecil).
- Laravel dengan kode di luar `public_html`.
- File tersembunyi (`.htaccess`, `.env`, `.well-known`).
- Website butuh versi PHP tertentu.
- Internet putus saat download; saat upload; saat import DB.
- Windows restart saat backup.
- Token dicabut di tengah batch.
- Paket Single tanpa SSH (jalur manual).
- DNS dengan MX/TXT pihak ketiga; domain dengan NS eksternal.
- Domain sudah ada di akun tujuan (harus `BLOCKED`, tidak ditimpa).
- Kuota website tujuan tidak cukup.
- Email dengan lampiran besar dan banyak folder.

## 18. Risiko & Pertanyaan Terbuka

| # | Risiko / pertanyaan | Penanganan |
|---|---|---|
| R1 | API Shared Hosting mungkin tidak mendukung buat website, ganti domain, buat DB, SSL | Fase 0 memetakan; fallback checkpoint manual sudah didesain |
| R2 | Paket Single mungkin tanpa SSH atau dengan batasan | Audit + jalur `MANUAL_BACKUP` |
| R3 | Perilaku folder saat domain website diganti belum diketahui | Dibuktikan di Fase 4 sebelum cutover diimplementasikan |
| R4 | Endpoint email yang dipakai operator sebelumnya belum terdokumentasi | **Operator diminta menunjukkan langkah/endpoint yang pernah dipakai sebelum Fase 6** |
| R5 | Email lama dihapus sebelum tujuan siap (keterbatasan Hostinger) | Backup tervalidasi + konfirmasi khusus + downtime diterima operator |
| R6 | Masa berlaku/perilaku subdomain gratis | Diuji di Fase 4; item dummy dicek ulang sebelum cutover |
| R7 | API masih berkembang | Contract registry + test kontrak; update adapter saja bila berubah |
| R8 | Satu SSH key untuk semua akun | Izin file ketat + pengingat penghapusan key di laporan |

## 19. Aturan Kerja Agent (untuk Claude Code)

Salin bagian ini ke `CLAUDE.md` di root repo.

1. **Bahasa:** semua teks CLI, laporan, README, dan komentar penting dalam bahasa Indonesia. Nama variabel/fungsi tetap bahasa Inggris.
2. **Jangan mengarang endpoint.** Setiap pemanggilan API harus merujuk entri di `internal/hostinger/contracts/*.yaml` yang bersumber dari `openapi.json` resmi. Endpoint `agency-hosting` tidak boleh dipakai untuk Shared Hosting kecuali terbukti berfungsi dan dicatat di `docs/decisions.md`.
3. **Jangan memakai API internal/tidak resmi hPanel.** Bila operasi tidak tersedia, buat checkpoint manual.
4. **Uji hanya ke akun/website uji** yang ditunjuk operator. Jangan pernah menjalankan operasi tulis ke akun produksi tanpa instruksi eksplisit.
5. **Operasi destruktif** (hapus website/DB/email, ganti domain, ubah DNS) wajib lewat fungsi gerbang yang meminta konfirmasi dan menulis `audit_log`. Tidak ada jalan pintas di kode.
6. **Rahasia:** jangan pernah mencetak token, password, atau isi `.env`/`wp-config.php` ke log, test output, atau commit. Gunakan fixture palsu.
7. **Dependency minimal** sesuai tabel 5.1. Tambahan harus dicatat alasannya di `docs/decisions.md`.
8. **Setiap fase:** tulis test, jalankan `go vet` dan `go test ./...`, build `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`, lalu laporkan hasil DoD ke operator sebelum lanjut.
9. **Jujur soal status:** jangan menandai langkah selesai hanya karena HTTP 200; selalu validasi hasil nyata (checksum, jumlah baris, jumlah pesan, respons halaman).
10. **Bila ragu** tentang perilaku Hostinger, berhenti dan tanyakan operator alih-alih menebak.
