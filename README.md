# Hostinger Migration Manager (`migrator`)

Alat baris perintah (CLI) untuk memindahkan website beserta layanan terkait — file, database, DNS, SSL, dan email — dari satu akun Hostinger Shared Hosting ke akun Hostinger lain secara otomatis, aman, bisa dilanjutkan setelah gangguan, dan menghasilkan laporan lengkap dalam bahasa Indonesia.

> **Status: v1.0.0-rc8.** Semua fase PRD (0–8) selesai di sisi kode dan lulus pengujian otomatis memakai server tiruan (Hostinger tidak menyediakan sandbox). **Belum diuji di akun Hostinger nyata.** Ikuti [`docs/uji-lapangan.md`](docs/uji-lapangan.md) di akun uji sebelum dipakai untuk website klien.

---

## 1. Unduh

Binary Windows x64 siap pakai: [`dist/migrator.exe`](dist/migrator.exe). Checksum di [`dist/SHA256SUMS`](dist/SHA256SUMS).

```bash
cd dist && sha256sum -c SHA256SUMS
```

Hasil harus `migrator.exe: OK`.

## 2. Persiapan

1. Windows 10/11 dengan **Git for Windows** (Git Bash). Tidak perlu Python, Node.js, Docker, WSL, OpenSSH, atau MySQL client.
2. Buat folder kerja **di luar OneDrive/Google Drive/Dropbox** (backup tidak dienkripsi), mis. `C:\migrasi`. Salin `migrator.exe` ke sana.
3. Siapkan **API token** hPanel untuk akun sumber dan tujuan (hPanel → Profil → API).
4. Pastikan **SSH aktif** di hPanel → Advanced → SSH Access untuk setiap akun hosting (paket tanpa SSH tetap bisa, lewat jalur backup manual).

Cara menjalankan:

- **Klik dua kali `migrator.exe`** — jendela konsol terbuka dan wizard muncul. Seluruh alur migrasi bisa dikerjakan dari menu ini.
- Atau dari Git Bash/CMD di folder kerja: `./migrator.exe` (wizard) atau `./migrator.exe <perintah>` (perintah langsung, lihat bagian 7).

Tampilan awal wizard:

```
==================================
        INFORMASI PROGRAM
==================================

 Nama Program : Hostinger Migrator
 Pembuat      : Suwahadi
 Versi        : 1.0.0-rc8
 Platform     : Windows 10/11 64bit

==================================
  Dibuat oleh: Suwahadi (c) 2026
==================================

MENU UTAMA — HOSTINGER MIGRATION MANAGER
 1. Kelola API token
 2. Siapkan SSH key & audit
 3. Scan akun (inventaris)
 4. Buat rencana & mulai migrasi
 5. Status migrasi
 6. Tindakan manual (lihat & konfirmasi)
 7. Cutover domain
 8. Pindahkan email
 9. Bersihkan akun sumber (cleanup)
10. Laporan
11. Kendali worker & pemulihan
 0. Keluar
Pilih:
```

Di jendela konsol dan Git Bash, layar dibersihkan setiap pindah menu dan hasil tindakan tampil sampai Anda menekan ENTER. Warna membantu membaca: judul biru, sukses dan konfirmasi hijau, hapus dan error merah, peringatan kuning. Output yang dialihkan ke file tetap polos.

Keluar dari wizard tidak menghentikan worker yang sedang berjalan di background. Bila terjadi error saat program dibuka (mis. `config.yaml` salah), jendela menunggu ENTER agar pesan sempat dibaca.

Bila token terlihat saat diketik, jalankan lewat `winpty ./migrator.exe`.

Saat pertama kali dijalankan, tool membuat `config.yaml` dan folder `data/` (database SQLite, log, SSH key, backup, laporan).

## 3. Alur Migrasi Langkah demi Langkah

Kolom **Menu** adalah nomor di wizard; kolom **Perintah** untuk yang lebih suka mengetik.

