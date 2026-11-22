# Hostinger Migration Manager (`migrator`)

Alat baris perintah (CLI) untuk memindahkan website beserta layanan terkait — file, database, DNS, SSL, dan email — dari satu akun Hostinger Shared Hosting ke akun Hostinger lain secara otomatis, aman, bisa dilanjutkan setelah gangguan, dan menghasilkan laporan lengkap dalam bahasa Indonesia.

> **Status:** Fase 3 selesai — kelola token, setup SSH, audit kapabilitas, inventaris, dan pembuatan rencana migrasi sudah bisa dipakai. Eksekusi migrasi menyusul di Fase 4. Fitur migrasi dikerjakan bertahap di fase berikutnya. Spesifikasi lengkap ada di [`docs/PRD.md`](docs/PRD.md).

## Fitur Utama (rencana v1)

- Mendaftarkan banyak API token Hostinger dengan alias, disimpan aman di Windows Credential Manager.
- Menemukan semua akun hosting (`uXXXXXXXX`) di balik setiap token.
- Inventaris website, root path, database, domain, DNS, email, dan cron.
- Deteksi otomatis tipe website: WordPress, Laravel, CodeIgniter, Joomla, PHP native, statis, Node.js.
- Backup lokal via SSH (streaming, tanpa file sementara di server) dengan validasi checksum dan jumlah baris tabel.
- Restore ke domain dummy (`*.hostingersite.com`) di akun tujuan, verifikasi otomatis, lalu cutover domain.
- Snapshot DNS dan rollback sebelum cleanup.
- Migrasi email (backup → hapus lama → buat baru → restore) dengan password lama tetap berlaku.
- Website dengan domain expired/disabled tetap dipindah ke domain dummy (`DUMMY_ONLY`).
- Berjalan di background, auto-resume setelah internet putus, aplikasi ditutup, atau Windows restart.
- Laporan akhir: terminal, HTML, JSON, CSV.

## Kebutuhan Sistem

- Windows 10 atau 11.
- Git for Windows (Git Bash). Tidak perlu Python, Node.js, Docker, WSL, OpenSSH, atau MySQL client.
- Akun Hostinger Shared Hosting (Single, Premium, Business) dengan API token.
- Akses SSH di hPanel (paket tanpa SSH memakai jalur manual).

## Unduh

Binary siap pakai untuk Windows x64 ada di [`dist/migrator.exe`](dist/migrator.exe). Checksum SHA-256 ada di [`dist/SHA256SUMS`](dist/SHA256SUMS). Cek di Git Bash dengan:

```bash
cd dist && sha256sum -c SHA256SUMS
```

## Build

