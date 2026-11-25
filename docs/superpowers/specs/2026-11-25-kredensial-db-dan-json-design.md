# Desain: Sumber Password Database Tidak Diketahui dan JSON Inventaris Rusak

| Info | Nilai |
|---|---|
| Tanggal | 25 November 2026 |
| Ukuran | Perubahan kecil |
| Status | Disetujui operator (25 November 2026) |

## 1. Masalah yang Terbukti

Batch `MIG-20261125-001`, website `inspired-darkgray-hyena-70.hostingersite.com` (akun `u874616924`):

1. Inventaris akun selesai pukul 09:46 **tanpa SSH**; SSH akun baru `SSH_READY` pukul 09:51. Tanpa SSH, file konfigurasi website tidak dibaca, sehingga database `u874616924_Lle8F` hanya tercatat dari API (sumber `api`) dan password-nya tidak bisa dibaca saat backup.
2. Rencana tidak memberi peringatan apa pun tentang hal ini.
3. `backup.ReadCredentials` mengembalikan error biasa; runner menganggapnya gangguan sementara dan mengulang sampai 8 kali lalu `FAILED`, tanpa memberi tahu operator langkah perbaikannya.
4. Terpisah: ke-78 file `data/inventory/*.json` tidak valid. `detector.ExtractSecrets` mendaftarkan nilai sepele seperti `false`/`true`/`null` dari baris `*_KEY=`, `*TOKEN=`, `*PASSWORD=` di `.env`, lalu `writeJSON` menyaring teks JSON mentah sehingga `"zone_dns_di_token": false` menjadi `"zone_dns_di_token": [DISEMBUNYIKAN]`. Log tidak terkena karena hanya nilai teks yang disaring.

## 2. Sasaran

1. **Worker:** error "sumber kredensial tidak diketahui" tidak lagi diulang. Item langsung menunggu tindakan manual `MANUAL_DB_SOURCE` dengan instruksi: pindai ulang akun lalu `confirm`. Setelah `confirm`, backup dicoba lagi dengan data inventaris terbaru; bila masih tidak diketahui, tindakan manual dibuka ulang.
2. **Rencana:** pemeriksaan baru `KREDENSIAL_DB` (PERINGATAN, bukan penolakan) bila website terpilih memiliki database yang sumber password-nya belum diketahui dan SSH sumber siap. Pesan menyebut domain + database dan saran `migrator inventory --akun <akun> --refresh`.
3. **JSON inventaris:**
   - Penyaring rahasia mengabaikan nilai sepele: `true`, `false`, `null`, `none` (tanpa membedakan huruf besar/kecil) dan rujukan `${...}`.
   - JSON inventaris hanya menyaring nilai teks (string), tidak lagi teks JSON mentah, sehingga rahasia apa pun (termasuk password berupa angka) tidak bisa merusak struktur JSON.

## 3. Non-sasaran

- Membaca password database dari file konfigurasi yang polanya tidak dikenali detektor.
- Memperbaiki file JSON lama yang sudah rusak: file itu dibuat ulang saat akun dipindai ulang.
- Backup database manual sebagian (hanya database) lewat hPanel.

## 4. Teks Instruksi `MANUAL_DB_SOURCE`

```
Password database <db> untuk <domain> tidak bisa dibaca karena file konfigurasinya belum diketahui.
Penyebab paling umum: inventaris akun <akun> dibuat sebelum SSH siap, sehingga file konfigurasi website tidak dibaca.
1. Pindai ulang akun: migrator inventory --akun <akun> --refresh (atau menu 3, pindai ulang, pilih akun ini)
2. Lanjutkan: migrator confirm MANUAL_DB_SOURCE <domain>
Bila tindakan ini muncul lagi setelah pindai ulang, website memakai file konfigurasi yang tidak dikenali tool. Catat nama file konfigurasinya dan laporkan ke pengembang.
```

## 5. Pendekatan

| Bagian | Pilihan | Alasan |
|---|---|---|
| Worker | Error bertanda `backup.ErrUnknownDBSource`, diubah menjadi `ManualError` di `RunBackup` | Pola `ManualError` + `confirm` generik sudah dipakai checkpoint lain |
| Rencana | Peringatan, bukan penolakan | Operator tetap bisa melanjutkan, worker akan berhenti di tindakan manual |
| JSON | Akar masalah diperbaiki di `Redactor.Add`, ditambah penyaringan per nilai teks di `writeJSON` | Nilai sepele tidak pernah rahasia; penyaringan per nilai mencegah kerusakan dari rahasia apa pun |

## 6. Test

- `backup`: `ReadCredentials` dengan sumber `api`/kosong mengembalikan error yang `errors.Is(err, ErrUnknownDBSource)`.
- `pipeline`: fungsi pemetaan error backup menghasilkan `ManualError{Code: "MANUAL_DB_SOURCE"}` berisi akun, domain, database (tanpa koneksi SSH).
- `planner`: `KREDENSIAL_DB` muncul sebagai PERINGATAN untuk database bersumber `api`; tidak muncul bila semua sumber diketahui atau SSH sumber tidak siap.
- `logx`: `Add("false")`, `Add("NULL")`, `Add("${PUSHER_KEY}")` tidak menyaring apa pun; rahasia biasa tetap tersaring.
- `inventory`: JSON yang ditulis valid walau ada rahasia angka yang muncul di dalam angka ukuran, dan rahasia di perintah cron tetap tersaring.

## 7. Rilis

`1.0.0-rc9`, D-057 di `docs/decisions.md`, README bagian tindakan manual (`MANUAL_DB_SOURCE`).
