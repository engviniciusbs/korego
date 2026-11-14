package api

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/oauth"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	pendingTTL      = 10 * time.Minute
	maxPending      = 256
	authCodeTTL     = 60 * time.Second
	sessionFreshFor = 2 * time.Minute

	msgAuthzInvalid = "Pedido de autorização inválido. Inicie a conexão de novo pelo agente."
	msgAuthzExpired = "Pedido de autorização expirado ou inexistente. Inicie a conexão de novo pelo agente."
)

// pendingAuth é um pedido de autorização válido, à espera do consentimento.
// O resource não é guardado: depois de validado, é sempre o canônico.
type pendingAuth struct {
	ID, ClientID, ClientName, RedirectURI, State, Challenge string
	CreatedAt                                               time.Time
}

// pendingStore guarda os pedidos pendentes em memória (somem ao reiniciar).
type pendingStore struct {
	mu sync.Mutex
	m  map[string]pendingAuth
}

func newPendingStore() *pendingStore { return &pendingStore{m: map[string]pendingAuth{}} }

// pruneLocked remove os vencidos.
func (p *pendingStore) pruneLocked(now time.Time) {
	for id, a := range p.m {
		if now.Sub(a.CreatedAt) >= pendingTTL {
			delete(p.m, id)
		}
	}
}

// add guarda o pedido; no teto de 256, o mais antigo sai.
func (p *pendingStore) add(a pendingAuth, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	for len(p.m) >= maxPending {
		var oldest string
		var at time.Time
		for id, x := range p.m {
			if oldest == "" || x.CreatedAt.Before(at) {
				oldest, at = id, x.CreatedAt
			}
		}
		delete(p.m, oldest)
	}
	p.m[a.ID] = a
}

// get devolve o pedido, se existir e não tiver vencido.
func (p *pendingStore) get(id string, now time.Time) (pendingAuth, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	a, ok := p.m[id]
	return a, ok
}

// remove tira o pedido e diz se ele ainda estava lá: só quem remove aprova.
func (p *pendingStore) remove(id string, now time.Time) (pendingAuth, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	a, ok := p.m[id]
	delete(p.m, id)
	return a, ok
}

// authCode é um código de autorização emitido (guardado pelo hash).
type authCode struct {
	ClientID, ClientName, RedirectURI, Challenge string
	UserID                                       int64
	TTL                                          time.Duration // validade da conexão (30 ou 90 dias)
	ExpiresAt                                    time.Time
	Used                                         bool
	TokenID                                      int64 // conexão gerada, para revogar no reuso
	// Reused marca um segundo uso visto enquanto a conexão do primeiro ainda
	// estava sendo emitida (Used e TokenID == 0): setToken o reporta a quem emite.
	Reused bool
}

// codeStore guarda os códigos até vencerem, inclusive os já usados, para a
// Task 7 detectar o reuso (RFC 6749, seção 4.1.2).
type codeStore struct {
	mu sync.Mutex
	m  map[string]*authCode
}

func newCodeStore() *codeStore { return &codeStore{m: map[string]*authCode{}} }

func (c *codeStore) pruneLocked(now time.Time) {
	for h, a := range c.m {
		if !now.Before(a.ExpiresAt) {
			delete(c.m, h)
		}
	}
}

// put guarda o código pelo hash.
func (c *codeStore) put(hash string, a authCode, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	c.m[hash] = &a
}

// take consome o código: marca Used e devolve uma cópia do estado anterior.
// Um código já usado volta com Used == true; vencido ou desconhecido, nil.
func (c *codeStore) take(hash string, now time.Time) (*authCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	a, ok := c.m[hash]
	if !ok {
		return nil, false
	}
	cp := *a
	if a.Used && a.TokenID == 0 {
		a.Reused = true
	}
	a.Used = true
	return &cp, true
}

