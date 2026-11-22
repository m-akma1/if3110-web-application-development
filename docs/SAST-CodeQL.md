# Security: Static Application Security Testing (SAST) dengan CodeQL

## 1. Konfigurasi & Cakupan Analisis

Workflow CodeQL didefinisikan pada [`.github/workflows/codeql.yml`](../.github/workflows/codeql.yml) dan berjalan otomatis pada:
- `push` ke branch `main`, `dev`, dan `feat/**`
- `pull_request` menuju `main` dan `dev`
- Jadwal mingguan (`cron: "30 2 * * 1"`) serta pemicu manual (`workflow_dispatch`)

### Cakupan Kode & Query Suite

| Bahasa CodeQL | Mode Build | Cakupan Kode | Query Suite |
|---|---|---|---|
| `go` | `autobuild` (dengan Go sesuai `go.mod`) | Seluruh backend & worker Go (`main.go`, `domain/`, `internal/`) | `security-extended` |
| `javascript-typescript` | `none` | Seluruh frontend SPA React/TypeScript (`frontend/src/`) | `security-extended` |

Setiap eksekusi menghasilkan:
1. Upload otomatis hasil analisis ke tab **Security → Code scanning** di GitHub (`upload: always`).
2. Tabel ringkasan temuan pada **GitHub Actions Job Summary** (`$GITHUB_STEP_SUMMARY`).
3. Berkas laporan `.sarif` yang disimpan sebagai **Build Artifact** (`codeql-sarif-go` dan `codeql-sarif-javascript-typescript`) selama 30 hari.

---

## 2. Dokumentasi Temuan & Evaluasi Keamanan

### A. Perbaikan Preventif Hasil Audit Kode (Sebelum / Saat Integrasi SAST)

Sebelum dan saat mengaktifkan CodeQL, dilakukan pemeriksaan terhadap pola *source-to-sink* yang umum terdeteksi oleh query `security-extended`:

| Rule / CWE | Severity | Lokasi Kode | Potensi Dampak | Keputusan & Alasan | Status / Referensi |
|---|---|---|---|---|---|
| `go/log-injection` (CWE-117) | Medium | `internal/http/middleware_logger.go` (`SanitizedLoggerMiddleware`) | Penyerang dapat menyisipkan karakter `\r` atau `\n` ke URL path / HTTP method untuk memalsukan entri log aplikasi (*log forging*). | **Diperbaiki (Temuan Valid)**: Menambahkan helper `stripLogNewlines` untuk menghapus `\r` dan `\n` pada `r.Method` serta `SanitizeURL(r.URL)` sebelum dicetak ke `logger.Printf`. | Diterapkan pada commit `feat/security` (bersama penambahan workflow CodeQL). |
| `go/sql-injection` (CWE-89) | High | `internal/migration/repository.go` (`hasPendingActionFor`) | Penggabungan string kolom ke dalam literal query SQL (`WHERE ` + `col` + ` = $1`) berisiko disalahgunakan jika suatu saat parameter tidak lagi terbatas pada dua nilai internal. | **Diperbaiki (Hardening Preventif)**: Mengganti penggabungan string dengan dua konstanta query SQL terpisah (`rollbackQuery` dan `forwardQuery`) yang 100% statis dengan parameter bind `$1, $2`. | Diterapkan pada commit `feat/security`. |
| `CWE-400` / Slowloris HTTP DoS | Medium | `main.go` (`http.ListenAndServe`) | Server Go tanpa `ReadHeaderTimeout` berpotensi menahan koneksi HTTP terbuka terlalu lama saat menerima header lambat. | **Diperbaiki (Hardening Preventif)**: Menggunakan `http.Server` dengan `ReadHeaderTimeout: 10 * time.Second`. | Diterapkan pada commit `feat/security`. |

### B. Pola Tervalidasi Aman (Potensi *False Positive* / *Mitigated by Design*)

Beberapa konstruksi dinamis di bawah ini memang disengaja untuk kebutuhan fitur platform dan telah dilindungi lapisan validasi ketat:

| Rule / CWE | Severity | Lokasi Kode | Potensi Dampak | Keputusan & Alasan |
|---|---|---|---|---|
| `go/sql-injection` (CWE-89) | High | `internal/dbmanager/registry.go` (`InspectTableDataAndSchema`) | Query `SELECT COUNT(*) FROM ...` dan `SELECT * FROM ...` menggunakan nama schema/tabel dinamis. | **False Positive / Mitigated**: Nama schema & tabel tidak pernah dipakai mentah dari input user; wajib diverifikasi keberadaannya via `describeTable` (`pg_catalog`), di-escape menggunakan `quoteIdent`, dan dijalankan di dalam transaksi `READ ONLY`. |
| `go/sql-injection` (CWE-89) | High | `internal/authn/repository.go` (`getUser`) | `getUser` menerima string klausa `where` untuk `SELECT`. | **False Positive**: Fungsi `getUser` bersifat private dan hanya dipanggil oleh `GetUserByUsername`, `GetUserByEmail`, dan `GetUserByID` dengan klausa statis (`username = $1`, `email = $1`, `id = $1`). |
| `CWE-89` (Eksekusi SQL Migrasi) | High | `internal/migration/engine.go`, `internal/dbmanager/query_guard.go` | Eksekusi script SQL dari pengguna ke target PostgreSQL. | **Accepted Risk (Core Feature)**: Menjalankan SQL pengguna adalah fungsi inti platform. Mitigasi dilakukan dengan RBAC ketat (`VIEW_EDIT` + approval Group Leader untuk migrasi; guard `READ ONLY` + parser AST/token untuk query viewer) serta isolasi kredensial per database target. |

---

## 3. Catatan Bukti Hasil Scan & Keterbatasan SAST

1. **Bukti Hasil Scan GitHub Actions & Code Scanning:**
   Setelah melakukan `git push` ke GitHub:
   - Buka tab **Actions → CodeQL SAST** pada repositori GitHub untuk melihat ringkasan jumlah temuan pada setiap bahasa (`go` dan `javascript-typescript`) serta mengunduh file SARIF pada bagian **Artifacts**.
   - Buka tab **Security → Code scanning** (pada repositori publik atau repositori dengan GitHub Code Security aktif) untuk memverifikasi status setiap alert.
   - Apabila muncul temuan baru setelah integrasi fitur dari anggota tim lain (seperti *Queue/Worker*, *Batching*, atau *Observability*), lakukan perbaikan atau dokumentasikan keputusannya pada tabel di atas beserta referensi commit perbaikannya.
2. **Keterbatasan SAST:**
   Hasil scan bersih (`0 alerts`) **tidak berarti aplikasi pasti aman sepenuhnya**. CodeQL memfokuskan analisis pada aliran data (*taint tracking*) dan pola kerentanan statis yang terdaftar pada suite `security-extended`, sehingga tidak menggantikan pengujian otorisasi *business logic*, review konfigurasi infrastruktur (`Caddyfile`, `.env`, TLS), maupun *automated security/integration test*.
