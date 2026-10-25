package authn_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/ids"
)

// seedTestUsers mendaftarkan akun uji melalui RegisterUser (pengganti SeedDefaultUsers yang sudah dihapus)
func seedTestUsers(t *testing.T, store *authn.SessionStore) {
	t.Helper()
	accounts := []authn.RegisterInput{
		{Username: "admin", Email: "admin@email.com", Password: "SuperSecretPass999!", Role: authn.RolePlatformAdmin},
		{Username: "moreno", Email: "moreno@email.com", Password: "Rahasia123", Role: authn.RolePlatformAdmin},
		{Username: "dhia", Email: "dhia@email.com", Password: "PasswordDhia123!", Role: authn.RoleUser},
		{Username: "caca", Password: "PasswordCaca123!", Role: authn.RoleUser},
	}
	for _, acc := range accounts {
		if _, err := store.RegisterUser(context.Background(), acc); err != nil {
			t.Fatalf("gagal mendaftarkan akun uji %s: %v", acc.Username, err)
		}
	}
}

// 1. Menguji Identifikasi Principal: Argon2id Hashing & Pemuatan Unique Identifier Permanen (UUID)
func TestPrincipalIdentification_HashingAndPermanentID(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()

	rawPassword := "SangatRahasia#2026"

	createdUser, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "budi_santoso", Password: rawPassword})
	if err != nil {
		t.Fatalf("gagal mendaftarkan user: %v", err)
	}
	permanentID := createdUser.ID
	if !ids.IsValid(permanentID) {
		t.Fatalf("ID user seharusnya UUID, dapat: %q", permanentID)
	}
	if createdUser.Role != authn.RoleUser {
		t.Fatalf("role default seharusnya USER, dapat: %s", createdUser.Role)
	}

	if !strings.HasPrefix(createdUser.PasswordHash, "$argon2id$") {
		t.Fatalf("password seharusnya di-hash dengan Argon2id, dapat: %s", createdUser.PasswordHash)
	}

	// Pastikan PasswordHash tidak pernah bocor saat di-serialize ke JSON
	serialized, err := json.Marshal(createdUser)
	if err != nil {
		t.Fatalf("gagal marshal JSON user: %v", err)
	}
	if strings.Contains(string(serialized), createdUser.PasswordHash) || strings.Contains(string(serialized), rawPassword) {
		t.Fatalf("PasswordHash bocor pada JSON: %s", string(serialized))
	}

	// Verifikasi password salah wajib ditolak
	if _, err := store.Authenticate(ctx, "budi_santoso", "PasswordSalah123"); err == nil {
		t.Fatalf("autentikasi dengan password salah seharusnya gagal")
	}

	// Verifikasi password benar berhasil dan memuat Unique Identifier Permanen (permanentID)
	user, err := store.Authenticate(ctx, "budi_santoso", rawPassword)
	if err != nil {
		t.Fatalf("autentikasi seharusnya berhasil, dapat error: %v", err)
	}
	if user.ID != permanentID {
		t.Fatalf("unique identifier permanen tidak sesuai: dapat %s, ingin %s", user.ID, permanentID)
	}
}

// 2. Menguji Idle Timeout dan Absolute Expiration
func TestSessionLifecycle_IdleAndAbsoluteExpiration(t *testing.T) {
	ctx := context.Background()
	cfg := authn.SessionConfig{
		IdleTimeout:        30 * time.Minute,
		AbsoluteExpiration: 2 * time.Hour,
		RotationInterval:   10 * time.Hour, // matikan rotasi berkala di test ini agar fokus ke expiry
		GracePeriod:        15 * time.Second,
	}

	repo := authn.NewMemoryRepository()
	store := authn.NewSessionStoreWithRepo(repo, cfg)

	currentTime := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return currentTime })
	seedTestUsers(t, store)

	// Skenario A: Idle Timeout
	tokenIdle, _, err := store.Login(ctx, "admin", "SuperSecretPass999!", "")
	if err != nil {
		t.Fatalf("login gagal: %v", err)
	}

	// Majukan waktu 20 menit (< 30m idle timeout) -> masih valid & memperbarui idle timeout
	currentTime = currentTime.Add(20 * time.Minute)
	if _, err := store.ValidateAndRefresh(ctx, tokenIdle); err != nil {
		t.Fatalf("sesi seharusnya masih aktif pada menit ke-20: %v", err)
	}

	// Majukan waktu 31 menit tanpa aktivitas -> wajib expired karena Idle Timeout (30m)
	currentTime = currentTime.Add(31 * time.Minute)
	if _, err := store.ValidateAndRefresh(ctx, tokenIdle); err == nil {
		t.Fatalf("sesi seharusnya berakhir karena melewati Idle Timeout")
	}

	// Skenario B: Absolute Expiration (tetap berakhir meski user aktif terus-menerus)
	currentTime = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	tokenAbs, _, err := store.Login(ctx, "admin", "SuperSecretPass999!", "")
	if err != nil {
		t.Fatalf("login gagal: %v", err)
	}

	// Pengguna aktif setiap 20 menit selama 100 menit (5x20m = 1h40m < 2h Absolute Expiration)
	for i := 1; i <= 5; i++ {
		currentTime = currentTime.Add(20 * time.Minute)
		if _, err := store.ValidateAndRefresh(ctx, tokenAbs); err != nil {
			t.Fatalf("iterasi %d (menit %d): sesi seharusnya masih aktif: %v", i, i*20, err)
		}
	}

	// Majukan 21 menit lagi (total 121 menit > 120 menit Absolute Expiration)
	// Meskipun baru saja aktif 21 menit lalu (< 30m Idle Timeout), sesi WAJIB ditolak karena Absolute Expiration!
	currentTime = currentTime.Add(21 * time.Minute)
	if _, err := store.ValidateAndRefresh(ctx, tokenAbs); err == nil {
		t.Fatalf("sesi seharusnya ditolak karena telah melewati batas Absolute Expiration (2 jam)")
	}
}

