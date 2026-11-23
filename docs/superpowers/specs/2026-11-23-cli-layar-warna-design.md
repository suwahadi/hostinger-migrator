# Desain: Layar Bersih dan Warna di Wizard CLI

| Info | Nilai |
|---|---|
| Tanggal | 23 November 2026 |
| Ukuran | Perubahan kecil |
| Status | Disetujui operator (23 November 2026) |

## 1. Tujuan dan Alasan

Operator menjalankan wizard (`migrator.exe` diklik dua kali atau dari Git Bash). Saat ini setiap menu dan hasil tindakan menumpuk di bawah tumpukan lama, sehingga hasil terbaru sulit dibaca. Warna membantu membedakan judul, hasil sukses, operasi berbahaya, dan hal yang perlu diperhatikan.

## 2. Sasaran

1. Setiap kali menu tampil dan setiap kali pilihan dibuat, layar dibersihkan (termasuk riwayat gulir). Hasil tindakan tampil sendiri, lalu `Tekan ENTER untuk kembali ke menu.`, lalu layar bersih dan menu tampil lagi.
2. Di dalam satu tindakan, layar tidak dibersihkan. Daftar yang harus dibaca dan prompt `Ketik YAKIN/HAPUS/...` tetap berada di satu layar.
3. Warna sesuai permintaan operator:
   - judul/topik: biru tebal
   - setuju, konfirmasi, sukses: hijau
   - hapus, error: merah
   - peringatan, perlu perhatian khusus: kuning
4. `account list` menampilkan satu akun hosting per baris, bukan satu baris panjang per alias.

## 3. Non-sasaran

- Tidak ada opsi tema atau pengaturan warna di `config.yaml`.
- Perintah langsung (`migrator account list`, `migrator status`, dll.) ikut berwarna bila dijalankan di terminal, tetapi tidak membersihkan layar dan tidak menunggu ENTER.
- Teks pesan yang sudah ada tidak diubah, kecuali tata letak `account list`.
- Laporan HTML/JSON/CSV dan file log tidak berubah.

## 4. Anti-sasaran (gagal walau secara teknis jalan)

- Kode warna bocor ke file log, laporan, output yang dialihkan ke file/pipe, atau test.
- Kolom tabel bergeser karena kode warna.
- Jeda ENTER memakan baris input ketika wizard diberi input dari pipe atau script.

## 5. Batasan

- Harus jalan di: konsol Windows 10/11 (klik dua kali), Windows Terminal, dan Git Bash mintty tanpa `winpty`.
- Dependency: `github.com/mattn/go-isatty` dipakai langsung untuk mengenali mintty. Modul ini sudah ada di `go.sum` sebagai dependency tidak langsung, jadi tidak ada unduhan baru. Dicatat sebagai D-052.
- Tanpa komentar di kode (aturan operator 6).
- Teks baru tanpa em dash.

## 6. Pendekatan

| Pendekatan | Ringkasan | Kelebihan | Kekurangan |
|---|---|---|---|
| **A. Warna eksplisit per pesan (dipilih operator)** | Satu file kecil berisi fungsi judul/sukses/peringatan/bahaya, dipasang di tiap titik cetak yang relevan | Warna selalu tepat, status di tabel bisa diwarnai tanpa merusak kolom | Diff menyentuh sekitar 10 file |
| B. Pewarna otomatis per baris | Pembungkus output yang mewarnai baris berdasarkan pola teks | Diff paling kecil | Salah warna (mis. "akan dihapus dari komputer ini saja" jadi merah), status di dalam tabel tidak bisa diwarnai |
| C. Struktur saja | Judul, ERROR, PERINGATAN, prompt konfirmasi | Kecil dan tepat | Tidak ada hijau untuk sukses |

Operator memilih A pada 23 November 2026.

## 7. Desain

### 7.1 Deteksi terminal

- Warna aktif untuk sebuah output bila output itu `*os.File` dan merupakan terminal (`term.IsTerminal` atau `isatty.IsCygwinTerminal` untuk mintty). Stdout dan stderr dinilai terpisah.
- Pembersihan layar dan jeda ENTER hanya aktif di wizard bila stdin **dan** stdout terminal.
- Di Windows, `cmd/migrator/console_windows.go` mengaktifkan `ENABLE_VIRTUAL_TERMINAL_PROCESSING` pada stdout dan stderr. Bila gagal (konsol lama), warna dan pembersihan layar dimatikan.
- Test memakai buffer, bukan terminal, sehingga perilakunya tetap seperti sekarang.

### 7.2 Kode warna

Varian terang dipakai karena varian standar gagal kontras di latar hitam bawaan:

