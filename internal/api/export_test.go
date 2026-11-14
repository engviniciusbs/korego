package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/go-chi/chi/v5"
)

// CoreForTest devolve um roteador só com o núcleo de rotas autenticadas
// (sem sessão nem CSRF), montado na raiz.
func CoreForTest(d Deps) http.Handler {
	r := chi.NewRouter()
	newRoutes(d).mountCore(r)
	return r
}

// TokenProbeForTest monta RequireToken na frente de probe.
func TokenProbeForTest(tokens *auth.Tokens, db *store.DB, lim *auth.Limiter, probe http.HandlerFunc) http.Handler {
	return RequireToken(tokens, db, lim)(probe)
}

// TokenIdentityForTest devolve a identidade de token que RequireToken pôs em ctx.
func TokenIdentityForTest(ctx context.Context) (tok store.APIToken, u store.User, remoteAddr string, hdr http.Header, ok bool) {
	id, ok := ctx.Value(tokenIdentityKey).(tokenIdentity)
	return id.Token, id.User, id.RemoteAddr, id.Header, ok
}

// TokenTransportOKForTest expõe tokenTransportOK.
func TokenTransportOKForTest(r *http.Request) bool { return tokenTransportOK(r) }

// DispatcherForTest despacha requisições no núcleo (/api) como um token.
type DispatcherForTest struct{ d dispatcher }

// NewDispatcherForTest monta o núcleo de d como o MCP vai usar.
func NewDispatcherForTest(d Deps) DispatcherForTest {
	return DispatcherForTest{d: dispatcher{core: newCore(newRoutes(d))}}
}

// Do chama dispatcher.do com a identidade do token tok do usuário u.
func (t DispatcherForTest) Do(ctx context.Context, tok store.APIToken, u store.User, remoteAddr string, hdr http.Header,
	method, path string, query url.Values, body any, code string) (int, []byte) {
	return t.d.do(ctx, tokenIdentity{Token: tok, User: u, RemoteAddr: remoteAddr, Header: hdr}, method, path, query, body, code)
}
