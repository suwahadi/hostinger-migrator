# AGENT.md — Panduan untuk Semua Agent AI

Panduan ini berlaku untuk agent AI apa pun (Claude Code, Codex, Cursor, Copilot, dan lainnya) yang bekerja di repo `hostinger-migrator`. Isinya sama mengikatnya dengan [`CLAUDE.md`](CLAUDE.md). Bila ada perbedaan, `CLAUDE.md` bagian A (Aturan Operator) yang berlaku.

## 1. Aturan Operator (tidak boleh berubah)

| # | Aturan |
|---|---|
| 1 | Semua percakapan, file Markdown, teks CLI, laporan, dan pesan commit dalam **bahasa Indonesia**. |
| 2 | Push **selalu ke `main`**. Tidak membuat branch baru. Tidak membuat PR kecuali diminta. |
| 3 | **Tanpa anotasi AI** (tanpa `Co-Authored-By`, tanpa tautan sesi, tanpa "Generated with ..."). |
| 4 | Identitas git: `suwahadi` / `suwahadi@gmail.com`. |
| 5 | **Tidak mulai build/coding** sebelum operator berkata **setuju**, per fase. |
| 6 | **Tidak ada komentar di kode** dalam bentuk apa pun (`//`, `/* */`, `#`, `TODO`, `FIXME`, dll.). Hanya direktif build Go yang benar-benar diperlukan yang diizinkan. |
| 7 | Keputusan ditanyakan ke operator dalam bentuk **wizard** (pilihan bertahap). |

## 2. Alur Kerja per Fase

1. Baca `docs/PRD.md` bagian terkait fase.
2. Ajukan rencana fase ke operator dalam bentuk wizard, tunggu kata **setuju**.
3. Kerjakan fase. Tulis test bersamaan dengan kode.
4. Jalankan:
   ```bash
   gofmt -l .
   go vet ./...
   go test ./...
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o migrator.exe ./cmd/migrator
   ```
5. Pastikan tidak ada komentar di kode:
   ```bash
   grep -rnE '^\s*(//|/\*|\*)' --include='*.go' . | grep -vE '//go:(build|embed|generate)'
   ```
   Hasil harus kosong.
6. Commit dengan identitas operator, pesan bahasa Indonesia, tanpa anotasi AI, lalu `git push origin main`.
7. Laporkan hasil Definition of Done (DoD) ke operator. Jangan lanjut fase berikutnya sebelum disetujui.

## 3. Larangan Keras

- Mengarang endpoint API. Semua endpoint harus ada di `internal/hostinger/contracts/*.yaml` dan bersumber dari `openapi.json` resmi.
- Memakai endpoint `agency-hosting` untuk Shared Hosting tanpa bukti dan catatan di `docs/decisions.md`.
- Memakai API internal/tidak resmi hPanel.
- Operasi tulis ke akun produksi tanpa instruksi eksplisit operator.
- Operasi destruktif tanpa fungsi gerbang konfirmasi + `audit_log`.
- Mencetak token, password, isi `.env`/`wp-config.php` ke log, output test, atau commit.
- Menambah dependency di luar tabel 5.1 PRD tanpa alasan tertulis di `docs/decisions.md`.
- Menandai langkah selesai hanya karena HTTP 200 tanpa validasi hasil nyata.

## 4. Struktur Proyek

```
hostinger-migrator/
├── cmd/migrator/main.go
├── configs/default.yaml              config bawaan (di-embed)
├── internal/
│   ├── cli/          perintah cobra, wizard, teks bahasa Indonesia
│   ├── config/       config.yaml
│   ├── store/        SQLite, repository
│   ├── secrets/      Credential Manager, password generator
│   ├── logx/         log JSON/teks dengan penyaringan rahasia
│   ├── hostinger/    API client, rate limiter, contract registry (contracts/*.yaml)
│   ├── sshx/         SSH/SFTP, SSH key, verifikasi fingerprint
│   ├── capability/   audit kapabilitas
│   ├── inventory/    inventaris read-only
│   ├── detector/     deteksi tipe website, baca/ubah kredensial DB
│   ├── planner/      rencana batch dan pemeriksaan
│   ├── gate/         gerbang persetujuan operasi destruktif
│   ├── backup/       tar + mysqldump streaming, manifest, hitung baris
│   ├── provision/    website dummy di akun tujuan
│   ├── restore/      upload, ekstrak, buat DB, import, kredensial, PHP
│   ├── verify/       verifikasi HTTP/DB/file
│   ├── pipeline/     state machine per website
│   ├── cutover/      cutover domain, DNS, SSL, rollback
│   ├── imapx/        klien IMAP minimal
│   ├── email/        migrasi email
│   ├── cleanup/      gerbang cleanup dan penghapusan sumber
│   ├── report/       laporan HTML/JSON/CSV/terminal
│   └── worker/       worker background, lock, autostart
├── migrations/sqlite/*.sql
├── tests/            server tiruan (API, SSH, IMAP), testenv, uji integrasi
├── docs/             PRD, keputusan, pemetaan API, uji lapangan
├── dist/             migrator.exe + SHA256SUMS
├── AGENT.md · CLAUDE.md · README.md
└── go.mod
```