Butuh Go 1.26 atau lebih baru (hanya untuk build, tidak perlu di komputer operator).

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X hostinger-migrator/internal/cli.Version=0.1.0" -o migrator.exe ./cmd/migrator
```

Uji:

```bash
go vet ./...
go test ./...
```

## Yang Sudah Bisa Dipakai (Fase 1)

```bash
./migrator.exe account add                 # tambah token (input tersembunyi), validasi, temukan akun hosting
./migrator.exe account list                # daftar token + akun hosting uXXXXXXXX
./migrator.exe account test xyz            # uji ulang token
./migrator.exe account remove xyz          # hapus token dari komputer ini saja
./migrator.exe version
```

## Yang Sudah Bisa Dipakai (Fase 2)

```bash
./migrator.exe ssh setup                   # buat SSH key, pandu tempel di hPanel, tes koneksi semua akun
./migrator.exe ssh setup u494983133        # atur ulang satu akun
./migrator.exe ssh test                    # uji ulang SSH + SFTP semua akun
./migrator.exe ssh remove-password u494983133   # hapus password SSH cadangan
./migrator.exe audit                       # audit kapabilitas API + SSH (read-only) semua token
./migrator.exe audit xyz                   # audit satu token
```

Langkah setup SSH per akun:

1. hPanel → Advanced → SSH Access → pastikan SSH **Enabled**.
2. Klik **Add SSH Key**, tempel baris `ssh-ed25519 ... hostinger-migrator` yang ditampilkan tool (sama untuk semua akun).
3. Ketik `SELESAI`, lalu masukkan IP, port, dan username dari halaman SSH Access (port **tidak** selalu 22).
4. Bila paket tidak mendukung key, ketik `PASSWORD` untuk memakai password SSH (disimpan di Credential Manager).

Status audit: `SUPPORTED` = teruji nyata, `MANUAL` = ada jalur manual, `UNSUPPORTED` = tidak tersedia, `UNKNOWN` = belum terbukti (tidak pernah dianggap `SUPPORTED`).

Setelah migrasi selesai, **hapus SSH key migrator** dari hPanel semua akun (SSH Access → Delete).

## Yang Sudah Bisa Dipakai (Fase 3)

```bash
./migrator.exe inventory                   # inventaris semua token (dipindai bila belum ada)
./migrator.exe inventory xyz --refresh     # pindai ulang token xyz
./migrator.exe plan --dry-run              # wizard rencana + semua pemeriksaan, tanpa menyimpan
./migrator.exe plan                        # wizard rencana, simpan batch setelah ketik YAKIN
./migrator.exe plan --sumber xyz --web domainanda.com,toko.id --tujuan zyx --akun-tujuan u73839202 --dry-run
```

Inventaris menampilkan per website: folder backup (root path), tipe (WordPress, Laravel, CodeIgniter, Joomla, Node.js, PHP native, Statis, Lainnya), ukuran, database, mailbox, versi PHP, serta tanda `[EXPIRED]`, `[DISABLED]`, `[NS EKSTERNAL]`. Hasil juga disimpan ke `data/inventory/<alias>-<akun>.json`. Inventaris tidak pernah mengubah apa pun.

Pemeriksaan rencana: token aktif, inventaris tersedia, bentrok domain di tujuan (item `BLOCKED`, tidak ditimpa), kuota website, sisa disk (ukuran × 1,3), kuota database, SSH sumber dan tujuan. Website dengan domain expired/disabled tetap ikut dan berakhir `DUMMY_ONLY`.

Kuota website per paket diatur di `config.yaml`:

```yaml
kuota_paket:
  hostinger_single:
    website: 1
  hostinger_premium:
    website: 100
  hostinger_business:
    website: 100
```

Saat pertama kali dijalankan, `config.yaml` dan folder `data/` (database, log) dibuat di folder kerja.

Bila token terlihat saat diketik di Git Bash, jalankan lewat `winpty ./migrator.exe` agar input tersembunyi.

## Cara Pakai (rencana lengkap)

```bash
./migrator.exe
```

Menu wizard:

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
```

Perintah langsung:

```
migrator account add|list|test|remove
migrator ssh setup|test [akun]
migrator audit [alias]
migrator inventory [alias] [--refresh]
migrator plan [--dry-run]
migrator start | stop | status | resume
migrator retry [--failed] [--item <domain>]
migrator pending
migrator confirm <KODE> <domain>
migrator cutover [--batch <id>]
migrator cleanup [--batch <id>]
migrator report [--batch <id>] [--html|--json|--csv]
migrator autostart on|off
migrator version
```

Kode keluar: `0` sukses, `1` error, `2` butuh tindakan operator.

## Keamanan

- Token API dan password tidak pernah ditulis ke database, log, laporan, atau file konfigurasi.
- Satu pasangan SSH key ed25519 dibuat oleh tool; laporan selalu mengingatkan akun mana yang masih memasang key tersebut.
- Hanya tiga operasi destruktif (hapus email lama, ganti domain sumber, cleanup), semuanya lewat konfirmasi dan tercatat di audit log.
- Backup lokal **tidak dienkripsi**. Simpan folder data di luar OneDrive/Google Drive/Dropbox.

## Tahapan Pengembangan

| Fase | Cakupan |
|---|---|
| 0 | Riset kontrak API Shared Hosting |
| 1 | Fondasi: proyek Go, SQLite, config, log, Credential Manager, API client, `account` |
| 2 | SSH key & Capability Audit |
| 3 | Inventory, Detector, Planner |
| 4 | Backup, Provisioning, Restore, Verifikasi, worker background |
| 5 | Tipe website lain + Cutover domain, DNS, SSL |
| 6 | Email |
| 7 | Cleanup & Laporan |
| 8 | Uji skala & rilis |

## Dokumen Terkait

- [`docs/PRD.md`](docs/PRD.md) — spesifikasi produk lengkap.
- [`CLAUDE.md`](CLAUDE.md) — aturan tetap untuk Claude Code.
- [`AGENT.md`](AGENT.md) — panduan untuk semua agent AI.
