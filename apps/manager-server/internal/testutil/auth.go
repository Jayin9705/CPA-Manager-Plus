package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	"golang.org/x/crypto/bcrypt"
)

const (
	AdminEmail    = "admin@example.test"
	AdminPassword = "test-admin-password"
)

// NewAdminAuth returns an isolated in-memory session service for HTTP tests.
// AdminKey is deliberately only a pre-existing opaque session token here; it
// is never checked against the legacy SQLite administrator credential.
func NewAdminAuth(t testing.TB) *adminauthsvc.Service {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(AdminPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test administrator password: %v", err)
	}
	admin := adminauthsvc.ActiveAdmin{
		ID:           1,
		Email:        AdminEmail,
		PasswordHash: string(hash),
	}
	admins := &memoryAdminRepository{admin: admin}
	sessions := &memorySessionRepository{sessions: make(map[string]adminauthsvc.Session)}
	now := time.Now().UTC()
	sessions.sessions[digest(AdminKey)] = adminauthsvc.Session{
		AdminID:             admin.ID,
		AdminEmail:          admin.Email,
		PasswordFingerprint: digest(admin.PasswordHash),
		IssuedAtMS:          now.UnixMilli(),
		ExpiresAtMS:         now.Add(24 * time.Hour).UnixMilli(),
	}
	return adminauthsvc.NewWithBackends(admins, sessions, adminauthsvc.Options{
		SessionTTL:  12 * time.Hour,
		LoginLimit:  10,
		LoginWindow: 5 * time.Minute,
	})
}

type memoryAdminRepository struct {
	mu    sync.RWMutex
	admin adminauthsvc.ActiveAdmin
}

func (r *memoryAdminRepository) FindActiveByEmail(_ context.Context, email string) (adminauthsvc.ActiveAdmin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if strings.EqualFold(strings.TrimSpace(r.admin.Email), strings.TrimSpace(email)) {
		return r.admin, nil
	}
	return adminauthsvc.ActiveAdmin{}, adminauthsvc.ErrAdminNotFound
}

func (r *memoryAdminRepository) FindActiveByID(_ context.Context, id int64) (adminauthsvc.ActiveAdmin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.admin.ID == id {
		return r.admin, nil
	}
	return adminauthsvc.ActiveAdmin{}, adminauthsvc.ErrAdminNotFound
}

func (r *memoryAdminRepository) Close() error { return nil }

type memorySessionRepository struct {
	mu       sync.RWMutex
	sessions map[string]adminauthsvc.Session
}

func (r *memorySessionRepository) Save(_ context.Context, tokenDigest string, session adminauthsvc.Session, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[tokenDigest] = session
	return nil
}

func (r *memorySessionRepository) Load(_ context.Context, tokenDigest string) (adminauthsvc.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[tokenDigest]
	if !ok {
		return adminauthsvc.Session{}, adminauthsvc.ErrSessionNotFound
	}
	return session, nil
}

func (r *memorySessionRepository) Delete(_ context.Context, tokenDigest string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, tokenDigest)
	return nil
}

func (r *memorySessionRepository) ConsumeLoginAttempt(_ context.Context, _ []string, _ int, _ time.Duration) (bool, time.Duration, error) {
	return true, 0, nil
}

func (r *memorySessionRepository) ResetLoginAttempts(_ context.Context, _ []string) error {
	return nil
}

func (r *memorySessionRepository) Close() error { return nil }

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

var _ adminauthsvc.AdminRepository = (*memoryAdminRepository)(nil)
var _ adminauthsvc.SessionRepository = (*memorySessionRepository)(nil)
