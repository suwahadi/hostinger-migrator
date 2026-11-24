# Daftar Uji Lapangan (Akun Hostinger Nyata)

Semua kode sudah lulus pengujian otomatis dengan server tiruan. Hal-hal di bawah **hanya bisa dibuktikan di akun Hostinger nyata** dan wajib dikerjakan di **akun dan website uji** (bukan produksi) sebelum memindahkan website klien. Tandai setiap baris setelah dicoba, dan catat hasil yang berbeda dari harapan.

Persiapan: satu akun uji **Single** dan satu akun uji **Premium/Business** (sumber dan tujuan), satu website WordPress uji, satu website statis uji, satu domain uji dengan 2 mailbox (salah satu berisi lampiran besar), satu domain expired (opsional).

## A. Fondasi dan SSH (Fase 1–2)

| # | Uji | Harapan | Hasil |
|---|---|---|---|
| A1 | `./migrator.exe version` di Git Bash Windows 10 dan 11 | Versi tampil, tidak ada error | |
| A2 | `account add` dengan token valid dan tidak valid | Valid: akun `uXXX` muncul. Tidak valid: exit 2 "token ditolak" | |
| A3 | Input token tersembunyi (langsung dan lewat `winpty`) | Token tidak terlihat | |
| A4 | Cari token di `data/` dan Credential Manager | Tidak ada di `data/`, ada di Credential Manager (`hostinger-migrator`) | |
| A5 | `ssh setup` di akun Single dan Premium/Business | `SSH_READY`; port sesuai hPanel | |
| A6 | Izin file `data/ssh/id_ed25519` (Properties → Security) | Hanya user Windows saat ini | |
| A7 | `audit` kedua akun | Tabel masuk akal; catat kode yang berbeda dari kenyataan hPanel | |

## B. Inventaris dan Rencana (Fase 3)

| # | Uji | Harapan | Hasil |
|---|---|---|---|
| B1 | `inventory --refresh` | Root path, tipe, ukuran, DB, mailbox sesuai hPanel | |
| B2 | Apakah Free Business Email muncul di inventaris (R9) | Mailbox tampil | |
| B3 | `plan --dry-run` ke akun tujuan berpaket kecil atau disk hampir penuh | Rencana diterima, tanpa pemeriksaan kuota website/disk/database | |
| B4 | Migrasi ke akun tujuan yang kapasitasnya benar-benar penuh | Setelah kapasitas ditambah: item `FAILED` lanjut dengan `retry --failed`; item tindakan manual (mis. `MANUAL_CREATE_SITE`) lanjut lewat `pending` lalu `confirm` | |
| B5 | `inventory --refresh` dengan `rate_per_menit: 80`: catat durasi total dan cari `batas rate API tercapai` di `data/logs/*.log` | Progres per website tampil; tidak ada atau jarang 429 | |
| B6 | `inventory --refresh --cepat` di akun yang sama | Lebih cepat, ukuran `?`, tipe sama dengan scan penuh | |
| B7 | Tutup jendela di tengah `inventory --refresh` (token dengan 2 akun atau lebih), buka lagi, menu 3 jawab `T` | Hanya akun yang belum selesai dipindai | |
| B8 | `inventory --akun uXXX` | Hanya akun itu yang dipindai | |
| B9 | Menu 4 dengan token berisi ratusan website: cari sebagian domain, pilih nomor, cari id akun lalu `A` | Hasil sesuai kata kunci, maksimal 30 baris, `Terpilih (...)` benar | |
| B10 | Layar akun tujuan, menu 3, dan menu 2 SSH dengan lebih dari 20 akun: cari sebagian id akun atau domain | Daftar penuh tidak tampil, hasil pencarian benar, akun tujuan tampil lebih cepat | |

## C. Backup, Provisioning, Restore, Verifikasi (Fase 4)

| # | Uji | Harapan | Hasil |
|---|---|---|---|
| C1 | `plan` website WordPress uji → worker berjalan | Sampai `VERIFIED` | |
| C2 | Apakah `POST /onboardings` membuat website dummy (API_CREATE_WEBSITE) | Website `*.hostingersite.com` muncul di hPanel tujuan | |
| C3 | Apakah `POST /databases` berhasil (API_DB_CREATE) | Database `u<tujuan>_...` muncul | |
| C4 | Apakah `MYSQL_PWD` diterima lewat SSH `setenv` atau fallback `export` (D-021) | Dump berhasil | |
| C5 | Metode verifikasi yang terpakai (`status --item`) (D-032) | Catat: header Host berhasil atau fallback | |
| C6 | Matikan paksa worker (Task Manager) di tengah backup, lalu `resume` | Lanjut tanpa website dummy ganda | |
| C7 | Putuskan internet di tengah upload, sambungkan lagi | Artefak diulang, lanjut | |
| C8 | Restart Windows dengan `autostart on` | Worker berjalan lagi setelah login | |
| C9 | Paket tanpa SSH → `MANUAL_BACKUP` | Backup hPanel diterima dan divalidasi | |
| C10 | Masa berlaku subdomain gratis (R6) | Dummy masih aktif saat cutover | |

## D. Cutover (Fase 5)

| # | Uji | Harapan | Hasil |
|---|---|---|---|
| D1 | `cutover` → ganti domain sumber ke sementara di hPanel → `confirm` | Lolos verifikasi | |
| D2 | Apakah parked domain di website dummy berfungsi (D-037, R3) | Domain asli membuka website tujuan, folder tetap | |
| D3 | Record DNS pihak ketiga (MX, TXT, CNAME) setelah cutover | Tidak berubah | |
| D4 | SSL untuk domain parked (D-039) | `SSL_ACTIVE`, atau catat bila harus manual | |
| D5 | `rollback <domain>` | DNS kembali, situs lama hidup setelah domain sumber dikembalikan | |
| D6 | Website expired | Berakhir `DUMMY_ONLY` | |

## E. Email (Fase 6)

| # | Uji | Harapan | Hasil |
|---|---|---|---|
| E1 | Ganti password mailbox via API (R9) | Berhasil, atau catat bila `MANUAL_EMAIL_PASSWORD` | |
| E2 | Login IMAP `imap.hostinger.com:993` dengan password baru | Berhasil | |
| E3 | Backup 2 mailbox (lampiran besar, banyak folder) | Jumlah pesan cocok | |
| E4 | Hapus layanan email sumber, aktifkan di tujuan | Layanan pindah | |
| E5 | Restore + verifikasi | Jumlah pesan per folder cocok, flag/tanggal benar | |
| E6 | Alias/forwarder/auto-reply di tujuan | Ada; forwarder mungkin perlu konfirmasi ulang | |

## F. Cleanup, Laporan, Skala (Fase 7–8)

| # | Uji | Harapan | Hasil |
|---|---|---|---|
| F1 | `cleanup` sebelum DNS propagasi | Tertahan dengan alasan | |
| F2 | `cleanup` setelah semua gerbang lolos | Website + DB sumber terhapus, domain tetap | |
| F3 | `report` | HTML/JSON/CSV lengkap, kesimpulan jujur, daftar akun ber-SSH key | |
| F4 | Batch 10–20 website non-kritis | Semua selesai atau tertahan dengan alasan jelas | |
| F5 | Cabut token di tengah batch, buat baru, `retry --failed --termasuk-blocked` | Lanjut | |

Kirimkan hasil (setelah token/password disensor) beserta `data/logs/*.log` bila ada yang tidak sesuai harapan.
