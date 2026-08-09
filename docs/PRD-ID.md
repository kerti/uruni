# Uruni — Dokumen Kebutuhan Produk (PRD)

**Versi 0.1 · 2026-08-08 · Status: draf (pra-kode)**

Dokumen pendamping: [`Positioning.md`](./Positioning.md) · [`Decisions.md`](./Decisions.md). Berlandaskan wawancara bendahara #1 (disimpan di vault kerja privat, bukan di repo publik ini). PRD ini menerjemahkan dokumen-dokumen tersebut menjadi kebutuhan konkret untuk versi pertama.

*Catatan: ini terjemahan dari `PRD.md` (bahasa Inggris), yang tetap menjadi versi acuan.*

---

## 1. Ringkasan

Uruni adalah aplikasi mobile, ditopang oleh **server milik komunitas sendiri yang selalu aktif (self-hosted)**, yang memungkinkan seorang bendahara — yang terpaksa dan bukan akuntan — mencatat setiap transaksi langsung dari ponselnya dan selalu tahu bahwa saldo yang tercatat cocok dengan uang yang sebenarnya. Versi pertama melayani satu persona yang sudah tervalidasi dan mengerjakan satu tugas dengan sangat baik: **menjaga dana bersama sebuah kelompok kecil tetap rapi dan jujur, dengan sesedikit mungkin harus berpikir.**

## 2. Masalah (tervalidasi)

Dari wawancara #1 (bendahara unit kantor, 8 orang, ~Rp 1–2 juta/bulan):

- Tugas yang paling sulit dan paling tidak disukainya adalah **rekonsiliasi** — mencocokkan saldo yang tercatat dengan uang yang sebenarnya. Uang berada di dua tempat (dompet tunai + rekening bank *pribadi* yang tercampur dengan gaji), kas rutin dan kas insidentil sering tergabung, dan ada saja yang mengambil uang tunai untuk belanja lalu lupa mencatatnya. Akibatnya angkanya melenceng dan ia harus mencari-cari selisihnya.
- **Menagih yang telat bukanlah beban** (kelompok kecil, semua bayar habis gajian, ditagih langsung sudah cukup).
- **Transparansi bukan kebutuhan yang dirasakan** — anggota tidak pernah membaca catatannya — meskipun ia ingin fitur itu *tetap ada* karena tanggung jawab moral.
- Saat ini ia memakai laptop (Google Sheets), tetapi **ingin mengerjakan semuanya dari ponsel**.
- Syarat agar mau pindah: harus **lebih sederhana daripada spreadsheet dan cukup andal untuk menggantikannya** — "kalau malah bikin tambah rumit" adalah pembatal; "tidak usah berpikir" adalah impiannya.

## 3. Pengguna sasaran

