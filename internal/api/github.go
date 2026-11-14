package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/github"
	"github.com/engviniciusbs/korego/internal/store"
)

const githubSettingsPath = "/configuracoes/github/"

var orgRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)

type githubHandlers struct {
	svc      *auth.Service
	gh       *github.Service
	dom      *domains.Service // nil = painel nunca "pronto"
	states   *stateStore
	hostname func() (string, error)
	// connectMu serializa "já conectado?" + gravação no callback.
	connectMu *sync.Mutex
}

// panelURL devolve https://<domínio> quando o painel tem domínio com certificado ativo.
func (h githubHandlers) panelURL(ctx context.Context) (string, bool, error) {
	return panelOrigin(ctx, h.dom)
}

// status serves GET /api/github.
func (h githubHandlers) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, ready, err := h.panelURL(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	app, ok, err := h.gh.Connected(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	type insDTO struct {
		ID      int64  `json:"id"`
		Account string `json:"account"`
	}
	out := map[string]any{"connected": ok, "app": nil, "installations": []insDTO{}, "panelReady": ready}
	if !ok {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	out["app"] = map[string]string{
		"slug": app.Slug, "owner": app.Owner, "htmlUrl": app.HTMLURL,
		"installUrl": h.gh.Client.WebBaseURL() + "/apps/" + url.PathEscape(app.Slug) + "/installations/new",
	}
	ins, err := h.gh.Installations(ctx)
	if err != nil {
		log.Printf("github: listar instalações: %v", err)
		out["installationsError"] = "Não foi possível listar as instalações do GitHub App."
	} else {
		list := make([]insDTO, 0, len(ins))
		for _, i := range ins {
			list = append(list, insDTO{ID: i.ID, Account: i.Account})
		}
		out["installations"] = list
	}
	WriteJSON(w, http.StatusOK, out)
}

// manifest serves POST /api/github/manifest com {"org": ""}.
func (h githubHandlers) manifest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Org string `json:"org"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Org != "" && !orgRe.MatchString(in.Org) {
		writeFieldError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"Organização inválida. Use letras, números e hífens (até 39 caracteres).", "org")
		return
	}
	sess, ok := CurrentSession(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	ctx := r.Context()
	if _, connected, err := h.gh.Connected(ctx); err != nil {
		internalError(w, err)
		return
	} else if connected {
		WriteError(w, http.StatusConflict, "ALREADY_CONNECTED", "Já há um GitHub App conectado. Desconecte-o antes de criar outro.")
		return
	}
	panel, ready, err := h.panelURL(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	if !ready {
		WriteError(w, http.StatusConflict, "PANEL_NOT_HTTPS",
			"Configure o domínio do painel com HTTPS ativo antes de conectar o GitHub.")
		return
	}
	host, err := h.hostname()
	if err != nil || host == "" {
		host = "korego"
	}
	state, err := h.states.issue(sess.UserID)
	if err != nil {
		internalError(w, err)
		return
	}
	action := h.gh.Client.WebBaseURL() + "/settings/apps/new?state=" + state
	if in.Org != "" {
		action = h.gh.Client.WebBaseURL() + "/organizations/" + in.Org + "/settings/apps/new?state=" + state
	}
	WriteJSON(w, http.StatusOK, map[string]string{"action": action, "manifest": string(github.Manifest(panel, host))})
}

func callbackRedirect(w http.ResponseWriter, r *http.Request, query string) {
	http.Redirect(w, r, githubSettingsPath+"?"+query, http.StatusFound)
}

// callback serves GET /api/github/callback?code&state. É pública (D7): o
// state de uso único autentica o retorno do github.com, que chega sem o
// cookie SameSite=Strict. Nem o code nem o PEM vão para logs.
func (h githubHandlers) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	userID, ok := h.states.take(q.Get("state"))
	if !ok {
		callbackRedirect(w, r, "erro=STATE_INVALID")
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	h.connectMu.Lock()
	defer h.connectMu.Unlock()
	if _, connected, err := h.gh.Connected(ctx); err != nil {
		log.Printf("github: callback: %v", err)
		callbackRedirect(w, r, "erro=CONVERSION_FAILED")
		return
	} else if connected {
		callbackRedirect(w, r, "erro=ALREADY_CONNECTED")
		return
	}
	conv, err := h.gh.Client.ConvertManifest(ctx, q.Get("code"))
	if err == nil && (conv.ID <= 0 || conv.Slug == "" || conv.PEM == "" || conv.WebhookSecret == "") {
		err = errors.New("github: conversão incompleta")
	}
	if err != nil {
		log.Printf("github: converter manifesto: %v", err)
		h.audit(ctx, r, &userID, "github.connect", "", map[string]any{"ok": false})
		callbackRedirect(w, r, "erro=CONVERSION_FAILED")
		return
	}
	pemEnc, err := h.gh.Box.Encrypt([]byte(conv.PEM))
	if err != nil {
		log.Printf("github: cifrar chave privada: %v", err)
		callbackRedirect(w, r, "erro=CONVERSION_FAILED")
		return
	}
	secretEnc, err := h.gh.Box.Encrypt([]byte(conv.WebhookSecret))
	if err != nil {
		log.Printf("github: cifrar segredo do webhook: %v", err)
		callbackRedirect(w, r, "erro=CONVERSION_FAILED")
		return
	}
	app := &store.GitHubApp{AppID: conv.ID, Slug: conv.Slug, Owner: conv.Owner, HTMLURL: conv.HTMLURL,
		PrivateKeyEnc: pemEnc, WebhookSecretEnc: secretEnc, CreatedAt: h.states.now()}
	if err := h.gh.DB.SaveGitHubApp(ctx, app); err != nil {
		log.Printf("github: gravar App: %v", err)
		callbackRedirect(w, r, "erro=CONVERSION_FAILED")
		return
	}
	h.gh.Forget()
	h.audit(ctx, r, &userID, "github.connect", conv.Slug,
		map[string]any{"ok": true, "appId": conv.ID, "owner": conv.Owner})
	callbackRedirect(w, r, "conectado=1")
}

func (h githubHandlers) audit(ctx context.Context, r *http.Request, userID *int64, action, target string, meta map[string]any) {
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditTimeout)
	defer cancel()
	h.svc.Record(actx, audit.Entry{UserID: userID, Action: action, Target: target, IP: auth.ClientIP(r), Metadata: meta})
}

// repos serves GET /api/github/repos.
func (h githubHandlers) repos(w http.ResponseWriter, r *http.Request) {
	rs, err := h.gh.Repos(r.Context())
	if errors.Is(err, github.ErrNotConnected) {
		WriteError(w, http.StatusConflict, "NO_GITHUB", "Conecte o GitHub antes de listar os repositórios.")
		return
	}
	if err != nil {
		log.Printf("github: listar repositórios: %v", err)
		WriteError(w, http.StatusBadGateway, "GITHUB_UNAVAILABLE", "Não foi possível listar os repositórios no GitHub.")
		return
	}
	type repoDTO struct {
		FullName       string `json:"fullName"`
		DefaultBranch  string `json:"defaultBranch"`
		InstallationID int64  `json:"installationId"`
	}
	out := make([]repoDTO, 0, len(rs))
	for _, x := range rs {
		out = append(out, repoDTO{FullName: x.FullName, DefaultBranch: x.DefaultBranch, InstallationID: x.InstallationID})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"repos": out})
}

// disconnect serves DELETE /api/github: apaga só as credenciais locais; o
// App continua no GitHub e deve ser removido lá (manageUrl).
func (h githubHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	h.connectMu.Lock()
	defer h.connectMu.Unlock()
	app, ok, err := h.gh.Connected(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "NOT_CONNECTED", "O GitHub não está conectado.")
		return
	}
	if err := h.gh.DB.DeleteGitHubApp(ctx); err != nil {
		internalError(w, err)
		return
	}
	h.gh.Forget()
	record(h.svc, r, "github.disconnect", app.Slug, map[string]any{"appId": app.AppID})
	WriteJSON(w, http.StatusOK, map[string]any{
		"removed":   true,
		"manageUrl": app.HTMLURL,
		"message":   "As credenciais foram apagadas do KoreGO. Remova também o App no GitHub.",
	})
}

func defaultHostname() (string, error) { return os.Hostname() }
