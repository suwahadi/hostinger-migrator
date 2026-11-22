# Pemetaan API Hostinger untuk Shared Hosting (Fase 0)

| Info | Nilai |
|---|---|
| Tanggal riset | 22 November 2026 |
| Spesifikasi utama | `https://developers.hostinger.com/openapi/openapi.json` versi **3.4.2** (sha256 `308dd885…f6227`) |
| Spesifikasi Mail API | `https://api.mail.hostinger.com/openapi/openapi.json` versi **1.3.0** (sha256 `2ca5002f…51bd5`) |
| Dokumentasi pendukung | `https://docs.hostinger.com/llms.txt`, halaman `emails/*`, `websites/ftp-ssh`, `websites/backups` |
| Kontrak terverifikasi | `internal/hostinger/contracts/{hosting,dns,domains,mail,mail-api}.yaml` — 65 endpoint, semuanya dicocokkan otomatis dengan `operationId` + path + method di spesifikasi resmi |

## 1. Arti Status

| Status Fase 0 | Arti | Status awal di Capability Audit |
|---|---|---|
| **Tersedia** | Endpoint ada di namespace Shared Hosting (`/api/hosting`, `/api/dns`, `/api/domains`, `/api/mail`) atau di Mail API | `UNKNOWN` sampai lolos uji nyata, lalu `SUPPORTED` |
| **Tidak tersedia** | Tidak ada endpoint resmi untuk Shared Hosting | `MANUAL` (ada checkpoint manual) atau `UNSUPPORTED` |
| **Belum jelas** | Endpoint ada, tetapi perilakunya untuk kebutuhan migrator belum terbukti | `UNKNOWN` |

Ingat: "tersedia di spesifikasi" **belum** berarti `SUPPORTED`. Status `SUPPORTED` hanya diberikan setelah uji nyata di akun uji (AC-05).

## 2. Tabel Operasi (PRD bagian 8.3)

