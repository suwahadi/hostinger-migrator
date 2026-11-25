# Desain: Menu Cari Website

| Info | Nilai |
|---|---|
| Tanggal | 25 November 2026 |
| Ukuran | Perubahan kecil |
| Status | Disetujui operator (25 November 2026) |

## 1. Tujuan

Operator ingin cepat tahu sebuah website ada di akun hosting dan token mana, tanpa menelusuri daftar ratusan website. Contoh yang diminta operator:

```
Ditemukan: (1). serene-papayawhip-goldfish-35.hostingersite.com ada di akun: u920879458 - hostinger-ilhamiahnure
[0] Kembali ke menu utama
```

## 2. Sasaran

1. Menu utama baru **12. Cari website** (nomor menu lain tidak bergeser).
2. Isian `Cari (ketik sebagian domain, 0 = kembali): `. Pencarian substring tanpa membedakan huruf besar/kecil terhadap nama domain.
3. **Sumber data (pilihan operator):**
   - akun yang sudah punya inventaris: website dari database lokal;
   - akun yang belum punya inventaris: website dari API Hostinger (read-only), diambil **sekali saat menu dibuka**, hanya untuk token yang punya akun belum dipindai, lalu disimpan di memori selama menu terbuka.
   - Saat menu dibuka ditampilkan ringkasan: `Data: <n> akun dari inventaris lokal, <m> akun dari API (<w> website).`
4. Hasil:
   ```
   Ditemukan:
   (1). serene-papayawhip-goldfish-35.hostingersite.com ada di akun: u920879458 - hostinger-ilhamiahnure
   (2). blog.domainanda.com ada di akun: u494983133 - xyz (subdomain)
   (3). toko.id ada di akun: u147413601 - hostinger-backupdata, hostinger-ilhamiahnure

   [0] Kembali ke menu utama
   ```
   - Label `(subdomain)` / `(parked)` untuk jenis selain main/addon.
   - Akun yang terdaftar di lebih dari satu token menampilkan semua aliasnya.
   - Maksimal 30 hasil; lebih dari itu: `Menampilkan 30 dari <n>. Persempit kata kunci.` (kuning).
   - Tanpa hasil: `Tidak ada website yang cocok dengan "<kata>".` (kuning).
5. Layar dibersihkan sebelum setiap hasil pencarian (D-052). Judul `CARI WEBSITE` biru.
6. Bila pengambilan API satu token gagal (jaringan, token ditolak): peringatan kuning `Token <alias>: daftar website tidak bisa diambil (<sebab>); akun yang belum dipindai di token ini tidak ikut dicari.` Pencarian tetap berjalan dengan data yang ada.
7. ENTER kosong: tanya lagi. `0`: kembali ke menu utama.

## 3. Non-sasaran

- Perintah CLI langsung (`migrator cari ...`).
- Informasi tambahan per hasil (tipe, ukuran, status batch).
- Menyimpan hasil API ke database lokal (itu tugas inventaris).

## 4. Pendekatan

| Pendekatan | Pilihan operator |
|---|---|
| API untuk semua akun, sekali per buka menu | Tidak dipilih |
| Inventaris lokal saja | Tidak dipilih (27 dari 78 akun belum dipindai) |
| **Lokal untuk akun terpindai, API untuk sisanya (dipilih)** | Lengkap, panggilan API seperlunya |

Pengambilan API memakai `ListWebsites` per token (read-only, dengan pagination yang sudah ada di client), lalu disaring ke akun yang belum punya inventaris.

## 5. Test

- Pencocokan dan pengelompokan: domain sama di akun yang terdaftar di dua token menampilkan kedua alias; label subdomain/parked; batas 30.
- Gabungan sumber: akun berinventaris diambil dari lokal, akun tanpa inventaris dari API tiruan; token yang API-nya gagal memunculkan peringatan dan pencarian tetap jalan.
- Alur menu: `0` kembali, hasil dan "Tidak ada website yang cocok" tampil.

## 6. Rilis

Bersama rilis berikutnya (`1.0.0-rc11`), README (menu 12), D-059.