// 3. Menguji Rotasi Sesi (Berkala & Peningkatan Privilege) + Grace Period Request Paralel Tanpa Memperpanjang Absolute Expiration
func TestSessionLifecycle_RotationAndParallelGracePeriod(t *testing.T) {
	ctx := context.Background()
	cfg := authn.SessionConfig{
		IdleTimeout:        30 * time.Minute,
		AbsoluteExpiration: 12 * time.Hour,
		RotationInterval:   15 * time.Minute,
		GracePeriod:        15 * time.Second,
	}

	repo := authn.NewMemoryRepository()
	store := authn.NewSessionStoreWithRepo(repo, cfg)

	currentTime := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return currentTime })
	seedTestUsers(t, store)

	// 1. Login awal menghasilkan token_1
	token1, _, err := store.Login(ctx, "dhia", "PasswordDhia123!", "")
	if err != nil {
		t.Fatalf("login gagal: %v", err)
	}

	resInitial, err := store.ValidateAndRefresh(ctx, token1)
	if err != nil {
		t.Fatalf("validasi awal gagal: %v", err)
	}
	initialAbsExpiry := resInitial.Session.AbsoluteExpiresAt

	// 2. Majukan waktu 16 menit (>= 15m RotationInterval)
	currentTime = currentTime.Add(16 * time.Minute)

	// Request Utama A masuk dengan token1 -> memicu rotasi berkala menjadi token2
	resRotate, err := store.ValidateAndRefresh(ctx, token1)
	if err != nil {
		t.Fatalf("validasi saat rotasi berkala gagal: %v", err)
	}
	if !resRotate.Rotated || resRotate.NewOpaqueID == "" || resRotate.NewOpaqueID == token1 {
		t.Fatalf("seharusnya sesi dirotasi dan menghasilkan opaque token baru")
	}
	token2 := resRotate.NewOpaqueID

	// Pastikan Absolute Expiration TIDAK diperpanjang oleh rotasi!
	if !resRotate.Session.AbsoluteExpiresAt.Equal(initialAbsExpiry) {
		t.Fatalf("rotasi sesi dilarang memperpanjang absolute_expires_at: awal=%v, sesudah=%v",
			initialAbsExpiry, resRotate.Session.AbsoluteExpiresAt)
	}

	// 3. Request Paralel B tiba 5 detik kemudian (masih dalam 15 detik Grace Period) membawa token1 (token lama)
	currentTime = currentTime.Add(5 * time.Second)
	resParallel, err := store.ValidateAndRefresh(ctx, token1)
	if err != nil {
		t.Fatalf("request paralel dengan token lama selama grace period seharusnya diizinkan, dapat error: %v", err)
	}
	if !resParallel.UsedGraceSlot || resParallel.Rotated {
		t.Fatalf("request paralel pada grace period seharusnya ditandai UsedGraceSlot=true dan tidak memicu rotasi ulang")
	}
	if !resParallel.Session.AbsoluteExpiresAt.Equal(initialAbsExpiry) {
		t.Fatalf("request paralel dilarang memperpanjang absolute_expires_at")
	}

	// 4. Majukan waktu 12 detik lagi (total 17 detik sejak rotasi > 15 detik Grace Period)
	// Token lama (token1) sekarang WAJIB ditolak, sedangkan token baru (token2) tetap aktif!
	currentTime = currentTime.Add(12 * time.Second)
	if _, err := store.ValidateAndRefresh(ctx, token1); err == nil {
		t.Fatalf("token lama (token1) seharusnya ditolak setelah melewati masa grace period")
	}
	if _, err := store.ValidateAndRefresh(ctx, token2); err != nil {
		t.Fatalf("token baru (token2) seharusnya tetap valid: %v", err)
	}

	// 5. Rotasi saat Peningkatan Privilege (USER -> PLATFORM_ADMIN)
	token3, elevatedProfile, err := store.RotateOnPrivilegeChange(ctx, token2, authn.RolePlatformAdmin)
	if err != nil {
		t.Fatalf("rotasi saat peningkatan privilege gagal: %v", err)
	}
	if token3 == "" || token3 == token2 {
		t.Fatalf("peningkatan privilege wajib menghasilkan session identifier baru")
	}
	if elevatedProfile.Role != authn.RolePlatformAdmin {
		t.Fatalf("role pengguna seharusnya meningkat menjadi PLATFORM_ADMIN, dapat: %s", elevatedProfile.Role)
	}

	// Selama grace period 15 detik setelah peningkatan privilege, token2 masih diizinkan untuk request paralel yang sedang terbang
	currentTime = currentTime.Add(3 * time.Second)
	if _, err := store.ValidateAndRefresh(ctx, token2); err != nil {
		t.Fatalf("token sebelum peningkatan privilege (token2) seharusnya masih diizinkan selama grace period singkat: %v", err)
	}

	// Pastikan token3 memiliki absolute_expires_at yang tetap sama dengan saat login awal
	resToken3, err := store.ValidateAndRefresh(ctx, token3)
	if err != nil {
		t.Fatalf("token3 seharusnya valid: %v", err)
	}
	if !resToken3.Session.AbsoluteExpiresAt.Equal(initialAbsExpiry) {
		t.Fatalf("rotasi peningkatan privilege dilarang memperpanjang absolute_expires_at")
	}
}