| Kode | Operasi | Status Fase 0 | Endpoint resmi | Catatan / fallback |
|---|---|---|---|---|
| — | Validasi token, discovery akun `uXXX` | Tersedia | `GET /api/hosting/v1/orders`, `GET /api/hosting/v1/websites` (field `username`) | Akun hosting = nilai unik `username` dari daftar website per order. Order tanpa website belum punya username → tampil sebagai "akun belum disiapkan". |
| `API_LIST_WEBSITES` | List website | Tersedia | `GET /api/hosting/v1/websites` | Memberi `domain`, `vhost_type` (main/addon/parked/subdomain), `root_directory`, `is_enabled`, `website_type`, `order_id`. |
| `API_CREATE_WEBSITE` | Buat website | Tersedia | `POST /api/hosting/v1/orders/{order_id}/onboardings` + polling `GET /api/hosting/v1/onboardings?domain=` | `POST /api/hosting/v1/websites` sudah **deprecated** dan tidak dipakai. Polling tiap 10–15 detik sampai `completed`. Riwayat setup hanya 24 jam → idempotensi juga dicek lewat `GET /websites?domain=`. |
| `API_FREE_SUBDOMAIN` | Subdomain gratis | Tersedia | `POST /api/hosting/v1/domains/free-subdomains` | Atau kirim `domain: null` saat onboarding (otomatis). Subdomain hasil generate berlaku 24 jam untuk onboarding. Masa berlaku jangka panjang masih diuji di Fase 4 (R6). |
| `API_CHANGE_DOMAIN` | Ganti domain website | **Tidak tersedia** | — (hanya ada `agency-hosting_changeWebsiteDomainV1`) | Checkpoint `MANUAL_SOURCE_RENAME` di sumber. Untuk tujuan ada kandidat lain, lihat bagian 4. |
| `API_DB_CREATE` | Buat database | Tersedia | `POST /api/hosting/v1/accounts/{username}/databases` | Wajib `name`, `user`, `password`, `website_domain`. Prefix `u<akun>_` ditambahkan otomatis. List DB memberi `host`, `port`, `disk_usage_mb`, `max_size_mb`. |
| `API_DNS_READ` | Baca zone | Tersedia | `GET /api/dns/v1/zones/{domain}` | Hanya untuk domain yang zone-nya ada di akun token tersebut. |
| `API_DNS_WRITE` | Tulis zone | Tersedia | `PUT /api/dns/v1/zones/{domain}` (+ `POST …/validate` untuk dry-run) | `overwrite=true` mengganti RR dengan name+type yang sama. Migrator hanya mengirim RR yang diubah agar record pihak ketiga tetap ada (AC-11). |
| `API_DNS_SNAPSHOT` | Snapshot DNS | Tersedia (sebagian) | `GET /api/dns/v1/snapshots/{domain}`, `GET …/{id}`, `POST …/{id}/restore` | **Tidak ada endpoint untuk membuat snapshot.** Snapshot dibuat otomatis oleh Hostinger saat zone berubah. Migrator menyimpan JSON zone lokal sebelum perubahan sebagai sumber rollback utama, dan snapshot API sebagai cadangan. |
| `API_SSL` | Pasang SSL | Tersedia | `POST …/websites/{domain}/ssl/setup` + `GET …/ssl/status` | 422 untuk subdomain gratis (SSL dikelola platform). Status: `not_installed`, `installing`, `waiting_for_retry`, `active`, `expired`, `failed`. |
| — | Samakan versi PHP | Tersedia | `GET …/php/details`, `PATCH …/php/version`, `…/php/extensions`, `…/php/options` | Ekstensi dan opsi PHP juga bisa disamakan. |
| — | Cron job | Tersedia | `GET/POST/DELETE /api/hosting/v1/accounts/{username}/cron-jobs` | Cron bisa dipindah lewat API, tidak perlu manual. |
| — | Subdomain & parked domain | Tersedia | `…/subdomains`, `…/parked-domains` (list, create, delete) | |
| — | Status domain (expired, dll.) | Tersedia | `GET /api/domains/v1/portfolio`, `GET …/portfolio/{domain}` | Hanya untuk domain yang terdaftar di akun token tersebut. |
| — | Hapus website (cleanup) | Tersedia | `DELETE /api/hosting/v1/websites/{domain}` | Asinkron. Menghapus **file + database + konfigurasi** sekaligus. Hanya main/addon. Wajib lewat gerbang konfirmasi. |
| — | Hapus database (cleanup) | Tersedia | `DELETE /api/hosting/v1/accounts/{username}/databases/{name}` | |
| `SSH` | Tambah SSH key login | **Tidak tersedia** | — | `hosting_generateGitSSHKeyV1` hanya untuk deploy Git, bukan login. Key ditempel manual di hPanel (sesuai PRD 8.2). |
| `SSH` | Info host/port/IP SSH | **Tidak tersedia** | — | Tidak ada field SSH atau IP di resource Shared Hosting. Operator menyalin dari hPanel → SSH Access. Port **tidak** di-hardcode. |
| `SSH_TOOLS` | `tar`, `mysqldump`, dll. | (lewat SSH) | — | Diuji dengan `command -v` saat audit. |
| `QUOTA` | Kuota website/disk/inode/DB | **Tidak tersedia** | — (metrik hanya ada di agency-hosting) | Order hanya memberi nama paket (mis. `hostinger_business`). Disk dan inode via SSH (`df`, `quota`, `du`). Kuota website → `UNKNOWN` + konfirmasi operator. `max_size_mb` per DB tersedia dari list DB. |
| — | Download/export backup website | **Tidak tersedia** | — | Sesuai PRD: backup via SSH. Paket tanpa SSH → `MANUAL_BACKUP`. |
| — | IP server tujuan (untuk verifikasi `Host:` header) | **Tidak tersedia** | — | Diambil dari resolusi DNS subdomain dummy, atau dari SSH (`hostname -I` / IP publik). |

## 3. Email

Hostinger punya **dua API email yang terpisah**:

| API | Base URL | Token | Cakupan |
|---|---|---|---|
| Hostinger API — Mail | `developers.hostinger.com/api/mail/v1` | Token hPanel (sama dengan token hosting) | Order email, mailbox, alias, forwarder, catch-all, auto-reply, password |
| Mail API | `api.mail.hostinger.com/api/v1` | Token khusus per order email, dibuat di **Agentic Mail → API access** (atau `POST /api/mail/v1/orders/{orderId}/api-tokens`) | Isi mailbox: folder, pesan, sumber `.eml`, kuota |

