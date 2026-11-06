package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	setupTTL         = 15 * time.Minute
	maxPendingSetups = 16
	maxEmailLen      = 254
	auditEmailLen    = 254
)

var (
	// ErrInvalidCredentials covers any login failure (e-mail, senha ou código).
	ErrInvalidCredentials = errors.New("credenciais inválidas")
	// ErrRateLimited is returned while the client IP is blocked.
	ErrRateLimited = errors.New("muitas tentativas; tente novamente mais tarde")
	// ErrInvalidEmail is returned when the setup e-mail is malformed.
	ErrInvalidEmail = errors.New("e-mail inválido")
	// ErrSetupTokenInvalid is returned for an unknown or expired setup token.
	ErrSetupTokenInvalid = errors.New("token de configuração inválido ou expirado")
	// ErrSetupTokenRequired is returned when the one-time setup token is wrong.
	ErrSetupTokenRequired = errors.New("token de setup inválido")
	// ErrInvalidTOTP is returned when the setup confirmation code is wrong.
	ErrInvalidTOTP = errors.New("código TOTP inválido")
)

var totpCodeRe = regexp.MustCompile(`^\d{6}$`)

// dummyHash is verified against when the e-mail is unknown, so a login takes
// the same time whether or not the account exists.
var dummyHash = func() string {
	h, err := HashPassword("korego-dummy-password-for-timing")
	if err != nil {
		panic(fmt.Sprintf("gerar hash fictício: %v", err))
	}
	return h
}()

// SetupChallenge is returned by BeginSetup for the user to enroll TOTP.
type SetupChallenge struct {
	Token, Secret, OTPAuthURL string
}

// LoginResult describes a freshly created session.
type LoginResult struct {
	Token, CSRFToken string
	User             store.User
	ExpiresAt        time.Time
}

type pendingSetup struct {
	email, passwordHash, secret string
	expires                     time.Time
}

// Service implements the setup, login and logout flows.
type Service struct {
	DB       *store.DB
	Box      *secrets.Box
	Sessions *Sessions
	Limiter  *Limiter
	Audit    *audit.Logger
	Now      func() time.Time
	// SetupToken is the one-time token required by BeginSetup (empty rejects
	// every setup). SetupTokenFile, if set, is removed once setup completes.
	SetupToken     string
	SetupTokenFile string

	mu      sync.Mutex // guards pending and SetupToken after construction
	pending map[string]pendingSetup
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// NeedsSetup reports whether no user exists yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.DB.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("contar usuários: %w", err)
	}
	return n == 0, nil
}

