# Desain: Hapus Pemeriksaan Kapasitas Tujuan dari Planner

| Info | Nilai |
|---|---|
| Tanggal | 24 November 2026 |
| Ukuran | Perubahan kecil |
| Status | Disetujui operator (24 November 2026) |

## 1. Tujuan dan Alasan

Saat menyusun rencana (menu 4 / `migrator plan`), planner menolak rencana karena pemeriksaan kapasitas akun tujuan: jatah website dari tabel `kuota_paket`, sisa disk dari `df`, dan jatah database. Angka itu tidak mencerminkan batas paket Hostinger yang sebenarnya (D-023: `df` di shared hosting bukan kuota paket), sehingga rencana sering gagal padahal kapasitas bisa dikelola operator sendiri di hPanel. Operator memutuskan kapasitas tujuan di luar tanggung jawab tool.

## 2. Sasaran

1. Pemeriksaan `KUOTA_WEBSITE`, `DISK_TUJUAN`, dan `KUOTA_DATABASE` dihapus dari planner: tidak dicek, tidak tampil, tidak menolak, tidak meminta `LANJUT`.
2. Layar pilih akun tujuan tidak lagi menampilkan "sisa kuota website" (dan tidak lagi memanggil API per akun untuk itu). Baris akun menampilkan paket dan jumlah website dari inventaris lokal: `(hostinger_business_v3) · 46 web`.

## 3. Yang Tetap

- Pemeriksaan lain: token aktif, akun sumber dan tujuan berbeda, inventaris ada/umur inventaris, pilihan website, bentrok domain di akun tujuan (tetap membaca daftar website tujuan lewat API), website di batch lain, SSH sumber, SSH tujuan, `DUMMY_ONLY`.
- Capability audit kode `QUOTA` (`df`) tetap ada sebagai informasi audit, tidak dipakai planner.
- Ringkasan rencana tetap menampilkan perkiraan ukuran backup, jumlah database, dan mailbox.

## 4. Kompatibilitas Config

Config dibaca ketat (kunci tak dikenal = error). Agar `config.yaml` lama yang masih memuat `kuota_paket` tetap bisa dibuka, kunci `kuota_paket` **tetap diterima** di struktur config tetapi tidak dipakai lagi. Kunci itu dihapus dari `configs/default.yaml` dan dari README, sehingga config baru tidak memuatnya.

## 5. Konsekuensi yang Diterima Operator

Bila kapasitas tujuan memang penuh, kegagalan baru muncul saat migrasi berjalan (mis. pembuatan website dummy ditolak Hostinger, ekstrak gagal karena disk penuh). Website itu berstatus `FAILED` atau checkpoint manual, dan bisa diulang dengan `retry` setelah kapasitas ditambah.

## 6. Pendekatan

| Pendekatan | Pilihan operator |
|---|---|
| **Hapus ketiga pemeriksaan (dipilih)** | Tidak dicek dan tidak tampil |
| Tampilkan sebagai info saja | Ditolak: tetap menampilkan angka yang tidak akurat |
| Biarkan, atur `kuota_paket` manual | Ditolak: disk `df` tetap menolak, dan operator tidak ingin mengelola tabel kuota |

## 7. Perubahan

- `internal/planner`: hapus blok `KUOTA_WEBSITE`, `diskCheck`, `dbQuotaCheck`, dan perhitungan kuota yang tidak dipakai lagi. Pembacaan daftar website tujuan untuk `BENTROK_DOMAIN` tetap.
- `internal/cli/plan.go`: label akun tujuan tanpa kuota dan tanpa panggilan API.
- `configs/default.yaml`, README bagian config: tanpa `kuota_paket`.
- Test: unit test planner dan `tests/integration/plan_test.go` disesuaikan. `TestPlanDryRunRejectedWhenQuotaInsufficient` diganti test yang membuktikan rencana tetap diterima walau kuota/disk "kurang".
- Dokumen: D-056 di `docs/decisions.md` (menggantikan aturan kapasitas di D-011, D-025, D-029, PRD 8.6), `docs/uji-lapangan.md` baris B3/B4 diganti.
- Rilis `1.0.0-rc8`.