| Kode | Operasi | Status Fase 0 | Endpoint | Catatan |
|---|---|---|---|---|
| `API_EMAIL_INVENTORY` | Mailbox, alias, forwarder, catch-all, kuota | Tersedia | `GET /api/mail/v1/orders`, `…/{orderId}/mailboxes`, `…/aliases`, `…/forwarders`, `…/catchalls`, `…/autoreplies` | |
| — | Jumlah pesan per folder | Tersedia (Mail API) | `GET /api/v1/mailboxes/{id}/folders`, `…/messages` | Butuh token Mail API. |
| `API_EMAIL_BACKUP` | Backup pesan | Tersedia (Mail API) | `GET …/folders/{folder}/messages/{uid}/source` | Mengunduh RFC822 mentah (`.eml`) per pesan, termasuk lampiran. Tidak mengubah flag *Seen*. |
| `API_EMAIL_DELETE` | Hapus mailbox | Tersedia | `DELETE /api/mail/v1/mailboxes/{mailboxId}` | *Soft delete*, masih bisa dipulihkan dalam periode terbatas. |
| — | Hapus **layanan email domain** di sumber | **Tidak tersedia** | — | Checkpoint manual. |
| — | Aktifkan layanan email domain di akun tujuan | **Tidak tersedia** | — | Checkpoint `MANUAL_EMAIL_SETUP`. |
| `API_EMAIL_CREATE` | Buat mailbox | Tersedia | `POST /api/mail/v1/orders/{orderId}/mailboxes` | **Password wajib diisi teks biasa.** Tidak ada cara memindahkan hash password lama. |
| `API_EMAIL_RESTORE` | Restore pesan | **Tidak tersedia** | — | Mail API **tidak punya** endpoint upload/append pesan. Alternatif: IMAP APPEND, atau fitur **Email Import** di hPanel (IMAP, butuh mailbox sumber masih hidup). |
| — | Buat ulang alias/forwarder/catch-all/auto-reply | Tersedia | `POST /api/mail/v1/mailboxes/{id}/{aliases,forwarders,catchalls,autoreplies}` | Forwarder perlu konfirmasi ulang dari alamat tujuan. |
| — | Nilai DNS email tujuan (MX/SPF/DKIM) | **Tidak tersedia** | — | Diambil dari hPanel atau dokumentasi `emails/domain-settings`. |

### Dampak ke PRD 8.12

1. **"Restore via API" tidak bisa dilakukan** dengan API resmi saat ini. Jalur yang realistis: backup `.eml` via Mail API → buat mailbox di tujuan via API (password diketahui tool) → restore memakai **IMAP APPEND** ke mailbox tujuan.
2. **"Password lama tetap berlaku"** hanya mungkin bila operator memberikan password lama tiap mailbox. Tool lalu membuat mailbox tujuan dengan password itu. Tanpa password lama, tool harus membuat password baru dan operator membagikannya ke pengguna.
3. **Mail API butuh token per order email.** Agentic Mail disebut sebagai fitur paket berbayar. Belum jelas apakah *Free Business Email* bawaan hosting bisa memakai Mail API. Kalau tidak bisa, backup memakai IMAP FETCH dan butuh password mailbox.

## 4. Cutover Domain

API Shared Hosting **tidak punya** endpoint ganti domain website. Kandidat jalur:

| Langkah | Jalur API | Fallback |
|---|---|---|
| Lepas domain di sumber | Tidak ada | `MANUAL_SOURCE_RENAME`: operator mengganti domain website sumber ke subdomain sementara di hPanel |
| Pasang domain di tujuan | Kandidat: **parked domain** (`POST …/parked-domains`) pada website dummy, atau **onboarding baru** dengan domain asli lalu file dipindah dari folder dummy via SSH | `MANUAL_DEST_DOMAIN`: operator mengganti domain website dummy di hPanel |
| Verifikasi kepemilikan | `POST /api/hosting/v1/domains/verify-ownership` (memberi TXT verifikasi) | — |

Kedua kandidat API **wajib dibuktikan di Fase 4** pada akun uji (R3). Hal yang perlu dibuktikan: apakah domain bisa dipasang di akun tujuan selagi masih ada di akun sumber, dan apakah folder ikut berpindah.

## 5. Hal yang Wajib Dibuktikan di Akun Uji

