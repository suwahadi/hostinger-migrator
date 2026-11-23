# Desain: Pencarian di Layar Pilihan Wizard

| Info | Nilai |
|---|---|
| Tanggal | 23 November 2026 |
| Ukuran | Perubahan kecil |
| Status | Disetujui operator (23 November 2026) |

## 1. Tujuan dan Alasan

Dengan data nyata (puluhan akun, ratusan sampai ribuan website), layar pilihan di wizard mencetak daftar penuh dan meminta nomor. Operator harus menggulir dan mencocokkan nomor satu per satu. Pencarian berdasarkan domain atau akun membuat pemilihan cepat dan tidak rawan salah pilih.

## 2. Sasaran

1. **Menu 4, pilih website sumber:** daftar penuh tidak lagi dicetak di awal. Operator mengetik sebagian domain atau id akun, hasil yang cocok tampil bernomor, lalu operator memilih nomor. Pencarian bisa diulang, pilihan terkumpul.
2. **Layar pilih akun** (akun tujuan di menu 4, akun yang dipindai ulang di menu 3, akun di menu 2 SSH): bila akun lebih dari 20, operator mencari dulu (sebagian id akun atau domain di akun itu), lalu memilih nomor dari hasil. Bila 20 atau kurang, daftar tampil seperti sekarang.

## 3. Non-sasaran

- Membatalkan pilihan yang sudah terpilih, rentang nomor (`1-5`), pencarian fuzzy (salah ketik), pencarian berdasarkan tipe atau ukuran.
- Perintah langsung (`migrator plan --web ...`, `--akun-sumber`, `inventory --akun`) tidak berubah.
- Layar lain (token, rollback, tindakan manual) tidak berubah.

## 4. Anti-sasaran

- Website dari akun lain ikut masuk batch (aturan satu batch untuk satu akun sumber harus tetap berlaku).
- `A` memilih website yang tidak terlihat di layar.
- Layar akun tujuan tetap memanggil API kuota untuk semua akun sebelum operator mencari.

## 5. Batasan

- Input satu baris (`bufio`) seperti sekarang. Tanpa dependency baru.
- Teks bahasa Indonesia, tanpa em dash, tanpa komentar di kode.
- Warna dan layar bersih mengikuti D-052 (judul biru, peringatan kuning, `Terpilih` hijau).

## 6. Pendekatan

| Pendekatan | Ringkasan | Alasan dipakai atau tidak |
|---|---|---|
| **A. Cari dulu, lalu pilih dari hasil (dipilih operator)** | Isian `Cari:` lalu `Pilih nomor` dari hasil | Paling jelas untuk data ratusan; operator memilih ini |
| B. Ketik domain/akun langsung di satu isian | Satu isian untuk domain lengkap, id akun, atau kata kunci | Kurang jelas kapan sedang mencari dan kapan memilih |
| C. Daftar akun ringkas, lalu website per akun | Dua langkah | Lebih lambat bila domain diketahui tapi akunnya tidak |
| Pencarian langsung dengan tombol panah (gaya `fzf`) | Filter interaktif per huruf | Butuh dependency baru dan mode raw terminal yang tidak andal di Git Bash tanpa `winpty` |

## 7. Desain

### 7.1 Pencocokan

- Kata kunci dicocokkan tanpa membedakan huruf besar/kecil, sebagai potongan teks (substring).
- Website cocok bila domain atau id akunnya memuat kata kunci.
- Akun cocok bila id akun, atau salah satu domain website di akun itu, memuat kata kunci.

### 7.2 Menu 4: pilih website sumber

```
Token hostinger-internalaudit: 18 akun, 642 website.
Ketik sebagian domain atau id akun. ENTER = selesai.

Cari: toko
Hasil "toko" (3):
 1. u597522494  toko.id        Laravel   850 MB
 2. u597522494  tokobaru.com   WordPress 1,2 GB
 3. u668721044  tokoku.net     Statis    40 MB  [EXPIRED]
Pilih nomor (pisah koma), A = semua hasil, ENTER = cari lagi: 1,2
Terpilih (u597522494): toko.id, tokobaru.com

Cari:
```

- `Cari:` kosong: selesai bila sudah ada pilihan; bila belum, `Belum ada website dipilih.` dan tanya lagi.
- Isian berisi koma, atau sama persis dengan satu domain: setiap nama yang sama persis dengan domain langsung terpilih; nama yang tidak ditemukan dilaporkan kuning. Isian satu kata tanpa kecocokan persis diperlakukan sebagai kata kunci.
- Hasil paling banyak 30 baris. Bila lebih: `Menampilkan 30 dari 214. Persempit kata kunci.` `A` hanya memilih hasil yang tampil.
- Tidak ada hasil: `Tidak ada website yang cocok dengan "x".` (kuning), lalu kembali ke `Cari:`.
- Nomor tidak valid: `Pilihan "x" tidak valid.` (kuning), lalu tanya nomor lagi.
- Kolom baris hasil: id akun, domain, tipe, ukuran, dan penanda status domain yang sama dengan inventaris (mis. `[EXPIRED]`).
- Aturan satu akun: pilihan pertama menentukan akun batch; pilihan dari akun lain diabaikan dengan pesan yang sudah ada (`Satu batch hanya untuk satu akun sumber ...`).
- `Terpilih (uXXX): ...` dicetak hijau setelah setiap penambahan.

### 7.3 Layar pilih akun

```
Ditemukan 92 akun di token hostinger-ilhamiahnure.
Ketik sebagian id akun atau domain, atau nomor/id akun lengkap.
Cari akun: toko
Hasil "toko" (2):
 1. u597522494  (hostinger_business_v3)  toko.id, tokobaru.com
 2. u668721044  (hostinger_business_v5)  tokoku.net
Pilih nomor: 1
```

- 20 akun atau kurang: daftar tampil seperti sekarang, dan isian tetap menerima kata kunci.
- Lebih dari 20: daftar tidak dicetak, langsung `Cari akun:`.
- Id akun lengkap selalu langsung terpilih.
- Baris hasil menampilkan sampai 3 domain yang cocok.
- Menu 3: `ENTER = semua` tetap berlaku di isian pertama.
- Akun tujuan (menu 4): sisa kuota hanya dihitung untuk akun yang tampil di hasil.
- Menu 2 SSH: baris hasil tetap menampilkan status SSH.

### 7.4 Test

- Unit test pencocokan (domain, id akun, huruf besar/kecil, batas 30, `A` hanya hasil tampil, domain lengkap berkoma, aturan satu akun).
- Unit test pemilih akun (≤ 20 tampil daftar, > 20 cari dulu, id lengkap, ENTER = semua di menu 3).
- `tests/integration/plan_test.go` `TestPlanInteractiveWizard` disesuaikan dengan alur baru.

### 7.5 Rilis

- Rilis `1.0.0-rc7` di `dist/migrator.exe` + `SHA256SUMS`; README (menu 4) dan `docs/uji-lapangan.md` diperbarui; D-055 di `docs/decisions.md`.

## 8. Diserahkan ke Pelaksana

- Nama fungsi dan struktur file pembantu pencarian.
- Format kolom persis baris hasil, mengikuti fungsi label yang sudah ada (`typeLabel`, `sizeLabel`, `PlanLabel`).
