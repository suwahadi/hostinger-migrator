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
