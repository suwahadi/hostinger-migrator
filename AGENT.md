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

## 4. Struktur Proyek Target

```
hostinger-migrator/
├── cmd/migrator/main.go
├── internal/
│   ├── cli/ config/ store/ secrets/ hostinger/ sshx/
│   ├── capability/ inventory/ detector/ planner/
│   ├── backup/ restore/ verify/ cutover/ email/
│   ├── cleanup/ pipeline/ worker/ report/
├── internal/hostinger/contracts/*.yaml
├── migrations/sqlite/*.sql
├── configs/default.yaml
├── docs/{PRD.md,decisions.md,api-shared-hosting.md}
├── tests/{fixtures,integration}
├── AGENT.md
├── CLAUDE.md
├── README.md
└── go.mod
```