| # | Langkah | Menu / Perintah | Catatan |
|---|---|---|---|
| 1 | Daftarkan token sumber dan tujuan | Menu 1 · `migrator account add` | Token disimpan di Windows Credential Manager, akun `uXXXXXXXX` ditemukan otomatis |
| 2 | Siapkan SSH | Menu 2 · `migrator ssh setup` | Tempel public key yang ditampilkan di hPanel tiap akun, isi IP/port/username |
| 3 | Audit kapabilitas | Menu 2 → 4 · `migrator audit` | Read-only. `SUPPORTED` hanya untuk yang teruji nyata |
| 4 | Inventaris | Menu 3 · `migrator inventory` | Root path, tipe, ukuran, database, mailbox, status domain |
| 5 | Rencana (uji dulu) | `migrator plan --dry-run` | Semua pemeriksaan + urutan langkah, tanpa mengubah apa pun |
| 6 | Rencana (simpan) | Menu 4 · `migrator plan` | Di isian `Cari:` ketik sebagian domain atau id akun, lalu pilih nomor dari hasil (`A` = semua hasil yang tampil). Domain lengkap dipisah koma langsung terpilih. ENTER kosong = selesai memilih. Ketik `LANJUT` (bila ada peringatan) lalu `YAKIN`; worker langsung berjalan di background |
| 7 | Pantau & konfirmasi | Menu 5, Menu 6 · `migrator status`, `migrator pending`, `migrator confirm <KODE> <domain>` | Di Menu 6 cukup pilih nomor tindakan yang sudah dikerjakan |
| 8 | Cutover domain | Menu 7 · `migrator cutover` | Ketik `CUTOVER`; Anda akan diminta mengganti domain sumber ke domain sementara di hPanel |
| 9 | Pindah email | Menu 8 · `migrator email` | Ketik `HAPUS EMAIL`; password mailbox baru: `migrator email passwords` |
| 10 | Bersihkan sumber | Menu 9 · `migrator cleanup` | Hanya item yang lolos semua gerbang; ketik `HAPUS` |
| 11 | Laporan | Menu 10 · `migrator report` | `data/reports/<batch>/laporan.html`, `.json`, `.csv` |
| 12 | Penutup | — | Hapus SSH key `hostinger-migrator` di hPanel semua akun, `migrator ssh remove-password <akun>`, `migrator autostart off` |

### Apa yang terjadi otomatis per website

1. **Backup** via SSH (streaming, tanpa file sementara di server): `tar` seluruh folder `/home/<u>/domains/<domain>/` + `mysqldump` per database. Divalidasi SHA-256, gzip, jumlah file, dan **jumlah baris per tabel**.
2. **Provisioning**: website dummy dibuat di akun tujuan dengan subdomain gratis (`*.hostingersite.com`).
3. **Restore**: upload + ekstrak (struktur identik, termasuk file tersembunyi), buat database, import, cocokkan jumlah baris, perbarui kredensial di `wp-config.php`/`.env` (salinan asli `.migrator-orig`), samakan versi PHP.
4. **Verifikasi**: HTTP, tanda error, judul halaman, ≥3 asset, query database, `error_log` baru.
5. Website dengan domain **expired/disabled** tetap dipindah dan berakhir **`DUMMY_ONLY`** (URL dummy tercantum di laporan).
6. Setelah `cutover`: backup final, snapshot DNS, domain dipasang di tujuan, A/AAAA diubah ke IP tujuan (MX/TXT/CNAME pihak ketiga dipertahankan), SSL, verifikasi akhir.

## 4. Tindakan Manual (`migrator pending`)

| Kode | Kapan muncul | Yang dikerjakan operator |
|---|---|---|
| `MANUAL_BACKUP` | SSH sumber tidak tersedia | Unduh backup file + DB dari hPanel ke folder yang ditunjukkan |
| `MANUAL_CREATE_SITE` | API menolak membuat website dummy | Buat website kosong di hPanel tujuan, `confirm ... --nilai <domain-sementara>` |
| `MANUAL_CREATE_DB` | API menolak membuat database | Buat DB dengan nama/user yang ditampilkan; password: `migrator pending --tampilkan-rahasia` |
| `MANUAL_PHP` | Versi PHP tidak bisa disamakan lewat API | Ubah versi PHP website dummy di hPanel |
| `MANUAL_NODEJS` | Website Node.js | Atur aplikasi Node.js (versi, entry, build) di hPanel tujuan |
| `MANUAL_SOURCE_RENAME` | Saat cutover | Ganti domain website sumber ke domain sementara, `confirm ... --nilai <domain-sementara>` |
| `MANUAL_DEST_DOMAIN` | API menolak memasang domain di tujuan | Tambahkan domain sebagai parked/alias di website dummy |
| `MANUAL_DNS` | Nameserver eksternal / zone di luar token | Ubah A/AAAA di penyedia DNS sesuai instruksi |
| `MANUAL_EMAIL_PASSWORD` | API menolak mengganti password mailbox | Ganti password manual dengan password dari `migrator email passwords` |
| `MANUAL_EMAIL_SOURCE_REMOVE` | Setelah mailbox sumber dihapus | Lepaskan layanan email domain di akun sumber |
| `MANUAL_EMAIL_SETUP` | Sebelum mailbox dibuat ulang | Aktifkan email domain di akun tujuan (jangan buat mailbox) |
| `MANUAL_EMAIL_CREATE` | API menolak membuat mailbox | Buat mailbox di hPanel tujuan dengan password baru |
| `MANUAL_EMAIL_DNS` | Setelah restore email | Samakan MX/SPF/DKIM/DMARC dengan nilai di hPanel tujuan |
| `MANUAL_CLEANUP` | API menolak menghapus website sumber | Hapus website + DB sumber di hPanel (jangan hapus domain) |