// 4. Menguji Penonaktifan Sesi Seketika (Logout & Pencabutan Akses Pengguna)
func TestSessionLifecycle_InstantRevocationOnLogoutAndAccessRevoke(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	seedTestUsers(t, store)

	// Skenario A: Logout menonaktifkan sesi seketika (termasuk token dalam grace period)
	tokenA, _, err := store.Login(ctx, "caca", "PasswordCaca123!", "")
	if err != nil {
		t.Fatalf("login caca gagal: %v", err)
	}

	// Rotasi sesi caca sehingga ada tokenA (dalam grace period) dan tokenB (aktif)
	tokenB, _, err := store.RotateOnPrivilegeChange(ctx, tokenA, authn.RolePlatformAdmin)
	if err != nil {
		t.Fatalf("rotasi privilege gagal: %v", err)
	}

	// Pengguna logout menggunakan tokenB
	if err := store.RevokeSession(ctx, tokenB); err != nil {
		t.Fatalf("gagal melakukan logout: %v", err)
	}

	// Baik tokenB maupun tokenA (meski waktu grace period belum habis) WAJIB langsung ditolak seketika!
	if _, err := store.ValidateAndRefresh(ctx, tokenB); err == nil {
		t.Fatalf("tokenB seharusnya langsung tidak valid setelah logout")
	}
	if _, err := store.ValidateAndRefresh(ctx, tokenA); err == nil {
		t.Fatalf("tokenA (grace period) seharusnya juga langsung tidak valid setelah sesi di-logout")
	}

	// Skenario B: Pencabutan akses pengguna (RevokeUserAccess) menonaktifkan semua sesi di berbagai perangkat seketika
	tokenDevice1, profile, err := store.Login(ctx, "Moreno", "Rahasia123", "")
	if err != nil {
		t.Fatalf("login device 1 gagal: %v", err)
	}
	tokenDevice2, _, err := store.Login(ctx, "Moreno", "Rahasia123", "")
	if err != nil {
		t.Fatalf("login device 2 gagal: %v", err)
	}

	// Cabut akses pengguna Moreno berdasarkan permanent User ID
	if err := store.RevokeUserAccess(ctx, profile.ID); err != nil {
		t.Fatalf("gagal mencabut akses user: %v", err)
	}

	// Kedua sesi perangkat wajib langsung ditolak seketika
	if _, err := store.ValidateAndRefresh(ctx, tokenDevice1); err == nil {
		t.Fatalf("sesi device 1 seharusnya langsung nonaktif saat akses user dicabut")
	}
	if _, err := store.ValidateAndRefresh(ctx, tokenDevice2); err == nil {
		t.Fatalf("sesi device 2 seharusnya langsung nonaktif saat akses user dicabut")
	}
}
