package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

var (
	// ErrNotConnected indica que o GitHub App ainda não foi conectado.
	ErrNotConnected = errors.New("GitHub não conectado")
	// ErrRepoNotInstalled indica que nenhuma instalação do App vê o repositório.
	ErrRepoNotInstalled = errors.New("repositório não está instalado no GitHub App")
)

const (
	repoCacheTTL = 10 * time.Minute
	tokenSkew    = 5 * time.Minute
)

// RepoView é um repositório visível, com a instalação que o enxerga.
type RepoView struct {
	FullName       string
	DefaultBranch  string
	InstallationID int64
}

type cachedToken struct {
	token   string
	expires time.Time
}

type cachedRepo struct {
	installation int64
	until        time.Time
}

// Service reúne o GitHub App salvo, o cliente e os caches. Implementa
// deploy.TokenSource (CloneToken).
type Service struct {
	DB     *store.DB
	Box    *secrets.Box
	Client *Client
	Now    func() time.Time

	mu     sync.Mutex
	tokens map[int64]cachedToken
	repos  map[string]cachedRepo
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Connected devolve o App salvo; ok=false quando não conectado.
func (s *Service) Connected(ctx context.Context) (store.GitHubApp, bool, error) {
	a, err := s.DB.GetGitHubApp(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return store.GitHubApp{}, false, nil
	}
	if err != nil {
		return store.GitHubApp{}, false, err
	}
	return a, true, nil
}

func (s *Service) key(app store.GitHubApp) (*rsa.PrivateKey, error) {
	pemBytes, err := s.Box.Decrypt(app.PrivateKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("github: decifrar chave privada: %w", err)
	}
	return ParsePrivateKey(pemBytes)
}

func (s *Service) jwt(ctx context.Context) (string, error) {
	app, ok, err := s.Connected(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotConnected
	}
	k, err := s.key(app)
	if err != nil {
		return "", err
	}
	return AppJWT(app.AppID, k, s.now())
}

// installationToken devolve o token em cache ou cria um novo. Chamar com s.mu preso.
func (s *Service) installationToken(ctx context.Context, jwt string, id int64) (string, error) {
	if t, ok := s.tokens[id]; ok && s.now().Before(t.expires.Add(-tokenSkew)) {
		return t.token, nil
	}
	tok, exp, err := s.Client.InstallationToken(ctx, jwt, id)
	if err != nil {
		return "", err
	}
	if s.tokens == nil {
		s.tokens = map[int64]cachedToken{}
	}
	s.tokens[id] = cachedToken{token: tok, expires: exp}
	return tok, nil
}

// Repos lista os repositórios de todas as instalações, sem duplicatas.
// Instalação com erro (ex.: suspensa) é ignorada; só falha se todas falharem.
func (s *Service) Repos(ctx context.Context) ([]RepoView, error) {
	jwt, err := s.jwt(ctx)
	if err != nil {
		return nil, err
	}
	ins, err := s.Client.Installations(ctx, jwt)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := []RepoView{}
	var lastErr error
	failed := 0
	for _, in := range ins {
		repos, err := s.installationRepos(ctx, jwt, in.ID)
		if err != nil {
			lastErr = err
			failed++
			continue
		}
		for _, r := range repos {
			key := strings.ToLower(r.FullName)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, RepoView{FullName: r.FullName, DefaultBranch: r.DefaultBranch, InstallationID: in.ID})
		}
	}
	if failed > 0 && failed == len(ins) {
		return nil, lastErr
	}
	return out, nil
}

// installationRepos lista os repositórios de uma instalação. Chamar com s.mu preso.
func (s *Service) installationRepos(ctx context.Context, jwt string, id int64) ([]Repo, error) {
	tok, err := s.installationToken(ctx, jwt, id)
	if err != nil {
		return nil, err
	}
	return s.Client.Repos(ctx, tok)
}

// CloneToken devolve um token de instalação que enxerga repo ("dono/nome").
func (s *Service) CloneToken(ctx context.Context, repo string) (string, error) {
	jwt, err := s.jwt(ctx)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	cacheKey := strings.ToLower(repo)
	if c, ok := s.repos[cacheKey]; ok && now.Before(c.until) {
		if tok, err := s.installationToken(ctx, jwt, c.installation); err == nil {
			return tok, nil
		}
		delete(s.repos, cacheKey)
	}
	ins, err := s.Client.Installations(ctx, jwt)
	if err != nil {
		return "", err
	}
	var lastErr error
	for _, in := range ins {
		repos, err := s.installationRepos(ctx, jwt, in.ID)
		if err != nil {
			lastErr = err
			continue
		}
		for _, r := range repos {
			if strings.EqualFold(r.FullName, repo) {
				if s.repos == nil {
					s.repos = map[string]cachedRepo{}
				}
				s.repos[cacheKey] = cachedRepo{installation: in.ID, until: now.Add(repoCacheTTL)}
				return s.tokens[in.ID].token, nil
			}
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrRepoNotInstalled
}
