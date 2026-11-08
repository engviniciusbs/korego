package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

const pushSHA = "0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c"

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func hubSign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// hook envia um webhook sem cookie nem CSRF; sig vazio = assina com o segredo de teste.
func (g *ghFx) hook(t *testing.T, event, delivery string, body []byte, sig string) *httptest.ResponseRecorder {
	t.Helper()
	if sig == "" {
		sig = hubSign(webhookSecret, body)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(body))
	r.RemoteAddr = "140.82.115.1:443"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", delivery)
	r.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	g.h.ServeHTTP(w, r)
	return w
}

func (g *ghFx) githubApp(t *testing.T, slug, repo, branch string, auto bool) *store.App {
	t.Helper()
	a := &store.App{ProjectID: g.project(t), Slug: slug, SourceType: "github", Repo: repo, BuildType: "dockerfile",
		Branch: branch, DockerfilePath: "Dockerfile", ComposePath: "docker-compose.yml",
		InternalPort: 3000, HealthTimeoutS: 60, AutoDeploy: auto}
	require.NoError(t, g.db.InsertApp(context.Background(), a))
	return a
}

func (g *ghFx) webhookDeploys(t *testing.T, appID int64) []store.Deploy {
	t.Helper()
	ds, err := g.db.ListDeploys(context.Background(), appID, 100)
	require.NoError(t, err)
	return ds
}

func TestWebhook_InvalidSignature401(t *testing.T) {
	g := newGHFx(t, true)
	body := readTestdata(t, "push.json")
	// Sem App conectado: 404.
	w := g.hook(t, "push", "d-0", body, "")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "NOT_CONNECTED", errCode(decode(t, w)))

	g.connect(t)
	app := g.githubApp(t, "site", "exemplo/site", "main", true)
	altered := bytes.Replace(body, []byte(pushSHA), []byte(strings.Repeat("f", 40)), 1)
	for _, sig := range []string{
		hubSign("outro-segredo", body),
		hubSign(webhookSecret, body)[7:], // sem prefixo
		"sha256=nao-e-hex",
		hubSign(webhookSecret, altered),
	} {
		w := g.hook(t, "push", "d-1", body, sig)
		require.Equal(t, http.StatusUnauthorized, w.Code, sig)
		assert.Equal(t, "INVALID_SIGNATURE", errCode(decode(t, w)))
	}
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(body))
	r.Header.Set("X-GitHub-Event", "push")
	w = httptest.NewRecorder()
	g.h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "sem cabeçalho de assinatura")
	assert.Empty(t, g.webhookDeploys(t, app.ID))

	// Assinatura recusada não envenena a deduplicação: a mesma entrega válida passa.
	w = g.hook(t, "push", "d-1", body, "")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.EqualValues(t, 1, decode(t, w)["queued"])
}

func TestWebhook_PushEnqueuesMatchingApps(t *testing.T) {
	g := newGHFx(t, true)
	g.connect(t)
	match := g.githubApp(t, "site", "Exemplo/Site", "main", true) // caixa diferente do full_name
	other := g.githubApp(t, "site-dev", "exemplo/site", "dev", true)
	noAuto := g.githubApp(t, "site-manual", "exemplo/site", "main", false)
	foreign := g.githubApp(t, "blog", "exemplo/blog", "main", true)
	deleting := g.githubApp(t, "site-saindo", "exemplo/site", "main", true)
	done := g.svc.BeginDelete(deleting.ID)
	defer done()

	w := g.hook(t, "push", "entrega-1", readTestdata(t, "push.json"), "")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.Equal(t, map[string]any{"queued": float64(1)}, decode(t, w))

	ds := g.webhookDeploys(t, match.ID)
	require.Len(t, ds, 1)
	assert.Equal(t, "webhook", ds[0].Trigger)
	assert.Equal(t, pushSHA, ds[0].CommitSHA)
	assert.Equal(t, "Atualiza a página inicial", ds[0].CommitMessage)
	for _, a := range []*store.App{other, noAuto, foreign, deleting} {
		assert.Empty(t, g.webhookDeploys(t, a.ID), a.Slug)
	}

	items := auditItems(t, g.env, g.cookie, "deploy.webhook")
	require.Len(t, items, 1)
	assert.Nil(t, items[0]["userId"])
	assert.Equal(t, "site", items[0]["target"])
	assert.Equal(t, map[string]any{"repo": "exemplo/site", "branch": "main", "sha": pushSHA, "delivery": "entrega-1"},
		items[0]["metadata"])

	// Tag, branch apagada, ping e outros eventos: 204 sem deploy.
	tag := bytes.Replace(readTestdata(t, "push.json"), []byte(`"refs/heads/main"`), []byte(`"refs/tags/v1.0.0"`), 1)
	assert.Equal(t, http.StatusNoContent, g.hook(t, "push", "entrega-2", tag, "").Code)
	del := []byte(`{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","deleted":true,"head_commit":null,"repository":{"full_name":"exemplo/site"}}`)
	assert.Equal(t, http.StatusNoContent, g.hook(t, "push", "entrega-3", del, "").Code)
	assert.Equal(t, http.StatusNoContent, g.hook(t, "ping", "entrega-4", readTestdata(t, "ping.json"), "").Code)
	assert.Equal(t, http.StatusNoContent, g.hook(t, "issues", "entrega-5", []byte(`{}`), "").Code)
	w = g.hook(t, "push", "entrega-6", []byte(`não é json`), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Len(t, g.webhookDeploys(t, match.ID), 1)
}

func TestWebhook_DuplicateDelivery(t *testing.T) {
	g := newGHFx(t, true)
	g.connect(t)
	app := g.githubApp(t, "site", "exemplo/site", "main", true)
	body := readTestdata(t, "push.json")

	w := g.hook(t, "push", "mesma-entrega", body, "")
	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, map[string]any{"queued": float64(1)}, decode(t, w))
	w = g.hook(t, "push", "mesma-entrega", body, "")
	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, map[string]any{"queued": float64(0), "duplicate": true}, decode(t, w))
	assert.Len(t, g.webhookDeploys(t, app.ID), 1)

	// Outra entrega do mesmo push gera outro deploy.
	w = g.hook(t, "push", "outra-entrega", body, "")
	assert.Equal(t, map[string]any{"queued": float64(1)}, decode(t, w))
}

func TestWebhook_NoSessionNoCSRFNeeded(t *testing.T) {
	g := newGHFx(t, true)
	g.connect(t)
	g.githubApp(t, "site", "exemplo/site", "main", true)
	// hook não manda cookie nem X-CSRF-Token.
	w := g.hook(t, "push", "sem-sessao", readTestdata(t, "push.json"), "")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	// Corpo acima de 10 MiB: 413.
	big := bytes.Repeat([]byte("a"), 10<<20+1)
	w = g.hook(t, "push", "grande", big, "")
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}
