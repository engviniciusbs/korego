package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
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
	for _, in := range ins {
		tok, err := s.installationToken(ctx, jwt, in.ID)
		if err != nil {
			return nil, err
		}
		repos, err := s.Client.Repos(ctx, tok)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if seen[r.FullName] {
				continue
			}
			seen[r.FullName] = true
			out = append(out, RepoView{FullName: r.FullName, DefaultBranch: r.DefaultBranch, InstallationID: in.ID})
		}
	}
	return out, nil
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
	if c, ok := s.repos[repo]; ok && now.Before(c.until) {
		return s.installationToken(ctx, jwt, c.installation)
	}
	ins, err := s.Client.Installations(ctx, jwt)
	if err != nil {
		return "", err
	}
	for _, in := range ins {
		tok, err := s.installationToken(ctx, jwt, in.ID)
		if err != nil {
			return "", err
		}
		repos, err := s.Client.Repos(ctx, tok)
		if err != nil {
			return "", err
		}
		for _, r := range repos {
			if r.FullName == repo {
				if s.repos == nil {
					s.repos = map[string]cachedRepo{}
				}
				s.repos[repo] = cachedRepo{installation: in.ID, until: now.Add(repoCacheTTL)}
				return tok, nil
			}
		}
	}
	return "", ErrRepoNotInstalled
}