| # | Hal | Fase |
|---|---|---|
| U1 | Token valid: `GET /orders` 200, token salah 401 | 1 |
| U2 | `username` dan `root_directory` muncul untuk paket Single/Premium/Business | 1–3 |
| U3 | Onboarding `domain: null` menghasilkan subdomain dummy + folder `/home/<u>/domains/<dummy>/` | 4 |
| U4 | Buat DB via API untuk website dummy | 4 |
| U5 | Parked domain atau onboarding domain asli di tujuan saat domain masih ada di sumber | 4 |
| U6 | Snapshot DNS otomatis muncul setelah `PUT /zones` | 5 |
| U7 | SSL `setup` setelah DNS mengarah ke tujuan | 5 |
| U8 | Token Mail API bisa dibuat untuk paket email yang dipakai operator | 6 |
| U9 | IMAP APPEND ke mailbox tujuan menjaga folder, flag, dan tanggal | 6 |

## 6. Pertanyaan untuk Operator

Sudah dijawab lewat wizard pada 22 November 2026. Ringkasan jawaban ada di bagian 7, detailnya di `docs/decisions.md` (D-008 sampai D-011).

1. **Restore email:** setuju memakai jalur *Mail API (backup .eml) → buat mailbox via API → IMAP APPEND*? Atau jalur yang pernah Anda pakai berbeda (sebutkan langkahnya)?
2. **Password mailbox:** bisakah Anda menyediakan password lama tiap mailbox, atau boleh dibuatkan password baru?
3. **Paket email:** domain-domain klien memakai *Free Business Email* bawaan hosting atau paket Hostinger Email berbayar?
4. **Cutover:** setuju langkah lepas domain di sumber dijadikan checkpoint manual (`MANUAL_SOURCE_RENAME`), sementara pasang domain di tujuan dicoba via API dulu di Fase 4?
5. **Kuota website tujuan:** karena tidak ada di API, boleh tool memakai tabel kuota paket standar Hostinger (Single/Premium/Business) dan meminta konfirmasi operator?

## 7. Jawaban Operator dan Alur Email Final

| Pertanyaan | Jawaban |
|---|---|
| Restore email | API + IMAP APPEND |
| Password mailbox | Dibuatkan password baru oleh tool |
| Paket email klien | Free Business Email |
| Cutover dan kuota | Setuju semua |

Alur email v1 (menggantikan PRD 8.12 langkah 2, 6, 7, 10):

1. Inventaris mailbox, alias, forwarder, catch-all, auto-reply via `/api/mail/v1` (token hPanel).
2. Konfirmasi operator (sekali per batch): ganti password sumber + hapus email lama.
3. Ganti password mailbox sumber ke password baru buatan tool (`PATCH …/mailboxes/{id}/password`).
4. Backup `IMAP FETCH` semua folder → `.eml` + `manifest.json` (jumlah pesan per folder, ukuran, sha256).
5. Validasi jumlah pesan backup = jumlah di server.
6. Hapus mailbox sumber (soft delete) dan layanan email domain (checkpoint manual).
7. Aktifkan email domain di akun tujuan (`MANUAL_EMAIL_SETUP`), lalu buat mailbox via API dengan password yang sama.
8. `IMAP APPEND` per folder, menjaga flag dan tanggal internal.
9. Buat ulang alias, forwarder, catch-all, dan auto-reply via API.
10. DNS email (MX, SPF, DKIM, DMARC) via token sumber.
11. Verifikasi jumlah pesan per folder dan login IMAP berhasil.
12. Operator melihat password baru lewat perintah khusus (ditampilkan sekali, tidak masuk laporan).

Risiko baru: **R9** — ketersediaan `/api/mail/v1` untuk Free Business Email belum terbukti. Bila tidak tersedia, langkah 3, 6, 7, dan 9 menjadi checkpoint manual dan operator perlu memberikan password mailbox sumber.

## 8. Pembaruan Fase 3

- Contoh skema `Mail.V1.Orders.OrderPlanResource` di openapi.json 3.4.2 memakai plan `hostinger_free` ("Free Email"), sehingga Free Business Email kemungkinan besar muncul di `GET /api/mail/v1/orders`. Risiko R9 turun, tetap dibuktikan di akun uji (D-030).
- Endpoint baca tambahan yang dipakai inventaris (semuanya sudah ada di kontrak): `hosting.subdomains.list`, `hosting.parked_domains.list`, `hosting.php.details`, `hosting.cron_jobs.list`, `hosting.files.list`, `domains.portfolio.list`, `dns.zones.get`, `mail.orders.list`, `mail.mailboxes.list`.
