# Security: Static Application Security Testing (SAST) dengan CodeQL

## 1. Konfigurasi & Cakupan Analisis

Workflow CodeQL didefinisikan pada [`.github/workflows/codeql.yml`](../.github/workflows/codeql.yml) dan berjalan otomatis pada:
- `push` ke branch `main`, `dev`, dan `feat/**`
- `pull_request` menuju `main` dan `dev`
- Jadwal mingguan (`cron: "30 2 * * 1"`) serta pemicu manual (`workflow_dispatch`)

### Cakupan Kode & Query Suite

| Bahasa CodeQL | Mode Build | Cakupan Kode (Berdasarkan SARIF) | Jumlah Rule (`security-extended`) | Jumlah Temuan Awal |
|---|---|---|---|---|
| `go` | `autobuild` (Go `1.26.0`) | 82 file backend Go (`main.go`, `domain/`, `internal/`) | 35 rules | 4 temuan (3 diperbaiki, 1 *accepted risk*) |
| `javascript-typescript` | `none` | 42 file frontend React/TypeScript (`frontend/src/`) | 103 rules | 0 temuan |

Setiap eksekusi menghasilkan:
1. Upload otomatis hasil analisis ke tab **Security → Code scanning** di GitHub (`upload: always`).
2. Tabel ringkasan temuan pada **GitHub Actions Job Summary** (`$GITHUB_STEP_SUMMARY`).
3. Berkas laporan `.sarif` yang disimpan sebagai **Build Artifact** (`codeql-sarif-go` dan `codeql-sarif-javascript-typescript`) selama 30 hari.

---

## 2. Dokumentasi Temuan Hasil Scan CodeQL (`go.sarif` & `javascript.sarif`)

### A. Temuan Aktual dari Hasil Scan CodeQL

| Rule / CWE | Severity | Lokasi Kode | Potensi Dampak | Keputusan & Alasan | Status |
|---|---|---|---|---|---|
| `go/log-injection` (CWE-117) | Medium | `internal/http/api_viewer.go:216` (`logViewerError`, 2 alur taint) | Parameter `dbID` dari query request (`?db_id=`) dan pesan error dicatat ke `s.logger.Printf` tanpa sanitasi newline, berisiko *log forging* apabila karakter `\r`/`\n` disisipkan. | **Perlu Diperbaiki (Temuan Valid)**: Seluruh argumen (`op`, `user.ID`, `dbID`, `err.Error()`) dibersihkan dengan `stripLogNewlines` sebelum dicetak ke log. | **Diperbaiki** (akan terverifikasi ulang pada scan berikutnya). |
| `go/log-injection` (CWE-117) | Medium | `internal/http/authz.go:144` (`authorizeDB`) | Parameter `dbID` dari input HTTP (`resolveDBID`) masuk ke `s.logger.Printf` saat terjadi kegagalan pemeriksaan otorisasi. | **Perlu Diperbaiki (Temuan Valid)**: Membersihkan `u.ID`, `dbID`, `action`, dan `err.Error()` menggunakan `stripLogNewlines`. | **Diperbaiki** (akan terverifikasi ulang pada scan berikutnya). |
| `go/sql-injection` (CWE-89) | High | `internal/dbmanager/query_guard.go:185` (`RunReadOnlyQuery`) | Query SQL dari pengguna diteruskan ke `tx.QueryContext(ctx, query, args...)`. | **Tidak Diperbaiki: Risiko Diterima (*Accepted Risk / Mitigated by Design*)**: Fitur **SQL Query Explorer** memang bertugas mengeksekusi query read-only dari pengguna. Mitigasi berlapis telah diterapkan: `CheckReadOnlyQuery` (hanya mengizinkan `SELECT`/`WITH`/`SHOW`, menolak DDL/DML & fungsi berbahaya), transaksi `SET TRANSACTION READ ONLY` yang selalu di-rollback, `statement_timeout`, batas baris `QUERY_ROW_LIMIT`, dan RBAC per database. | **Accepted Risk**. |

### B. Perbaikan Preventif Tambahan (Pra-Scan)

Selain temuan pada `go.sarif`, hardening preventif berikut telah diterapkan dan terkonfirmasi bersih pada hasil scan CodeQL (`0` alert pada lokasi terkait):

| Rule / CWE | Severity | Lokasi Kode | Potensi Dampak | Keputusan & Alasan |
|---|---|---|---|---|
| `go/log-injection` (CWE-117) | Medium | `internal/http/middleware_logger.go` (`SanitizedLoggerMiddleware`) | Penyisipan `\r`/`\n` pada HTTP Method dan URL Path saat logging request. | **Diperbaiki**: Diterapkan `stripLogNewlines` serta unit test pada `internal/http/middleware_logger_test.go`. |
| `go/sql-injection` (CWE-89) | High | `internal/migration/repository.go` (`hasPendingActionFor`) | Penggabungan nama kolom ke string SQL literal. | **Diperbaiki**: Dipecah menjadi dua konstanta query literal statis (`rollbackQuery` dan `forwardQuery`). |
| `CWE-400` / Slowloris HTTP DoS | Medium | `main.go` (`http.Server`) | Koneksi HTTP tanpa batas waktu pembacaan header. | **Diperbaiki**: Menetapkan `ReadHeaderTimeout: 10 * time.Second`. |

### C. Hasil Analisis Frontend (`javascript.sarif`)

- Cakupan: 42 file TypeScript/React (`frontend/src/`), 103 rule CodeQL `security-extended`.
- Hasil: **0 temuan (`0 alerts`)**.
- Catatan: Tidak ditemukan penggunaan sink berbahaya seperti `dangerouslySetInnerHTML`, `innerHTML`, `eval()`, maupun penyimpanan token rahasia di `localStorage` (autentikasi menggunakan cookie `__Host-sid` `HttpOnly` + `Secure` dan CSRF double-submit).

---

## 3. Keterbatasan SAST

Hasil scan bersih (`0 alerts` pada `javascript-typescript`, maupun setelah perbaikan `go/log-injection`) **tidak berarti aplikasi pasti aman sepenuhnya**. CodeQL menganalisis pola aliran data (*taint tracking*) statis sesuai cakupan rule `security-extended`, sehingga tetap perlu dilengkapi dengan *automated integration test* otorisasi, review konfigurasi TLS/Caddy, serta verifikasi *business logic* (seperti aturan LIFO rollback dan *Group Leader approval*).