// setToken liga a conexão gerada ao código, para a revogação no reuso. Devolve
// true se o código foi reapresentado antes de a conexão ser registrada: quem
// emitiu deve então revogá-la e recusar.
func (c *codeStore) setToken(hash string, tokenID int64) (reused bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a, ok := c.m[hash]; ok {
		a.TokenID = tokenID
		return a.Reused
	}
	return false
}

var authzErrorPage = template.Must(template.New("erro").Parse(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><title>KoreGO</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1>KoreGO</h1><p>{{.}}</p></body></html>`))

// writeAuthzPage escreve a página de erro, sem redirecionar.
func writeAuthzPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = authzErrorPage.Execute(w, msg)
}

// callbackURL monta o retorno ao agente; o redirect_uri registrado nunca tem
// query, então url.Values basta.
func callbackURL(redirectURI string, params map[string]string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// authorize trata GET /oauth/authorize.
func (s *oauthServer) authorize(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.authzLim.Attempt("oauth-authz:"+oauth.LimitKey(auth.ClientIP(r)), now) {
		writeAuthzPage(w, http.StatusTooManyRequests, "Muitas tentativas. Aguarde um pouco e tente de novo.")
		return
	}
	q := r.URL.Query()
	// Cliente e retorno primeiro: se falharem, nunca se redireciona.
	client, err := s.d.Auth.DB.OAuthClientByID(r.Context(), q.Get("client_id"))
	if err != nil {
		if err != store.ErrNotFound {
			log.Printf("oauth: authorize: %v", err)
		}
		writeAuthzPage(w, http.StatusBadRequest, msgAuthzInvalid)
		return
	}
	redirect := q.Get("redirect_uri")
	matched := false
	for _, reg := range client.RedirectURIs {
		if oauth.RedirectMatches(reg, redirect) {
			matched = true
			break
		}
	}
	if !matched {
		writeAuthzPage(w, http.StatusBadRequest, msgAuthzInvalid)
		return
	}
	origin := originFrom(r.Context())
	state := q.Get("state")
	fail := func(code, desc string) {
		loc, err := callbackURL(redirect, map[string]string{
			"error": code, "error_description": desc, "state": state, "iss": origin})
		if err != nil {
			writeAuthzPage(w, http.StatusBadRequest, msgAuthzInvalid)
			return
		}
		http.Redirect(w, r, loc, http.StatusFound)
	}
	switch {
	case q.Get("response_type") != "code":
		fail("unsupported_response_type", "Só response_type=code é aceito.")
		return
	case q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256":
		fail("invalid_request", "É preciso code_challenge com code_challenge_method=S256.")
		return
	case state == "":
		fail("invalid_request", "O parâmetro state é obrigatório.")
		return
	}
	if res := q.Get("resource"); res != "" {
		if n, ok := oauth.NormalizeResource(res); !ok || n != origin+"/mcp" {
			fail("invalid_target", "resource não corresponde a este servidor.")
			return
		}
	}
	id, err := oauth.RandomToken()
	if err != nil {
		writeAuthzPage(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	s.pending.add(pendingAuth{ID: id, ClientID: client.ID, ClientName: client.Name,
		RedirectURI: redirect, State: state, Challenge: q.Get("code_challenge"), CreatedAt: now}, now)
	http.Redirect(w, r, "/autorizar/?req="+id, http.StatusFound)
}

// mountSession monta as rotas que exigem sessão (pedidos e aprovação).
// Chamado de dentro de mountSessionOnly, nunca de mountCore: um token de API
// não pode aprovar conexões novas.
func (s *oauthServer) mountSession(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.requireOrigin)
		r.Get("/oauth/requests/{id}", s.requestInfo)
		r.Post("/oauth/requests/{id}/approve", s.approve)
		r.Post("/oauth/requests/{id}/deny", s.deny)
	})
}

func notFoundRequest(w http.ResponseWriter) {
	WriteError(w, http.StatusNotFound, "OAUTH_REQUEST_NOT_FOUND", msgAuthzExpired)
}

// requestInfo serve GET /api/oauth/requests/{id}.
func (s *oauthServer) requestInfo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pending.get(chi.URLParam(r, "id"), s.now())
	if !ok {
		notFoundRequest(w)
		return
	}
	client, err := s.d.Auth.DB.OAuthClientByID(r.Context(), p.ClientID)
	if err != nil {
		if err == store.ErrNotFound {
			notFoundRequest(w)
			return
		}
		internalError(w, err)
		return
	}
	// A sessão é comparada pelo relógio do serviço de autenticação, o mesmo
	// que a criou.
	fresh := false
	if sess, ok := CurrentSession(r.Context()); ok {
		fresh = s.authNow().Sub(sess.CreatedAt) < sessionFreshFor
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"clientName":   p.ClientName,
		"destination":  oauth.DescribeRedirect(p.RedirectURI),
		"registeredAt": client.CreatedAt,
		"sessionFresh": fresh,
	})
}

func (s *oauthServer) authNow() time.Time {
	if s.d.Auth.Now != nil {
		return s.d.Auth.Now()
	}
	return time.Now()
}

// approve serve POST /api/oauth/requests/{id}/approve: sessão, CSRF e 2FA.
func (s *oauthServer) approve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, ok := CurrentSession(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Não autenticado.")
		return
	}
	if _, ok := s.pending.get(id, s.now()); !ok {
		notFoundRequest(w)
		return
	}
	var in struct {
		Code    string `json:"code"`
		TTLDays *int   `json:"ttlDays"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	days := 90
	if in.TTLDays != nil {
		days = *in.TTLDays
	}
	if days != 30 && days != 90 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "ttlDays: a validade deve ser 30 ou 90 dias.")
		return
	}
	rec := func(meta map[string]any) { record(s.d.Auth, r, "token.oauth_authorized", id, meta) }
	if !verifyCode(s.d.Auth, w, r, sess.UserID, auth.ClientIP(r), in.Code, rec) {
		return
	}
	// Só quem remove o pedido aprova: duas aprovações simultâneas não passam.
	now := s.now()
	p, ok := s.pending.remove(id, now)
	if !ok {
		notFoundRequest(w)
		return
	}
	if err := s.d.Auth.DB.MarkOAuthClientAuthorized(r.Context(), p.ClientID, now); err != nil {
		internalError(w, err)
		return
	}
	code, err := oauth.RandomToken()
	if err != nil {
		internalError(w, err)
		return
	}
	s.codes.put(oauth.Hash(code), authCode{
		ClientID: p.ClientID, ClientName: p.ClientName, RedirectURI: p.RedirectURI, Challenge: p.Challenge,
		UserID: sess.UserID, TTL: time.Duration(days) * 24 * time.Hour, ExpiresAt: now.Add(authCodeTTL),
	}, now)
	loc, err := callbackURL(p.RedirectURI, map[string]string{
		"code": code, "state": p.State, "iss": originFrom(r.Context())})
	if err != nil {
		internalError(w, err)
		return
	}
	record(s.d.Auth, r, "token.oauth_authorized", p.ClientID, map[string]any{
		"client": p.ClientName, "clientId": p.ClientID,
		"destination": oauth.DescribeRedirect(p.RedirectURI), "ttlDays": days})
	WriteJSON(w, http.StatusOK, map[string]string{"redirect": loc})
}

// deny serve POST /api/oauth/requests/{id}/deny.
func (s *oauthServer) deny(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pending.remove(chi.URLParam(r, "id"), s.now())
	if !ok {
		notFoundRequest(w)
		return
	}
	loc, err := callbackURL(p.RedirectURI, map[string]string{
		"error": "access_denied", "state": p.State, "iss": originFrom(r.Context())})
	if err != nil {
		internalError(w, err)
		return
	}
	record(s.d.Auth, r, "token.oauth_denied", p.ClientID, map[string]any{
		"client": p.ClientName, "clientId": p.ClientID,
		"destination": oauth.DescribeRedirect(p.RedirectURI)})
	WriteJSON(w, http.StatusOK, map[string]string{"redirect": loc})
}
