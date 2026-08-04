package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookieName      = "cpamp_session"
	sessionTokenBytes      = 32
	defaultSessionCacheTTL = 2 * time.Second
	maxSessionCacheEntries = 4096
)

// CSRFToken derives a non-secret, per-session synchronizer token from the
// opaque session token. The token is only returned after the session has been
// authenticated; because the session cookie is HttpOnly an unrelated origin
// cannot read or reproduce it. Keeping the derivation stateless also avoids a
// second Redis record and preserves the existing session data model.
func CSRFToken(sessionToken string) string {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return ""
	}
	digest := sha256.Sum256(append([]byte("cpamp-csrf\x00"), []byte(sessionToken)...))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

var (
	ErrAdminNotFound     = errors.New("active administrator not found")
	ErrAmbiguousAdmin    = errors.New("multiple active administrators matched")
	ErrInvalidCredential = errors.New("invalid email or password")
	ErrInvalidSession    = errors.New("invalid or expired session")
	ErrSessionNotFound   = errors.New("session not found")
	ErrUnavailable       = errors.New("administrator authentication service unavailable")
)

type ActiveAdmin struct {
	ID           int64
	Email        string
	PasswordHash string
}

type Session struct {
	AdminID             int64  `json:"admin_id"`
	AdminEmail          string `json:"admin_email"`
	PasswordFingerprint string `json:"password_fingerprint"`
	IssuedAtMS          int64  `json:"issued_at_ms"`
	ExpiresAtMS         int64  `json:"expires_at_ms"`
}

type LoginResult struct {
	Token     string
	Session   Session
	ExpiresAt time.Time
}

type AdminRepository interface {
	FindActiveByEmail(ctx context.Context, normalizedEmail string) (ActiveAdmin, error)
	FindActiveByID(ctx context.Context, id int64) (ActiveAdmin, error)
	Close() error
}

type SessionRepository interface {
	Save(ctx context.Context, tokenDigest string, session Session, ttl time.Duration) error
	Load(ctx context.Context, tokenDigest string) (Session, error)
	Delete(ctx context.Context, tokenDigest string) error
	ConsumeLoginAttempt(ctx context.Context, keys []string, limit int, window time.Duration) (bool, time.Duration, error)
	ResetLoginAttempts(ctx context.Context, keys []string) error
	Close() error
}

type Options struct {
	SessionTTL      time.Duration
	SessionCacheTTL time.Duration
	LoginLimit      int
	LoginWindow     time.Duration
	Random          io.Reader
	Now             func() time.Time
}

type Service struct {
	admins          AdminRepository
	sessions        SessionRepository
	coreStore       *store.Store
	sessionTTL      time.Duration
	sessionCacheTTL time.Duration
	loginLimit      int
	loginWindow     time.Duration
	random          io.Reader
	now             func() time.Time
	dummyHash       []byte
	initialError    error
	sessionCacheMu  sync.Mutex
	sessionCache    map[string]sessionCacheEntry
}

type sessionCacheEntry struct {
	session   Session
	expiresAt time.Time
}

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return "too many login attempts"
}

func New(cfg config.Config, stores ...*store.Store) *Service {
	backendCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var initialError error
	admins, err := newPostgresAdminRepository(backendCtx, cfg.AuthPostgresDSN)
	if err != nil {
		initialError = err
	}
	sessions, err := newRedisSessionRepository(backendCtx, cfg.AuthRedisAddr, cfg.AuthRedisPassword, cfg.AuthRedisDB)
	if err != nil {
		initialError = errors.Join(initialError, err)
	}
	service := NewWithBackends(admins, sessions, Options{
		SessionTTL:  cfg.AuthSessionTTL,
		LoginLimit:  cfg.AuthLoginLimit,
		LoginWindow: cfg.AuthLoginWindow,
	}, initialError)
	if len(stores) > 0 {
		service.coreStore = stores[0]
	}
	return service
}