**Utama:** bendahara dadakan sebuah kelompok kecil (kira-kira 5–20 orang) yang mengelola iuran rutin ditambah pengumpulan sesekali, ingin bekerja dari ponsel, dan memiliki akses ke seseorang yang cukup teknis untuk menjalankan server (untuk pengguna wawancara #1, orang itu adalah pemelihara aplikasi/maintainer).

**Secara eksplisit bukan (v1):** kelompok besar (RT dengan puluhan kepala keluarga), bendahara tanpa pendamping teknis, dan siapa pun yang butuh pembukuan setara akuntansi. Sesuai prinsip proyek, kita tidak menggeneralisasi secara berlebihan.

## 4. Tujuan & bukan-tujuan

**Tujuan**

1. Mencatat transaksi apa pun dari ponsel dalam beberapa ketukan, di mana saja.
2. Selalu menampilkan saldo berjalan yang jujur, dan membuat rekonsiliasi (catatan vs. uang tunai + bank yang sebenarnya) menjadi mudah.
3. Memodelkan bentuk uang kelompok yang sebenarnya: dua lokasi, tag tujuan, iuran berjenjang, dan penggantian (reimbursement).
4. Cukup andal untuk menjadi satu-satunya catatan — aman meski ponsel hilang.
5. Lebih sederhana daripada spreadsheet yang digantikannya.

**Bukan-tujuan (v1)**

- Pengingat pembayaran / penagihan otomatis.
- Login anggota atau portal anggota (sebagai gantinya ada tautan laporan publik hanya-baca — tanpa akun).
- Integrasi QRIS/payment gateway, sinkronisasi bank, atau alur yang mewajibkan nota.
- Jurnal akuntansi, dasbor analitik, atau multi-mata-uang.
- Segala hal dalam daftar "sistem operasi RT" (data warga, pemungutan suara, pengaduan, inventaris).

## 5. Prinsip (mengikat)

- Kelompok kecil lebih dulu
- Lebih sederhana daripada spreadsheet, cukup andal untuk menggantikannya
- Mengutamakan ponsel (mobile-first)
- Angkanya selalu cocok, dan Uruni membuktikannya
- Catatan yang tidak bisa hilang
- Datamu tetap milikmu (self-hosted; proyek tidak menyimpan apa pun)
- Gratis selamanya
- Tanpa penambahan fitur berlebihan (no feature creep)

## 6. Konsep inti & model informasi

Satu server komunitas menampung satu atau lebih **Dana (Fund)** (untuk pengguna yang tervalidasi, satu dana bersama sudah cukup; modelnya memungkinkan lebih tanpa menambah kerumitan antarmuka).

Entitas utama:

- **Dana (Fund)** — kas bersama. Memiliki nama dan mata uang (IDR).
- **Akun/Lokasi** — tempat uang secara fisik berada: `Tunai` (dompet) dan `Bank`. Saldo tercatat dilacak *per lokasi* karena pemisahan inilah sumber selisihnya. (v1 mengasumsikan rekening bank bisa jadi rekening pribadi; Uruni hanya melacak bagian yang merupakan kas sesuai yang dilaporkan bendahara.)
- **Tag tujuan** — setiap transaksi diberi tag: `Kas Utama` (rutin), sebuah **Insidentil** bernama (mis. "Duka Pak Budi"), atau `Titipan/Pass-through` (mis. Kas Bidang). Satu saldo riil yang tergabung, dipisahkan *secara makna*, bukan dalam pos yang terpisah-pisah.
- **Anggota** — nama + peran/jenjang. Tidak butuh email/nomor telepon (meminimalkan data yang disimpan).
- **Tarif iuran** — nominal per jenjang (mis. pelaksana 50rb, fungsional pertama 70rb, muda 80rb, madya belum ditentukan); dapat diubah; berlaku menurut waktu.
- **Transaksi** — pemasukan atau pengeluaran; nominal; tanggal; lokasi; tag tujuan; kaitan anggota opsional (untuk iuran/penggantian); catatan opsional; foto nota opsional. Bersifat permanen setelah diposting (koreksi dibuat sebagai entri penyesuaian baru) agar catatan tetap tepercaya.
- **Pengumpulan insidentil** — amplop ringan: peruntukan/peristiwa, kontribusi masuk, penyaluran keluar, dan **sisa** yang bisa dialihkan ke Kas Utama dengan satu ketukan.
- **Snapshot rekonsiliasi** — rekaman pada satu titik waktu tentang saldo yang diharapkan vs. yang sebenarnya per lokasi, selisih yang ada, dan cara penyelesaiannya.

## 7. Kebutuhan fungsional

### 7.1 Penyiapan & akses
- Bendahara masuk ke server *komunitasnya sendiri* (satu peran bendahara di v1; peran "hanya-lihat" opsional menyusul). Tidak ada akun Uruni terpusat.
- Penyiapan awal: memberi nama dana, menambahkan anggota beserta jenjangnya, mengatur tarif iuran, mengatur saldo awal untuk Tunai dan Bank.

### 7.2 Mencatat transaksi (tindakan sehari-hari)
- Tombol "tambah" yang menonjol dan bisa dijangkau dengan satu ketukan dari layar utama.
- Kolom minimal dengan nilai bawaan yang cerdas: nominal, masuk/keluar, lokasi (mengingat yang terakhir), tujuan (bawaan Kas Utama), tanggal (bawaan hari ini). Catatan dan foto opsional.
- Pencatatan harus terasa responsif; server adalah satu-satunya sumber kebenaran dan mengonfirmasi penulisan dengan cepat.
- Konektivitas: **aplikasi membutuhkan koneksi aktif.** Saat offline, aplikasi sengaja tidak tersedia — dengan status "butuh koneksi" yang jelas, tanpa data lokal dan tanpa antrean. Ini pilihan pengguna: ia lebih memilih tidak ada kerancuan "salinan mana yang benar?" ketimbang kemampuan offline apa pun.

### 7.3 Iuran
- Melihat anggota dan, untuk periode berjalan, siapa yang sudah bayar / bayar sebagian / bayar di muka (ada yang membayar beberapa bulan sekaligus).
- Menandai pembayaran iuran (nominal terisi otomatis dari jenjang anggota, dapat diubah; lokasi; tunai/transfer).
- Tampilan sederhana "belum bayar". **Tanpa pengingat, tanpa penagihan otomatis.**

### 7.4 Penggantian (reimbursement)
- Mencatat bahwa seorang anggota menalangi lebih dulu → menjadi utang kepadanya.
- Menyelesaikan penggantian saat dibayar kembali. Nota opsional dan tidak pernah wajib (parkir Rp 2.000 tak perlu nota).

### 7.5 Pengumpulan insidentil
- Membuat insidentil untuk sebuah peristiwa (sakit, kematian, sunatan, pensiun).
- Mengumpulkan kontribusi sekali jalan dan mencatat penyalurannya.
- Saat ditutup, tampilkan sisanya dan tawarkan **alihkan ke Kas Utama** dengan satu ketukan.

### 7.6 Titipan (Kas Bidang)
- Mencatat uang yang dikumpulkan atas nama organisasi di atasnya beserta penerusannya, sehingga tidak pernah menggelembungkan saldo dana sendiri.

### 7.7 Saldo & layar utama
- Layar utama menampilkan: total saldo saat ini, saldo per lokasi (Tunai / Bank), dan **status rekonsiliasi**: "cocok" atau "selisih Rp X — cek?".
- Rincian opsional menurut tag tujuan.

### 7.8 Rekonsiliasi (inti dari produk)
- Alur "rekonsiliasi" yang bisa dijalankan bendahara kapan saja: ia memasukkan jumlah *tunai yang sebenarnya* ada dan *saldo kas yang sebenarnya* di bank; Uruni membandingkan masing-masing dengan angka yang tercatat.
- Jika cocok: konfirmasi kecil yang memuaskan.
- Jika berbeda: tampilkan selisih per lokasi, daftarkan transaksi terbaru untuk membantunya menemukan entri yang hilang/terganda, lalu izinkan ia menambahkan transaksi yang hilang atau memposting **penyesuaian** bercatatan untuk merapikannya. Setiap rekonsiliasi disimpan sebagai snapshot.
- (Pertimbangan ke depan, bukan v1: mendorong penggunaan rekening kas khusus non-pribadi untuk menghilangkan percampuran dari sumbernya.)

### 7.9 Laporan publik yang bisa dibagikan
- Server menyediakan **halaman laporan publik hanya-baca** pada tautan yang stabil dan sulit ditebak. Bendahara membagikan tautannya sekali saja; tautan tetap berlaku selama umur aplikasi (tanpa perlu diganti).
- Siapa pun yang punya tautan bisa membukanya tanpa login.
- Halaman menampilkan **semuanya secara bawaan** dan menyediakan **filter** (bulan, tujuan/tag, anggota, pemasukan/pengeluaran, status iuran) agar pemirsa publik mudah menelusuri data.
- Pengaman: slug acak yang panjang + `noindex` agar tidak terindeks mesin pencari; ada opsi **"buat ulang tautan"** sebagai jalan darurat bila tautan bocor (tidak diperlukan dalam pemakaian normal). Konsekuensi yang diterima: karena semuanya ditampilkan, URL publik menampakkan nama anggota dan status pembayaran — dapat diterima mengingat niat transparansi bendahara dan rendahnya sensitivitas data.
- Ini halaman bersama, bukan portal anggota — tanpa akun, tanpa login anggota.

### 7.10 Cadangan / ekspor
- **Ekspor seluruh data ke JSON** (kanonik, bisa dipulihkan melalui impor) yang dapat diunduh bendahara kapan saja. **Workbook Excel** ditawarkan sebagai format sekunder yang mudah dibaca manusia (belum tentu bisa diimpor kembali).
- **Cadangan terjadwal di sisi server** yang dapat diaktifkan host (cadangan otomatis berkala yang ditulis di server).
- **Pengiriman lewat email opsional** untuk cadangan berkala (host perlu mengonfigurasi SMTP).

## 8. Kebutuhan non-fungsional

- **Keandalan & cadangan:** server adalah satu-satunya sumber kebenaran; kehilangan ponsel tidak menghilangkan apa pun. Cadangan berupa **ekspor JSON seluruh data** (kanonik, bisa dipulihkan lewat impor), dengan **workbook Excel** opsional sebagai format sekunder, ditambah **cadangan terjadwal di sisi server** dan **pengiriman lewat email** opsional yang bisa diaktifkan host (lihat 7.10).
- **Bisa di-self-host:** **image Docker siap pakai + templat `docker compose`** dengan konfigurasi minimal; host cukup menarik image, tidak perlu meng-compile. Sertakan TLS/reverse-proxy (mis. Caddy) karena tautan laporan publik butuh HTTPS. Kemudahan pemasangan (deployment UX) inilah penentu hidup-mati bagi siapa pun yang tak punya pendamping teknis.
- **Autentikasi:** **autentikasi lokal** untuk satu bendahara di v1 — mandiri, tanpa penyedia identitas eksternal yang perlu dikonfigurasi per instans. OIDC/OAuth opsional bisa menyusul. Halaman laporan publik (7.9) sengaja tanpa autentikasi.
- **Minimalkan data:** kumpulkan hanya yang dibutuhkan tugas (nama, nominal, tanggal, catatan). Tidak perlu info kontak anggota.
- **Platform klien: PWA** — bisa dipasang ke layar utama, tanpa app store (sekaligus melenyapkan nuansa lisensi app-store sebelumnya). Membutuhkan koneksi aktif untuk berfungsi (lihat 7.2).
- **Lisensi:** AGPL-3.0 (klausul jaringan menjaga setiap instans yang di-host tetap terbuka).
- **Pelokalan:** Bahasa Indonesia lebih dulu; format IDR/Rupiah; salinan yang hangat dan manusiawi sesuai nada merek.

## 9. Kriteria keberhasilan

- Bendahara menjadikan Uruni sebagai catatan **satu-satunya** dan berhenti membuka laptop/spreadsheet.
- Rekonsiliasi bulanannya selesai cepat dan **selisih yang tak terjelaskan menurun mendekati nol**.
- Mencatat transaksi hanya butuh beberapa detik, di ponsel, saat itu juga.
- Ia merasakan hal yang ia inginkan: lebih sedikit yang harus dipikirkan.

## 10. Pertanyaan terbuka

**Diputuskan 2026-08-08:** deployment = image Docker siap pakai + `docker compose` (dengan TLS) · cadangan = ekspor JSON manual (+ Excel opsional), ditambah cadangan terjadwal di sisi server dan pengiriman email opsional · autentikasi = autentikasi lokal (OIDC menyusul) · platform = PWA · offline = aplikasi tidak tersedia saat terputus (tanpa antrean) · laporan publik = menampilkan semuanya dengan filter (menerima penampakan nama/status pembayaran) · rekening kas khusus = ditunda · cakupan persona = n=1 diterima.

Tidak ada lagi pertanyaan terbuka di tingkat produk. Keputusan tingkat implementasi (stack, basis data, framework) dilacak terpisah — lihat `Tech-Design.md` (akan dibuat).

## 11. Di luar cakupan / kemungkinan ke depan

Aplikasi untuk anggota, banyak bendahara/peran, pelaporan yang lebih kaya, tampilan "uang ini menjadi apa" yang bernuansa emosional (nilai tambah, ditunda), panduan rekening khusus, serta persona tambahan (RT/kelompok besar).
