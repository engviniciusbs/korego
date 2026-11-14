package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/engviniciusbs/korego/internal/oauth"
	"github.com/engviniciusbs/korego/internal/store"
)

// ErrNoToken is returned when a secret matches no usable API token.
var ErrNoToken = errors.New("token inválido")

// ErrStaleToken indica um token bem formado cujo hash corresponde a uma linha,
// mas que está vencido, revogado, com o acesso expirado ou já foi trocado.
// Diferente de ErrNoToken, não é um palpite: não deve contar no limitador.
var ErrStaleToken = errors.New("token vencido")

// ErrRefreshReuse indica refresh anterior reapresentado fora da janela de
// graça; a conexão é revogada.
var ErrRefreshReuse = errors.New("refresh reutilizado")

// ErrInvalidTokenName is returned for empty or too long token names.
var ErrInvalidTokenName = errors.New("nome de token inválido")

const (
	tokenPrefix     = "kg_"
	tokenNameMaxLen = 64
	tokenPrefixLen  = 10

	refreshPrefix = "kgr_"

	// AccessTTL é a vida do token de acesso OAuth; RefreshGrace, a janela em que
	// o refresh anterior ainda é aceito (renovações concorrentes ou resposta perdida).
	AccessTTL    = time.Hour
	RefreshGrace = 60 * time.Second

	maxNameAttempts = 20
)

var refreshRe = regexp.MustCompile(`^kgr_[A-Za-z0-9_-]{43}$`)

// OAuthPair é o par de tokens devolvido ao agente.
type OAuthPair struct {
	Access, Refresh string
	ExpiresIn       time.Duration
}

var tokenRe = regexp.MustCompile(`^kg_[A-Za-z0-9_-]{43}$`)

// Tokens manages API tokens backed by the store.
type Tokens struct {
	DB  *store.DB
	Now func() time.Time
	// Key deriva, de forma determinística, o par entregue a cada refresh
	// (HMAC); sem ela Refresh falha. Veja DeriveKey.
	Key []byte
}

// DeriveKey deriva uma subchave da chave mestra para um uso (label).
func DeriveKey(master []byte, label string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// derivePair calcula o par que a rotação do refresh R produz. Ser função só de
// (Key, R) torna a rotação idempotente: quem reapresenta R na janela de graça
// recebe exatamente o mesmo par.
func (t *Tokens) derivePair(refresh string) (access, newRefresh string) {
	mac := func(purpose string) string {
		m := hmac.New(sha256.New, t.Key)
		m.Write([]byte(purpose + "\x00" + refresh))
		return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	}
	return tokenPrefix + mac("access"), refreshPrefix + mac("refresh")
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Issue creates a token and returns its plaintext secret (never stored).
// ttl 0 means the token never expires.
func (t *Tokens) Issue(ctx context.Context, userID int64, name string, ttl time.Duration) (string, store.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > tokenNameMaxLen {
		return "", store.APIToken{}, ErrInvalidTokenName
	}
	raw, err := randomBytes(32)
	if err != nil {
		return "", store.APIToken{}, err
	}
	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := t.now()
	tok := store.APIToken{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(secret),
		Prefix:    secret[:tokenPrefixLen],
		CreatedAt: now,
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		tok.ExpiresAt = &exp
	}
	id, err := t.DB.CreateAPIToken(ctx, tok)
	if err != nil {
		return "", store.APIToken{}, err
	}
	tok.ID = id
	return secret, tok, nil
}

// Validate resolves a secret to a live token and records its use (at most
// once per minute). Malformed or unknown secrets give ErrNoToken; known but
// dead ones (revoked, expired, access expired, replaced access) give ErrStaleToken.
func (t *Tokens) Validate(ctx context.Context, secret, ip string) (store.APIToken, error) {
	if !tokenRe.MatchString(secret) {
		return store.APIToken{}, ErrNoToken
	}
	h := hashToken(secret)
	tok, err := t.DB.APITokenByHash(ctx, h)
	if errors.Is(err, store.ErrNotFound) {
		// Acesso substituído por uma rotação: conhecido, porém morto.
		_, perr := t.DB.APITokenByPrevAccessHash(ctx, h)
		if errors.Is(perr, store.ErrNotFound) {
			return store.APIToken{}, ErrNoToken
		}
		if perr != nil {
			return store.APIToken{}, perr
		}
		return store.APIToken{}, ErrStaleToken
	}
	if err != nil {
		return store.APIToken{}, err
	}
	now := t.now()
	if tok.RevokedAt != nil || (tok.ExpiresAt != nil && !now.Before(*tok.ExpiresAt)) ||
		(tok.AccessExpiresAt != nil && !now.Before(*tok.AccessExpiresAt)) {
		return store.APIToken{}, ErrStaleToken
	}
	if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) >= touchInterval {
		if err := t.DB.TouchAPIToken(ctx, tok.ID, now, ip); err != nil {
			return store.APIToken{}, err
		}
		tok.LastUsedAt, tok.LastUsedIP = &now, ip
	}
	return tok, nil
}

// newPair gera um par kg_/kgr_ novo.
func newPair() (access, refresh string, err error) {
	a, err := oauth.RandomToken()
	if err != nil {
		return "", "", err
	}
	r, err := oauth.RandomToken()
	if err != nil {
		return "", "", err
	}
	return tokenPrefix + a, refreshPrefix + r, nil
}

// IssueOAuth cria uma conexão OAuth. O nome é único por usuário: clientName,
// depois "clientName (2)" até "(20)", cortado para caber em 64 runas.
func (t *Tokens) IssueOAuth(ctx context.Context, userID int64, clientName, clientID string, ttl time.Duration) (OAuthPair, store.APIToken, error) {
	clientName = strings.TrimSpace(clientName)
	if clientName == "" {
		return OAuthPair{}, store.APIToken{}, ErrInvalidTokenName
	}
	access, refresh, err := newPair()
	if err != nil {
		return OAuthPair{}, store.APIToken{}, err
	}
	now := t.now()
	accExp := now.Add(AccessTTL)
	tok := store.APIToken{
		UserID:          userID,
		TokenHash:       hashToken(access),
		Prefix:          access[:tokenPrefixLen],
		CreatedAt:       now,
		Kind:            "oauth",
		ClientID:        clientID,
		AccessExpiresAt: &accExp,
		RefreshHash:     hashToken(refresh),
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		tok.ExpiresAt = &exp
	}
	for i := 1; i <= maxNameAttempts; i++ {
		suffix := ""
		if i > 1 {
			suffix = fmt.Sprintf(" (%d)", i)
		}
		base := []rune(clientName)
		if max := tokenNameMaxLen - utf8.RuneCountInString(suffix); len(base) > max {
			base = base[:max]
		}
		tok.Name = strings.TrimSpace(string(base)) + suffix
		id, err := t.DB.CreateAPIToken(ctx, tok)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return OAuthPair{}, store.APIToken{}, err
		}
		tok.ID = id
		return OAuthPair{Access: access, Refresh: refresh, ExpiresIn: AccessTTL}, tok, nil
	}
	return OAuthPair{}, store.APIToken{}, store.ErrConflict
}

