# Desain: laporan.json Valid dan Opsi Lewati Database Tak Terpakai

| Info | Nilai |
|---|---|
| Tanggal | 25 November 2026 |
| Ukuran | Perubahan kecil |
| Status | Disetujui operator (25 November 2026) |

## 1. Masalah

1. `laporan.json` ditulis lalu disaring sebagai teks JSON mentah (`report.Builder.Write`). Pola penyaring berbasis nama kunci mengubah `"token": "sumber"` menjadi `"token": [DISEMBUNYIKAN]`, sehingga laporan hampir selalu JSON tidak valid. Ini masalah yang sama dengan JSON inventaris sebelum rc9.
2. Database yang terhubung ke website lewat API (sumber `api`) tetapi tidak dirujuk file konfigurasi mana pun (mis. sisa instalasi ulang WordPress) tetap tidak diketahui sumbernya walau akun sudah dipindai ulang dengan SSH. Item terus kembali ke `MANUAL_DB_SOURCE` dan tidak bisa dimigrasi, karena tidak ada cara melewati database itu.

## 2. Sasaran

### 2.1 laporan.json

Penyaringan per nilai string yang dibuat untuk inventaris (rc9) dipindah ke `logx` sebagai pembantu bersama dan dipakai oleh inventaris serta `laporan.json`. HTML dan CSV tetap seperti sekarang.

### 2.2 Lewati database

1. `migrator confirm MANUAL_DB_SOURCE <domain> --nilai lewati` melewati semua database website itu yang sumber password-nya tidak diketahui. Nilai lain selain kosong atau `lewati` (tanpa membedakan huruf besar/kecil) ditolak.
2. Database yang dilewati tidak ikut backup, restore, verifikasi, dan penghapusan database oleh tool saat cleanup.
3. Nama database yang dilewati dicatat di `audit_log` saat konfirmasi.
4. Instruksi `MANUAL_DB_SOURCE` membedakan dua kasus:
   - inventaris akun dibuat tanpa SSH: penyebab paling umum, saran pindai ulang (seperti rc9);
   - inventaris sudah memakai SSH: "tidak ada file konfigurasi website yang merujuk database ini; kemungkinan database tidak dipakai (mis. sisa instalasi lama)".
   Keduanya diberi langkah ketiga: `migrator confirm MANUAL_DB_SOURCE <domain> --nilai lewati`, dengan penjelasan bahwa database yang dilewati tidak dipindah dan kemungkinan ikut terhapus saat cleanup website sumber.
5. Menu 6 (tindakan manual) meminta isian untuk `MANUAL_DB_SOURCE`: "Ketik lewati bila database tidak dipakai website, atau ENTER setelah pindai ulang".
6. Peringatan `KREDENSIAL_DB` di rencana ditambah kalimat: database yang tidak dipakai website bisa dilewati saat tindakan manual `MANUAL_DB_SOURCE` muncul.
7. **Cleanup (pilihan operator):** daftar "Lolos gerbang" menampilkan baris merah per website yang punya database dilewati: `database dilewati: <db> (kemungkinan ikut terhapus bersama website sumber)`. Satu konfirmasi `HAPUS` per batch tetap berlaku.
8. Laporan mencatat database yang dilewati di catatan website.

## 3. Non-sasaran

- Memilih database tertentu untuk dilewati (yang dilewati selalu semua database bersumber tidak diketahui pada website itu).
- Mencegah Hostinger menghapus database yang terhubung saat website sumber dihapus. Perilaku persisnya belum teruji di akun nyata.

## 4. Pendekatan

| Bagian | Pilihan | Alasan |
|---|---|---|
| laporan.json | Pembantu bersama di `logx` | Satu cara yang sudah terbukti di inventaris |
| Lewati database | Nilai `lewati` pada `confirm` tindakan manual yang sudah ada, disimpan di tugas manual | Tanpa perubahan skema; daftar database dibaca ulang tiap siklus |
| Cleanup | Ditampilkan jelas di konfirmasi `HAPUS` (pilihan operator) | Alternatif "tahan cleanup website itu" ditolak operator |

## 5. Test

- `logx`: pembantu JSON menjaga struktur valid dan menyaring rahasia di string; kunci `token` tidak merusak JSON.
- `report`: `laporan.json` valid untuk laporan yang memuat baris SSH key (kunci `token`).
- `pipeline`: `confirm MANUAL_DB_SOURCE` menolak nilai selain kosong/`lewati`; dengan `lewati`, daftar database item tidak memuat database bersumber tidak diketahui dan audit mencatat namanya; instruksi menyebut kasus "sudah memakai SSH" saat inventaris memakai SSH.
- `cli`: daftar cleanup menampilkan baris database dilewati.

## 6. Rilis

`1.0.0-rc10`, D-058, README (baris `MANUAL_DB_SOURCE` menyebut opsi `lewati`).