// VerifyHeader preserves the legacy Admin Key contract for API clients while
// browser traffic uses Redis-backed sessions. The two credentials remain
// independent and are never converted into one another.
func (s *Service) VerifyHeader(ctx context.Context, authorizationHeader string) (bool, error) {
	if s == nil {
		return false, errors.New("administrator authentication service is not configured")
	}
	// A session-only deployment deliberately has no legacy Admin Key store.
	// Treat that as an unsupported credential so the caller can continue with
	// cookie/Bearer session verification instead of turning an invalid key into
	// an internal server error.
	if s.coreStore == nil {
		return false, nil
	}
	credential, ok, err := s.coreStore.LoadAdminCredential(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return security.VerifyAdminKey(credential, security.ExtractBearerToken(authorizationHeader)), nil
}

func (s *Service) VerifyPanelHeader(ctx context.Context, authorizationHeader string) (bool, error) {
	return s.VerifyHeader(ctx, authorizationHeader)
}

func (s *Service) VerifySubmittedExternalConfigHeader(ctx context.Context, authorizationHeader string, _ store.ManagerConfig) (bool, error) {
	return s.VerifyHeader(ctx, authorizationHeader)
}

func (s *Service) PanelUsesExternalManagementKey(context.Context) (bool, error) {
	return false, nil
}

func NewWithBackends(admins AdminRepository, sessions SessionRepository, options Options, initialErrors ...error) *Service {
	if options.SessionTTL <= 0 {
		options.SessionTTL = config.DefaultAuthSessionTTL
	}
	if options.SessionCacheTTL <= 0 {
		options.SessionCacheTTL = defaultSessionCacheTTL
	}
	if options.LoginLimit <= 0 {
		options.LoginLimit = config.DefaultAuthLoginLimit
	}
	if options.LoginWindow <= 0 {
		options.LoginWindow = config.DefaultAuthLoginWindow
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("cpamp-auth-dummy-password"), bcrypt.DefaultCost)
	joined := errors.Join(initialErrors...)
	if err != nil {
		joined = errors.Join(joined, err)
	}
	if admins == nil {
		joined = errors.Join(joined, errors.New("administrator repository is not configured"))
	}
	if sessions == nil {
		joined = errors.Join(joined, errors.New("session repository is not configured"))
	}
	return &Service{
		admins:          admins,
		sessions:        sessions,
		sessionTTL:      options.SessionTTL,
		sessionCacheTTL: options.SessionCacheTTL,
		loginLimit:      options.LoginLimit,
		loginWindow:     options.LoginWindow,
		random:          options.Random,
		now:             options.Now,
		dummyHash:       dummyHash,
		initialError:    joined,
		sessionCache:    make(map[string]sessionCacheEntry),
	}
}

func (s *Service) Login(ctx context.Context, email string, password string, clientIP string) (LoginResult, error) {
	if err := s.ready(); err != nil {
		return LoginResult{}, err
	}
	normalizedEmail := normalizeEmail(email)
	loginKeys := loginAttemptKeys(normalizedEmail, clientIP)
	allowed, retryAfter, err := s.sessions.ConsumeLoginAttempt(ctx, loginKeys, s.loginLimit, s.loginWindow)
	if err != nil {
		return LoginResult{}, unavailable(err)
	}
	if !allowed {
		return LoginResult{}, &RateLimitError{RetryAfter: retryAfter}
	}

	admin, lookupErr := s.admins.FindActiveByEmail(ctx, normalizedEmail)
	hash := s.dummyHash
	if lookupErr == nil {
		hash = []byte(admin.PasswordHash)
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if lookupErr != nil {
		if !errors.Is(lookupErr, ErrAdminNotFound) {
			return LoginResult{}, unavailable(lookupErr)
		}
		return LoginResult{}, ErrInvalidCredential
	}
	if normalizedEmail == "" || password == "" || !passwordMatches {
		return LoginResult{}, ErrInvalidCredential
	}

	token, err := randomSessionToken(s.random)
	if err != nil {
		return LoginResult{}, unavailable(err)
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.sessionTTL)
	session := Session{
		AdminID:             admin.ID,
		AdminEmail:          normalizeEmail(admin.Email),
		PasswordFingerprint: passwordFingerprint(admin.PasswordHash),
		IssuedAtMS:          now.UnixMilli(),
		ExpiresAtMS:         expiresAt.UnixMilli(),
	}
	if err := s.sessions.Save(ctx, tokenDigest(token), session, s.sessionTTL); err != nil {
		return LoginResult{}, unavailable(err)
	}
	if err := s.sessions.ResetLoginAttempts(ctx, loginKeys); err != nil {
		_ = s.sessions.Delete(ctx, tokenDigest(token))
		return LoginResult{}, unavailable(err)
	}
	return LoginResult{Token: token, Session: session, ExpiresAt: expiresAt}, nil
}

func (s *Service) Session(ctx context.Context, token string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 4096 {
		return Session{}, ErrInvalidSession
	}
	digest := tokenDigest(token)
	session, err := s.sessions.Load(ctx, digest)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			s.evictSession(digest)
			return Session{}, ErrInvalidSession
		}
		return Session{}, unavailable(err)
	}
	if session.AdminID <= 0 || session.ExpiresAtMS <= s.now().UTC().UnixMilli() {
		s.evictSession(digest)
		_ = s.sessions.Delete(ctx, digest)
		return Session{}, ErrInvalidSession
	}
	admin, err := s.admins.FindActiveByID(ctx, session.AdminID)
	if err != nil {
		if errors.Is(err, ErrAdminNotFound) {
			s.evictSession(digest)
			_ = s.sessions.Delete(ctx, digest)
			return Session{}, ErrInvalidSession
		}
		return Session{}, unavailable(err)
	}
	wantFingerprint := passwordFingerprint(admin.PasswordHash)
	if subtle.ConstantTimeCompare([]byte(session.PasswordFingerprint), []byte(wantFingerprint)) != 1 {
		s.evictSession(digest)
		_ = s.sessions.Delete(ctx, digest)
		return Session{}, ErrInvalidSession
	}
	session.AdminEmail = normalizeEmail(admin.Email)
	return session, nil
}