## 5. Status Website

`PLANNED → BACKUP_RUNNING → BACKUP_DONE → DEST_PROVISIONING → DEST_READY → RESTORE_RUNNING → RESTORE_DONE → VERIFYING → VERIFIED → (DUMMY_ONLY) | CUTOVER_RUNNING → CUTOVER_DONE → SSL_ACTIVE/SSL_PENDING → EMAIL_RUNNING → EMAIL_DONE/NO_EMAIL → CLEANUP_READY → CLEANED → COMPLETED`

Status samping: `WAITING_MANUAL` (lihat `pending`), `BLOCKED` (butuh keputusan operator, mis. token dicabut, fingerprint SSH berubah, domain bentrok), `FAILED` (percobaan habis, ulangi dengan `migrator retry`).

## 6. Gangguan dan Pemulihan

| Kejadian | Yang dilakukan tool | Yang dilakukan operator |
|---|---|---|
| Internet putus | Retry dengan backoff; artefak yang terputus diulang dari awal | Tidak ada; bila `FAILED`: `migrator retry --failed` |
| Git Bash ditutup | Worker tetap berjalan di background | — |
| Windows restart | Langkah terputus diperiksa ulang | `migrator resume`, atau aktifkan `migrator autostart on` |
| Token dicabut | Item `BLOCKED` | Token baru: `account remove` + `account add`, lalu `migrator retry --failed --termasuk-blocked` |
| Fingerprint SSH berubah | Item `BLOCKED` | Periksa di hPanel, `migrator ssh setup <akun> --terima-host-baru` |
| Ingin menghentikan | — | `migrator stop` (langkah besar diulang saat `start`/`resume`) |
| Cutover bermasalah | — | `migrator rollback <domain>` (sebelum email dipindah dan sebelum cleanup) |

## 7. Daftar Perintah

```
migrator                                  wizard menu utama
migrator account add|list|test|remove     kelola API token
migrator ssh setup|test [akun]            siapkan/uji SSH
migrator ssh remove-password <akun>       hapus password SSH cadangan
migrator audit [alias]                    audit kapabilitas (read-only)
migrator inventory [alias] [--refresh] [--cepat] [--akun uXXX]    inventaris (read-only); --cepat tanpa hitung ukuran folder; --akun pindai ulang satu akun
migrator plan [--dry-run]                 buat rencana batch
migrator start | stop | status | resume   kendali worker background
migrator status --item <domain>           detail satu website
migrator retry --failed | --item <domain> [--termasuk-blocked]
migrator pending [--tampilkan-rahasia]    tindakan manual tertunda
migrator confirm <KODE> <domain> [--nilai x]
migrator cutover [--batch] [--item]       setujui cutover (CUTOVER)
migrator rollback <domain>                batalkan cutover (ROLLBACK)
migrator email [--batch]                  setujui pemindahan email (HAPUS EMAIL)
migrator email passwords [--domain]       tampilkan password mailbox baru
migrator cleanup [--termasuk-dummy] [--kecualikan-ssl d1,d2]
migrator report [--batch] [--html|--json|--csv]
migrator autostart on|off                 Windows Task Scheduler
migrator version
```

Kode keluar: `0` sukses, `1` error, `2` butuh tindakan operator.

## 8. Konfigurasi (`config.yaml`)

```yaml
storage:
  data_dir: ./data
api:
  base_url: https://developers.hostinger.com
  timeout_detik: 60
  rate_per_menit: 80
  rate_burst: 5
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
email:
  imap_host: imap.hostinger.com:993
  imap_tls: true
```

Rencana tidak memeriksa kapasitas akun tujuan (jatah website, sisa disk, jatah database); kapasitas dikelola sendiri di hPanel. Bila kapasitas tujuan ternyata penuh, website terkait berhenti saat migrasi berjalan. Setelah kapasitas ditambah, item `FAILED` dilanjutkan dengan `migrator retry --failed`, sedangkan item yang menunggu tindakan manual (mis. `MANUAL_CREATE_SITE` bila Hostinger menolak membuat website) diselesaikan lewat `migrator pending` lalu `migrator confirm`. Kunci `kuota_paket` di `config.yaml` lama masih diterima tetapi tidak dipakai lagi.

