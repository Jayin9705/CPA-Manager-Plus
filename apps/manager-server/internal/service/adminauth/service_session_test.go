package adminauth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoginCreatesOpaqueDigestBackedSession(t *testing.T) {
	service, admins, sessions, now := newSessionTestService(t)

	result, err := service.Login(context.Background(), "  ADMIN@Example.Test ", "correct-password", "203.0.113.9")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.Token == "" || strings.Contains(sessions.savedDigest, result.Token) {
		t.Fatalf("session token was empty or persisted verbatim: token=%q digest=%q", result.Token, sessions.savedDigest)
	}
	if sessions.savedDigest != tokenDigest(result.Token) || len(sessions.savedDigest) != 64 {
		t.Fatalf("saved digest = %q", sessions.savedDigest)
	}
	if result.Session.AdminID != admins.admin.ID || result.Session.AdminEmail != "admin@example.test" {
		t.Fatalf("session administrator = %#v", result.Session)
	}
	if result.Session.ExpiresAtMS-result.Session.IssuedAtMS != int64(12*time.Hour/time.Millisecond) {
		t.Fatalf("session lifetime = %dms", result.Session.ExpiresAtMS-result.Session.IssuedAtMS)
	}
	if !result.ExpiresAt.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("expiresAt = %s", result.ExpiresAt)
	}
	for _, key := range sessions.attemptKeys {
		if strings.Contains(key, "admin@example.test") || strings.Contains(key, "203.0.113.9") {
			t.Fatalf("rate-limit key contains identifying input: %q", key)
		}
	}
}

func TestLoginUsesUniformCredentialErrorAndRateLimit(t *testing.T) {
	service, admins, sessions, _ := newSessionTestService(t)
	admins.missing = true
	if _, err := service.Login(context.Background(), "nobody@example.test", "wrong", "198.51.100.1"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("missing administrator error = %v", err)
	}

	admins.missing = false
	if _, err := service.Login(context.Background(), admins.admin.Email, "wrong", "198.51.100.1"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong password error = %v", err)
	}

	sessions.allowAttempts = false
	sessions.retryAfter = 17 * time.Second
	admins.emailLookups = 0
	_, err := service.Login(context.Background(), admins.admin.Email, "correct-password", "198.51.100.1")
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) || rateLimitErr.RetryAfter != 17*time.Second {
		t.Fatalf("rate limit error = %#v", err)
	}
	if admins.emailLookups != 0 {
		t.Fatalf("rate-limited request performed %d administrator lookups", admins.emailLookups)
	}
}

func TestSessionIsRevokedWhenAdministratorChangesOrDisappears(t *testing.T) {
	service, admins, sessions, _ := newSessionTestService(t)
	result, err := service.Login(context.Background(), admins.admin.Email, "correct-password", "192.0.2.10")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	verified, err := service.Session(context.Background(), result.Token)
	if err != nil || verified.AdminEmail != admins.admin.Email {
		t.Fatalf("verify session = %#v, %v", verified, err)
	}

	changedHash, err := bcrypt.GenerateFromPassword([]byte("changed-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash changed password: %v", err)
	}
	admins.admin.PasswordHash = string(changedHash)
	if _, err := service.Session(context.Background(), result.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("changed-password session error = %v", err)
	}
	if sessions.deletedDigest != tokenDigest(result.Token) {
		t.Fatalf("deleted digest = %q", sessions.deletedDigest)
	}

	admins.admin.PasswordHash = sessions.originalPasswordHash
	result, err = service.Login(context.Background(), admins.admin.Email, "correct-password", "192.0.2.10")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	admins.missing = true
	if _, err := service.Session(context.Background(), result.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("removed-administrator session error = %v", err)
	}
}

func TestLogoutDeletesOnlySessionTokenDigest(t *testing.T) {
	service, admins, sessions, _ := newSessionTestService(t)
	result, err := service.Login(context.Background(), admins.admin.Email, "correct-password", "192.0.2.11")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := service.Logout(context.Background(), result.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if sessions.deletedDigest != tokenDigest(result.Token) || sessions.deletedDigest == result.Token {
		t.Fatalf("logout deleted key = %q", sessions.deletedDigest)
	}
	if ok, err := service.VerifySession(context.Background(), result.Token); err != nil || ok {
		t.Fatalf("logged-out session verification = %v, %v", ok, err)
	}
}

func TestVerifyHeaderFallsThroughWhenLegacyStoreIsDisabled(t *testing.T) {
	service, _, _, _ := newSessionTestService(t)
	ok, err := service.VerifyHeader(context.Background(), "Bearer cpamp_invalid_legacy_key")
	if err != nil || ok {
		t.Fatalf("session-only legacy header verification = %v, %v", ok, err)
	}
}

func newSessionTestService(t testing.TB) (*Service, *sessionTestAdmins, *sessionTestSessions, time.Time) {
	t.Helper()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	admins := &sessionTestAdmins{admin: ActiveAdmin{
		ID:           42,
		Email:        "admin@example.test",
		PasswordHash: string(passwordHash),
	}}
	sessions := &sessionTestSessions{
		allowAttempts:        true,
		items:                make(map[string]Session),
		originalPasswordHash: string(passwordHash),
	}
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	service := NewWithBackends(admins, sessions, Options{
		SessionTTL:  12 * time.Hour,
		LoginLimit:  10,
		LoginWindow: 5 * time.Minute,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x5a}, sessionTokenBytes*8)),
		Now:         func() time.Time { return now },
	})
	return service, admins, sessions, now
}

type sessionTestAdmins struct {
	admin        ActiveAdmin
	missing      bool
	emailLookups int
}

func (r *sessionTestAdmins) FindActiveByEmail(_ context.Context, email string) (ActiveAdmin, error) {
	r.emailLookups++
	if r.missing || normalizeEmail(r.admin.Email) != email {
		return ActiveAdmin{}, ErrAdminNotFound
	}
	return r.admin, nil
}

func (r *sessionTestAdmins) FindActiveByID(_ context.Context, id int64) (ActiveAdmin, error) {
	if r.missing || r.admin.ID != id {
		return ActiveAdmin{}, ErrAdminNotFound
	}
	return r.admin, nil
}

func (r *sessionTestAdmins) Close() error { return nil }

type sessionTestSessions struct {
	allowAttempts        bool
	retryAfter           time.Duration
	attemptKeys          []string
	savedDigest          string
	deletedDigest        string
	originalPasswordHash string
	items                map[string]Session
}

func (r *sessionTestSessions) Save(_ context.Context, digest string, session Session, _ time.Duration) error {
	r.savedDigest = digest
	r.items[digest] = session
	return nil
}

func (r *sessionTestSessions) Load(_ context.Context, digest string) (Session, error) {
	session, ok := r.items[digest]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return session, nil
}

func (r *sessionTestSessions) Delete(_ context.Context, digest string) error {
	r.deletedDigest = digest
	delete(r.items, digest)
	return nil
}

func (r *sessionTestSessions) ConsumeLoginAttempt(_ context.Context, keys []string, _ int, _ time.Duration) (bool, time.Duration, error) {
	r.attemptKeys = append([]string(nil), keys...)
	return r.allowAttempts, r.retryAfter, nil
}

func (r *sessionTestSessions) ResetLoginAttempts(_ context.Context, _ []string) error {
	return nil
}

func (r *sessionTestSessions) Close() error { return nil }