// Refresh troca um refresh por um novo par, derivado do próprio refresh. O
// refresh atual faz a rotação (CAS); o anterior, dentro de RefreshGrace, recebe
// o mesmo par já emitido, sem escrita; fora da janela é reuso e a conexão é
// revogada.
func (t *Tokens) Refresh(ctx context.Context, refresh, clientID string) (OAuthPair, store.APIToken, error) {
	if len(t.Key) == 0 {
		return OAuthPair{}, store.APIToken{}, errors.New("auth: chave de renovação não configurada")
	}
	if !refreshRe.MatchString(refresh) {
		return OAuthPair{}, store.APIToken{}, ErrNoToken
	}
	h := hashToken(refresh)
	access, newRefresh := t.derivePair(refresh)
	// Duas tentativas: quem perde o CAS relê a linha e cai na janela de graça.
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := t.DB.APITokenByRefreshHash(ctx, h)
		if errors.Is(err, store.ErrNotFound) {
			return OAuthPair{}, store.APIToken{}, ErrNoToken
		}
		if err != nil {
			return OAuthPair{}, store.APIToken{}, err
		}
		now := t.now()
		if tok.ClientID != clientID || tok.RevokedAt != nil || (tok.ExpiresAt != nil && !now.Before(*tok.ExpiresAt)) {
			return OAuthPair{}, store.APIToken{}, ErrNoToken
		}
		if tok.RefreshHash != h { // casou com o anterior
			if tok.RotatedAt == nil || now.Sub(*tok.RotatedAt) > RefreshGrace {
				if err := t.DB.RevokeAPITokenByID(ctx, tok.ID, now); err != nil {
					return OAuthPair{}, store.APIToken{}, err
				}
				return OAuthPair{}, store.APIToken{}, ErrRefreshReuse
			}
			// Na janela: só repete o par já emitido, se ainda for o atual.
			if hashToken(newRefresh) != tok.RefreshHash || tok.AccessExpiresAt == nil {
				return OAuthPair{}, store.APIToken{}, ErrNoToken
			}
			return OAuthPair{Access: access, Refresh: newRefresh, ExpiresIn: tok.AccessExpiresAt.Sub(now)}, tok, nil
		}
		accExp := now.Add(AccessTTL)
		if tok.ExpiresAt != nil && tok.ExpiresAt.Before(accExp) {
			accExp = *tok.ExpiresAt
		}
		rot := store.TokenRotation{
			AccessHash: hashToken(access), RefreshHash: hashToken(newRefresh), Prefix: access[:tokenPrefixLen],
			AccessExpiresAt: accExp, At: now,
		}
		err = t.DB.RotateAPIToken(ctx, tok.ID, h, rot)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return OAuthPair{}, store.APIToken{}, err
		}
		tok.TokenHash, tok.RefreshHash, tok.Prefix = rot.AccessHash, rot.RefreshHash, rot.Prefix
		tok.AccessExpiresAt = &accExp
		tok.PrevRefreshHash = h
		tok.RotatedAt = &now
		return OAuthPair{Access: access, Refresh: newRefresh, ExpiresIn: accExp.Sub(now)}, tok, nil
	}
	return OAuthPair{}, store.APIToken{}, ErrNoToken
}

// RevokeByID revoga uma conexão sem conferir o dono.
func (t *Tokens) RevokeByID(ctx context.Context, id int64) error {
	return t.DB.RevokeAPITokenByID(ctx, id, t.now())
}