Batas API: Hostinger mengizinkan 90 panggilan per 60 detik per token. `rate_per_menit: 80` ditambah `rate_burst: 5` menjaga jumlah panggilan di bawah 85 per menit. `config.yaml` yang dibuat versi sebelum `1.0.0-rc5` masih berisi `rate_per_menit: 60`; ubah ke `80` agar lebih cepat (atau hapus `config.yaml` supaya dibuat ulang).

Inventaris memindai 4 website sekaligus per akun dan menampilkan kemajuan per website (`[3/20] domain  tipe  ukuran  durasi`). Bila website memiliki sangat banyak file, pilih scan cepat (menu 3 lalu jawab `y` pada pertanyaan scan cepat, atau `--cepat`): ukuran folder dan jumlah file tidak dihitung, tipe website tetap dideteksi.

Pemindaian inventaris:

| Cara | Yang dipindai |
|---|---|
| Menu 3, "Pindai ulang: T" / `migrator inventory` | Hanya akun yang belum punya inventaris (misalnya setelah pemindaian terputus). Akun yang sudah selesai dilewati. Bila semua sudah ada, hasil tersimpan langsung ditampilkan. |
| Menu 3, "Pindai ulang: y", akun ENTER / `--refresh` | Semua akun di semua token, dari awal. |
| Menu 3, "Pindai ulang: y", pilih akun (nomor, id, atau cari dengan sebagian id/domain) / `--akun uXXX` | Satu akun hosting saja. |

Di semua layar pilih akun (akun tujuan, menu 3, menu 2 SSH), daftar lengkap hanya tampil bila akunnya 20 atau kurang. Bila lebih, ketik sebagian id akun atau domain untuk mencari, lalu pilih nomor dari hasil. Hasil pencarian tampil paling banyak 30 baris.

Inventaris belum mendeteksi perubahan per website: akun yang dipindai ulang selalu dipindai penuh.

Token dan password tidak pernah ada di file ini.

## 9. Keamanan

- Token API, password SSH cadangan, password database tujuan, dan password mailbox baru disimpan di **Windows Credential Manager**, tidak pernah di database, log, laporan, atau config (dibuktikan test otomatis).
- Satu pasangan SSH key ed25519 untuk semua akun; private key hanya bisa dibaca user Windows saat ini. Laporan selalu mencantumkan akun yang masih memasang key ini.
- Operasi destruktif (ganti password/hapus email lama, ubah DNS/domain, cleanup) hanya lewat konfirmasi per batch dan tercatat di `audit_log`.
- Backup lokal **tidak dienkripsi**; simpan folder data di tempat aman.

## 10. Keterbatasan yang Diketahui

- Hostinger tidak punya endpoint ganti domain, tambah SSH key, kuota, dan restore email untuk Shared Hosting; bagian itu memakai langkah manual atau alternatif (lihat `docs/decisions.md`).
- Password mailbox lama **tidak** bisa dipertahankan; tool membuat password baru (D-009).
- Verifikasi dengan header `Host` ke IP tujuan sebelum cutover kemungkinan tidak berhasil di server Hostinger; tool otomatis memakai fallback (D-032).
- Restore email ke INBOX bisa menghasilkan satu pesan ganda bila koneksi putus tepat saat penyimpanan (D-044).
- Email untuk domain tanpa website belum dicakup.

## 11. Untuk Pengembang

Butuh Go 1.26+.

```bash
go vet ./...
go test ./...
go test -race ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X hostinger-migrator/internal/cli.Version=1.0.0-rc8" -o dist/migrator.exe ./cmd/migrator
```

Pengujian memakai server tiruan in-process: API Hostinger, SSH yang menjalankan perintah shell sungguhan dengan `mysql`/`mysqldump` tiruan, HTTP, dan IMAP (`tests/`).

`go test ./...` lengkap harus dijalankan di **Linux/macOS** (atau container Linux). Di Windows, server SSH tiruan meneruskan path `C:\...` ke bash sehingga test pipeline, cutover, email, worker, cleanup, dan integrasi gagal. Kode produksi tidak terpengaruh (D-050).

## 12. Dokumen Terkait

- [`docs/PRD.md`](docs/PRD.md) — spesifikasi produk.
- [`docs/api-shared-hosting.md`](docs/api-shared-hosting.md) — pemetaan API Hostinger untuk Shared Hosting.
- [`docs/decisions.md`](docs/decisions.md) — catatan keputusan teknis.
- [`docs/uji-lapangan.md`](docs/uji-lapangan.md) — daftar uji di akun Hostinger nyata.
- [`CLAUDE.md`](CLAUDE.md), [`AGENT.md`](AGENT.md) — aturan untuk agent AI.
