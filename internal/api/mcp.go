package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/jsonschema-go/jsonschema"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpInstructions vai ao agente no initialize.
const mcpInstructions = `KoreGO é o painel deste servidor: apps em Docker, deploys a partir do git, domínios com HTTPS automático.

Para publicar um site que está no GitHub:
1. github_repos acha o repositório. Conectar o GitHub App se faz no painel (Configurações → GitHub); não há ferramenta para isso. Fora do GitHub, use sourceType "git" com a URL https do repositório.
2. project_list ou project_create, depois app_create (sourceType, repo, branch, buildType, internalPort).
3. app_env_set grava as variáveis. Os valores são só de escrita: app_env_names devolve apenas os nomes.
4. deploy_start e deploy_wait (até 120 s por chamada; repita enquanto timedOut for true). Se falhar, leia deploy_log, corrija o código, faça push e chame deploy_start de novo.
5. domain_create aponta o domínio para a app; domain_dns confere o DNS.
6. proxy_status mostra o certificado de cada domínio: é assim que se confirma que o HTTPS respondeu.

O DNS do domínio precisa apontar para este servidor antes de o certificado automático (tls "auto") ser emitido. Se domain_dns não der ok, peça ao usuário para ajustar o DNS e confira de novo.

Ações irreversíveis exigem code (2FA): apagar app, projeto, domínio, certificado ou container; assumir ou devolver o proxy; aplicar atualização; mudar o domínio ou a porta pública do painel; desconectar o GitHub. Chame sem code, receba CODE_REQUIRED, peça ao usuário o código de 6 dígitos do autenticador e chame de novo com code. Nunca invente nem reutilize um código: cada um vale uma vez.

Erros chegam como "<CÓDIGO> (HTTP n): mensagem".`

const (
	hintCodeRequired = "Peça ao usuário o código de 6 dígitos do aplicativo autenticador e chame de novo com `code`. Nunca invente nem reutilize um código."
	hintInvalidCode  = "Cada código vale uma vez; se o usuário acabou de usar este, peça o próximo."
)

// tokenIdentityFrom lê a identidade que RequireToken pôs em ctx.
func tokenIdentityFrom(ctx context.Context) (tokenIdentity, bool) {
	id, ok := ctx.Value(tokenIdentityKey).(tokenIdentity)
	return id, ok
}

// newMCPHandler serve o MCP (Streamable HTTP, sem estado). Vai atrás de
// RequireToken: o verificador do SDK só repassa a identidade já validada às
// ferramentas, por req.Extra.TokenInfo.
func newMCPHandler(d dispatcher, version string) http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "korego", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	registerMCPTools(s, d)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// O Caddy local chega por loopback com o Host do painel; a proteção
		// contra DNS rebinding do SDK recusaria. /mcp só aceita Bearer (nunca
		// cookie), então um navegador redirecionado não tem como se autenticar.
		DisableLocalhostProtection:   true,
		PropagateRequestCancellation: true,
	})
	return sdkauth.RequireBearerToken(verifyTokenIdentity,
		&sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
}

// verifyTokenIdentity não confere o token de novo: devolve a identidade que
// RequireToken validou nesta mesma requisição.
func verifyTokenIdentity(_ context.Context, _ string, r *http.Request) (*sdkauth.TokenInfo, error) {
	id, ok := tokenIdentityFrom(r.Context())
	if !ok {
		return nil, sdkauth.ErrInvalidToken
	}
	ti := &sdkauth.TokenInfo{UserID: strconv.FormatInt(id.User.ID, 10), Extra: map[string]any{"identity": id}}
	if id.Token.ExpiresAt != nil {
		ti.Expiration = *id.Token.ExpiresAt
	}
	return ti, nil
}

// identityOf lê a identidade do token que fez a chamada da ferramenta.
func identityOf(req *mcp.CallToolRequest) (tokenIdentity, bool) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return tokenIdentity{}, false
	}
	id, ok := req.Extra.TokenInfo.Extra["identity"].(tokenIdentity)
	return id, ok
}

// toolResult converte a resposta do núcleo no resultado da ferramenta: 2xx vira
// o JSON da resposta; o resto, "<CODE> (HTTP n): <mensagem>" com IsError.
func toolResult(status int, body []byte) *mcp.CallToolResult {
	body = bytes.TrimSpace(body)
	if status >= 200 && status < 300 {
		if len(body) == 0 {
			body = []byte(`{"ok":true}`)
		}
		return jsonResult(body)
	}
	var e fieldErrorBody
	code, msg := "HTTP_ERROR", http.StatusText(status)
	if json.Unmarshal(body, &e) == nil && e.Error.Code != "" {
		code, msg = e.Error.Code, e.Error.Message
	} else if len(body) > 0 {
		msg = string(body)
	}
	text := fmt.Sprintf("%s (HTTP %d): %s", code, status, msg)
	if e.Error.Field != "" {
		text += " Campo: " + e.Error.Field + "."
	}
	switch code {
	case "CODE_REQUIRED":
		text += "\n" + hintCodeRequired
	case "INVALID_CODE":
		text += "\n" + hintInvalidCode
	}
	return errorResult(text)
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// jsonResult põe o JSON no texto e, quando é objeto, também no conteúdo estruturado.
func jsonResult(b []byte) *mcp.CallToolResult {
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
	if len(b) > 0 && b[0] == '{' && json.Valid(b) {
		res.StructuredContent = json.RawMessage(b)
	}
	return res
}

// valueResult serializa v como resultado de sucesso.
func valueResult(v any) *mcp.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return errorResult("INTERNAL_ERROR (HTTP 500): " + err.Error())
	}
	return jsonResult(b)
}

func noIdentityResult() *mcp.CallToolResult {
	return errorResult("INVALID_TOKEN (HTTP 401): Token de API inválido, expirado ou revogado.")
}

// routeExists diz se o núcleo atende method+path ({id} vale qualquer segmento).
func routeExists(core http.Handler, method, path string) bool {
	rs, ok := core.(chi.Routes)
	if !ok {
		return true
	}
	return rs.Match(chi.NewRouteContext(), method, strings.ReplaceAll(path, "{id}", "1"))
}

// inputSchema infere o schema de In. Dentro de "body" nada é obrigatório:
// quem valida é o handler REST, como no painel (inclusive PUT parcial).
func inputSchema[In any]() *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp: schema de %T: %v", *new(In), err))
	}
	if b := s.Properties["body"]; b != nil {
		dropRequired(b)
	}
	return s
}

func dropRequired(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.Required = nil
	for _, p := range s.Properties {
		dropRequired(p)
	}
	dropRequired(s.Items)
	dropRequired(s.AdditionalProperties)
}

// deployWaitSleep espera d ou o fim de ctx (trocado nos testes).
var deployWaitSleep = sleepCtx

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