// BeginSetup checks the one-time setup token, validates the admin credentials
// and keeps them, already hashed, in memory for setupTTL together with a new
// TOTP secret.
func (s *Service) BeginSetup(ctx context.Context, setupToken, email, password, ip string) (SetupChallenge, error) {
	if !s.Limiter.Attempt(ip, s.now()) {
		return SetupChallenge{}, ErrRateLimited
	}
	need, err := s.NeedsSetup(ctx)
	if err != nil {
		return SetupChallenge{}, err
	}
	if !need {
		return SetupChallenge{}, store.ErrSetupDone
	}
	s.mu.Lock()
	want := s.SetupToken
	s.mu.Unlock()
	if want == "" || subtle.ConstantTimeCompare([]byte(setupToken), []byte(want)) != 1 {
		return SetupChallenge{}, ErrSetupTokenRequired
	}
	email, err = normalizeEmail(email)
	if err != nil {
		return SetupChallenge{}, err
	}
	if err := ValidatePasswordPolicy(password); err != nil {
		return SetupChallenge{}, err
	}
	hash, err := HashPasswordCtx(ctx, password)
	if err != nil {
		return SetupChallenge{}, err
	}
	secret, url, err := NewTOTPSecret(email)
	if err != nil {
		return SetupChallenge{}, fmt.Errorf("gerar segredo TOTP: %w", err)
	}
	raw, err := randomBytes(32)
	if err != nil {
		return SetupChallenge{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.prunePending(now)
	if s.pending == nil {
		s.pending = map[string]pendingSetup{}
	}
	for len(s.pending) >= maxPendingSetups {
		s.evictOldestPending()
	}
	s.pending[token] = pendingSetup{email: email, passwordHash: hash, secret: secret, expires: now.Add(setupTTL)}
	return SetupChallenge{Token: token, Secret: secret, OTPAuthURL: url}, nil
}

// prunePending drops expired pending setups. Caller holds s.mu.
func (s *Service) prunePending(now time.Time) {
	for tok, p := range s.pending {
		if !now.Before(p.expires) {
			delete(s.pending, tok)
		}
	}
}

// evictOldestPending drops the pending setup that expires first (all share the
// same TTL, so it is the oldest). Caller holds s.mu.
func (s *Service) evictOldestPending() {
	var oldest string
	var at time.Time
	for tok, p := range s.pending {
		if oldest == "" || p.expires.Before(at) {
			oldest, at = tok, p.expires
		}
	}
	delete(s.pending, oldest)
}

// ConfirmSetup checks the TOTP code for a pending setup, creates the admin and
// returns a session plus the plaintext recovery codes (shown only once).
func (s *Service) ConfirmSetup(ctx context.Context, token, code, ip, ua string) (LoginResult, []string, error) {
	now := s.now()
	if !s.Limiter.Attempt(ip, now) {
		return LoginResult{}, nil, ErrRateLimited
	}
	s.mu.Lock()
	s.prunePending(now)
	p, ok := s.pending[token]
	s.mu.Unlock()
	if !ok {
		return LoginResult{}, nil, ErrSetupTokenInvalid
	}
	step, ok := CheckTOTP(p.secret, stripSpaces(code), now, 0)
	if !ok {
		return LoginResult{}, nil, ErrInvalidTOTP
	}
	enc, err := s.Box.Encrypt([]byte(p.secret))
	if err != nil {
		return LoginResult{}, nil, err
	}
	plain, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return LoginResult{}, nil, err
	}
	id, err := s.DB.CreateAdminIfNone(ctx, store.User{
		Email:         p.email,
		PasswordHash:  p.passwordHash,
		Role:          "admin",
		TOTPSecretEnc: enc,
		TOTPLastStep:  step,
	}, hashes)
	if err != nil {
		return LoginResult{}, nil, err
	}
	// Setup is done: no pending setup may be confirmed and the setup token
	// is spent.
	s.mu.Lock()
	s.pending = nil
	s.SetupToken = ""
	s.mu.Unlock()
	if s.SetupTokenFile != "" {
		if err := os.Remove(s.SetupTokenFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("remover %s: %v", s.SetupTokenFile, err)
		}
	}
	s.Limiter.Reset(ip)

	s.record(ctx, audit.Entry{UserID: &id, Action: "auth.setup", IP: ip})
	res, err := s.startSession(ctx, id, ip, ua)
	if err != nil {
		return LoginResult{}, nil, err
	}
	return res, plain, nil
}

// Login authenticates with e-mail, password and a TOTP or recovery code.
func (s *Service) Login(ctx context.Context, email, password, code, ip, ua string) (LoginResult, error) {
	now := s.now()
	if !s.Limiter.Attempt(ip, now) {
		return LoginResult{}, ErrRateLimited
	}
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.DB.UserByEmail(ctx, email)
	found := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return LoginResult{}, fmt.Errorf("buscar usuário: %w", err)
	}
	hash := dummyHash
	if found {
		hash = u.PasswordHash
	}
	pwOK, err := VerifyPasswordCtx(ctx, hash, password)
	if err != nil {
		return LoginResult{}, err
	}
	if !found || !pwOK {
		return LoginResult{}, s.loginFailed(ctx, ip, email)
	}
	codeOK, err := s.checkCode(ctx, u, code, now)
	if err != nil {
		return LoginResult{}, err
	}
	if !codeOK {
		return LoginResult{}, s.loginFailed(ctx, ip, email)
	}
	s.Limiter.Reset(ip)
	s.record(ctx, audit.Entry{UserID: &u.ID, Action: "auth.login", IP: ip})
	return s.startSession(ctx, u.ID, ip, ua)
}

// loginFailed audits a failed login; the attempt was already counted by
// Limiter.Attempt.
func (s *Service) loginFailed(ctx context.Context, ip, email string) error {
	if len(email) > auditEmailLen {
		email = email[:auditEmailLen]
	}
	s.record(ctx, audit.Entry{Action: "auth.login_failed", IP: ip, Metadata: map[string]any{"email": email}})
	return ErrInvalidCredentials
}

// checkCode accepts a 6-digit TOTP code (with anti-replay) or a recovery code.
func (s *Service) checkCode(ctx context.Context, u store.User, code string, now time.Time) (bool, error) {
	code = stripSpaces(code)
	if code == "" {
		return false, nil
	}
	if !totpCodeRe.MatchString(code) {
		return s.DB.UseRecoveryCode(ctx, u.ID, HashRecoveryCode(code))
	}
	secret, err := s.Box.Decrypt(u.TOTPSecretEnc)
	if err != nil {
		return false, err
	}
	step, ok := CheckTOTP(string(secret), code, now, u.TOTPLastStep)
	if !ok {
		return false, nil
	}
	// A concurrent login with the same code loses this conditional update.
	return s.DB.AdvanceTOTPStep(ctx, u.ID, step)
}

func (s *Service) startSession(ctx context.Context, userID int64, ip, ua string) (LoginResult, error) {
	u, err := s.DB.UserByID(ctx, userID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("buscar usuário: %w", err)
	}
	token, sess, err := s.Sessions.Create(ctx, userID, ip, ua)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, CSRFToken: sess.CSRFToken, User: u, ExpiresAt: sess.ExpiresAt}, nil
}

// Logout ends the session with the given id.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.DB.DeleteSession(ctx, sessionID)
}

// Record writes an audit entry; failures are logged, never returned, so that
// an audit problem does not leave a flow half done.
func (s *Service) Record(ctx context.Context, e audit.Entry) { s.record(ctx, e) }

func (s *Service) record(ctx context.Context, e audit.Entry) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, e); err != nil {
		log.Printf("auditoria %s: %v", e.Action, err)
	}
}

func normalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Name != "" || a.Address != e {
		return "", ErrInvalidEmail
	}
	return e, nil
}

func stripSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
