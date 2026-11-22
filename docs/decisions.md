# Catatan Keputusan

Setiap keputusan teknis yang menyimpang dari PRD, menambah dependency, atau memakai endpoint di luar kontrak dicatat di sini.

| # | Tanggal | Keputusan | Alasan |
|---|---|---|---|
| D-001 | 2026-11-22 | Path modul Go: `hostinger-migrator`. | Keputusan operator (K1). |
| D-002 | 2026-11-22 | Tidak ada komentar di kode dalam bentuk apa pun. Menggantikan PRD bagian 19 poin 1. Penjelasan desain ditulis di `docs/`. | Instruksi operator. |
| D-003 | 2026-11-22 | Push selalu ke `main`, tanpa branch baru, tanpa anotasi AI, identitas `suwahadi <suwahadi@gmail.com>`. | Instruksi operator. |
| D-004 | 2026-11-22 | Website dibuat lewat `POST /api/hosting/v1/orders/{order_id}/onboardings`, bukan `POST /api/hosting/v1/websites`. | Endpoint lama berstatus deprecated di spesifikasi 3.4.2. |
| D-005 | 2026-11-22 | Endpoint `agency-hosting` tidak dipakai sama sekali. | PRD bagian 4 poin 5 dan aturan agent poin 2. Semua operasi yang dibutuhkan punya padanan di namespace `hosting`, kecuali ganti domain (jadi checkpoint manual). |
| D-006 | 2026-11-22 | Rollback DNS memakai JSON zone yang disimpan lokal sebelum perubahan. Snapshot API dipakai sebagai cadangan. | Tidak ada endpoint untuk membuat snapshot DNS sesuai permintaan. |
| D-007 | 2026-11-22 | Uji API memakai token akun uji dari environment variable `HOSTINGER_TEST_TOKEN_SUMBER`, `HOSTINGER_TEST_TOKEN_TUJUAN`, dan `HOSTINGER_TEST_MAIL_TOKEN`. Token tidak pernah ditempel di chat atau ditulis ke repo. | Keputusan operator (K2) dan keamanan rahasia. |
| D-008 | 2026-11-22 | Restore email: backup pesan → buat mailbox tujuan via `POST /api/mail/v1/orders/{orderId}/mailboxes` → restore pesan via **IMAP APPEND**. | Jawaban operator. Mail API tidak punya endpoint upload pesan. |
| D-009 | 2026-11-22 | Password mailbox tujuan dibuat **baru** oleh tool (kuat, sesuai aturan Hostinger), disimpan di Credential Manager, ditampilkan sekali ke operator lewat perintah khusus, dan **tidak pernah** masuk laporan atau log. Ini menggantikan syarat "password lama tetap berlaku" di PRD 8.12 dan AC-12. | Jawaban operator. Hash password lama tidak bisa dipindahkan lewat API. |
| D-010 | 2026-11-22 | Email klien umumnya Free Business Email, sehingga Mail API (Agentic Mail) dianggap **tidak tersedia**. Jalur utama backup: tool mengganti password mailbox **sumber** via `PATCH /api/mail/v1/mailboxes/{id}/password` ke password baru (D-009) → `IMAP FETCH` ke `.eml` → hapus mailbox sumber → buat mailbox tujuan dengan password yang sama → `IMAP APPEND`. Mail API dipakai hanya bila tersedia untuk order tersebut. Penggantian password sumber adalah operasi destruktif: lewat gerbang konfirmasi hapus email (sekali per batch) dan tercatat di `audit_log`. | Jawaban operator. Jalur ini tidak butuh password lama dari operator. Ketersediaan `/api/mail/v1` untuk Free Business Email wajib dibuktikan di akun uji (U8). |
| D-011 | 2026-11-22 | Cutover: lepas domain di sumber = checkpoint `MANUAL_SOURCE_RENAME`. Pasang domain di tujuan dicoba via API (parked domain atau onboarding domain asli) di Fase 4, fallback `MANUAL_DEST_DOMAIN`. Kuota website tujuan = tabel kuota paket standar Hostinger + konfirmasi operator. | Jawaban operator. Tidak ada endpoint ganti domain dan kuota untuk Shared Hosting. |