func (s *Service) VerifySession(ctx context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 4096 {
		return false, nil
	}
	digest := tokenDigest(token)
	if _, ok := s.cachedSession(digest, s.now().UTC()); ok {
		return true, nil
	}
	session, err := s.Session(ctx, token)
	if err == nil {
		s.cacheSession(digest, session)
		return true, nil
	}
	if errors.Is(err, ErrInvalidSession) {
		return false, nil
	}
	return false, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.ready(); err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	digest := tokenDigest(token)
	s.evictSession(digest)
	if err := s.sessions.Delete(ctx, digest); err != nil && !errors.Is(err, ErrSessionNotFound) {
		return unavailable(err)
	}
	return nil
}

func (s *Service) cachedSession(digest string, now time.Time) (Session, bool) {
	if s == nil || digest == "" {
		return Session{}, false
	}
	s.sessionCacheMu.Lock()
	defer s.sessionCacheMu.Unlock()
	entry, ok := s.sessionCache[digest]
	if !ok || entry.session.ExpiresAtMS <= now.UnixMilli() || !now.Before(entry.expiresAt) {
		if ok {
			delete(s.sessionCache, digest)
		}
		return Session{}, false
	}
	return entry.session, true
}

func (s *Service) cacheSession(digest string, session Session) {
	if s == nil || digest == "" || session.ExpiresAtMS <= 0 {
		return
	}
	ttl := s.sessionCacheTTL
	if ttl <= 0 {
		ttl = defaultSessionCacheTTL
	}
	now := s.now().UTC()
	expiresAt := now.Add(ttl)
	sessionExpiry := time.UnixMilli(session.ExpiresAtMS)
	if sessionExpiry.Before(expiresAt) {
		expiresAt = sessionExpiry
	}
	if !expiresAt.After(now) {
		return
	}
	s.sessionCacheMu.Lock()
	defer s.sessionCacheMu.Unlock()
	if len(s.sessionCache) >= maxSessionCacheEntries {
		for key, entry := range s.sessionCache {
			if !entry.expiresAt.After(now) {
				delete(s.sessionCache, key)
			}
		}
		if len(s.sessionCache) >= maxSessionCacheEntries {
			for key := range s.sessionCache {
				delete(s.sessionCache, key)
				break
			}
		}
	}
	s.sessionCache[digest] = sessionCacheEntry{session: session, expiresAt: expiresAt}
}

func (s *Service) evictSession(digest string) {
	if s == nil || digest == "" {
		return
	}
	s.sessionCacheMu.Lock()
	delete(s.sessionCache, digest)
	s.sessionCacheMu.Unlock()
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.admins != nil {
		errs = append(errs, s.admins.Close())
	}
	if s.sessions != nil {
		errs = append(errs, s.sessions.Close())
	}
	return errors.Join(errs...)
}

func (s *Service) ready() error {
	if s == nil {
		return unavailable(errors.New("service is nil"))
	}
	if s.initialError != nil {
		return unavailable(s.initialError)
	}
	return nil
}

// Ready exposes startup validation without leaking backend details through an
// HTTP request. PostgreSQL-only production mode uses it to fail closed before
// the listener accepts traffic.
func (s *Service) Ready() error {
	return s.ready()
}

func unavailable(err error) error {
	if err == nil {
		return ErrUnavailable
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func randomSessionToken(reader io.Reader) (string, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func passwordFingerprint(passwordHash string) string {
	sum := sha256.Sum256([]byte(passwordHash))
	return hex.EncodeToString(sum[:])
}

func loginAttemptKeys(normalizedEmail string, clientIP string) []string {
	if normalizedEmail == "" {
		normalizedEmail = "<empty>"
	}
	clientIP = strings.TrimSpace(clientIP)
	if clientIP == "" {
		clientIP = "<unknown>"
	}
	return []string{
		"cpamp:auth:login:email:" + tokenDigest(normalizedEmail),
		"cpamp:auth:login:ip:" + tokenDigest(clientIP),
	}
}