| Peran | Kode ANSI | Kontras di konsol Windows (#0C0C0C) | Kontras di Git Bash (#000000) |
|---|---|---|---|
| Judul | `1;94` biru terang tebal | 4,95 (biru standar: 2,38, gagal) | 4,61 |
| Sukses | `92` hijau terang | 8,49 | lulus |
| Bahaya | `91` merah terang | 5,09 (merah standar: 3,23, gagal) | 6,06 |
| Peringatan | `93` kuning terang | 16,91 | lulus |

Pembersih layar: `ESC[H ESC[2J ESC[3J` (kursor ke awal, hapus layar, hapus riwayat gulir).

### 7.3 Alur layar wizard

```
[bersih] banner + MENU UTAMA → Pilih: 3
[bersih] hasil inventaris
         Tekan ENTER untuk kembali ke menu.
[bersih] MENU UTAMA → Pilih: 1
[bersih] KELOLA API TOKEN → Pilih: 2
[bersih] daftar token
         Tekan ENTER untuk kembali ke menu.
[bersih] KELOLA API TOKEN → Pilih: 0
[bersih] MENU UTAMA
```

- Banner hanya di layar pertama.
- Tindakan langsung (menu utama 3, 4, 5, 7, 9, 10 dan semua pilihan di dalam sub-menu) diakhiri jeda ENTER. Masuk ke sub-menu (menu utama 1, 2, 6, 8, 11) dan pilihan `0` tidak diberi jeda.
- Pilihan tidak dikenal: pesan kuning, jeda ENTER, lalu menu lagi.
- Pesan ERROR dari tindakan tampil (merah) sebelum jeda ENTER.

### 7.4 Pemetaan warna

| Warna | Dipakai untuk |
|---|---|
| Biru tebal | Judul layar: banner `INFORMASI PROGRAM`, `MENU UTAMA`, judul sub-menu, `RINGKASAN RENCANA`, `URUTAN LANGKAH`, `DRY-RUN`, `TINDAKAN MANUAL`, `PINDAHKAN EMAIL`, dan judul sejenis |
| Hijau | Pesan sukses (token tersimpan, konfirmasi diterima, persetujuan tercatat, batch tersimpan, `OK`), prompt `Ketik YAKIN` |
| Merah | `ERROR:`, `GAGAL`, `RENCANA DITOLAK`, baris "akan DIHAPUS" di cleanup, prompt `Ketik HAPUS`, `Ketik HAPUS EMAIL`, `Ketik ROLLBACK` |
| Kuning | `PERINGATAN:`, prompt `Ketik LANJUT`, `Ketik CUTOVER`, `Ketik ULANG/PASSWORD/LEWATI`, pilihan tidak dikenal |
| Status di tabel | Hijau: status baik (mis. `AKTIF`, `SUPPORTED`, `SSH_READY`, `COMPLETED`, `PASS`). Merah: status gagal (mis. `DITOLAK`, `UNSUPPORTED`, `FAILED`, `BLOCKED`, `FAIL`). Kuning: perlu perhatian (mis. `UNKNOWN`, `MANUAL`, `WAITING_MANUAL`, `WARN`, `SSL_PENDING`). Lainnya warna bawaan |

Status di empat tabel `tabwriter` (account list, audit, ssh test, status batch) dibungkus kode dengan panjang byte yang sama untuk setiap sel di kolom itu, termasuk sel yang tidak berwarna, sehingga kolom tetap rata.

### 7.5 Tata letak `account list`

```
hostinger-backupdata   AKTIF   diuji 2026-11-23 02:39 UTC
  u147413601  Premium v3   1 web
  u195016552  Premium v3   2 web
  u226240492  Starter v3   1 web
hostinger-internalaudit  AKTIF   diuji 2026-11-23 02:38 UTC
  u258871322  Premium v3   1 web
```

Format persis nama paket mengikuti `PlanLabel` yang sudah ada.

### 7.6 Rilis

- Rilis `1.0.0-rc4` di `dist/migrator.exe` + `SHA256SUMS` (D-018), versi di README dan CLAUDE.md diperbarui.
- D-052 di `docs/decisions.md`: go-isatty langsung dan aturan warna/layar.

## 8. Verifikasi

1. Unit test di `internal/cli`: warna mati untuk output non-terminal, sel status punya panjang byte sama, tabel tetap rata setelah kode warna dibuang, tata letak `account list`.
2. `gofmt -l .`, `go vet ./...`, cek tanpa komentar, test paket yang berjalan di Windows.
3. Build exe, lalu uji visual di jendela konsol Windows nyata (tangkapan layar) memakai salinan `dist/data` di folder sementara. Hanya perintah baca: `account list`, `account test` (GET ke API), menu status. Tidak ada operasi tulis ke Hostinger.

## 9. Diserahkan ke Pelaksana

- Nama fungsi dan struktur file pembantu warna.
- Daftar persis pesan sukses yang diberi hijau, mengikuti pemetaan 7.4.
- Daftar persis nilai status per tabel, mengikuti pemetaan 7.4.
