package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

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
func TokenProbeForTest(tokens *auth.Tokens, db *store.DB, lim *auth.Limiter, origin func(context.Context) (string, bool), probe http.HandlerFunc) http.Handler {
	return RequireToken(tokens, db, lim, origin)(probe)
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

// TokenIdentityTransportForTest devolve o Host e o TLS guardados na identidade.
func TokenIdentityTransportForTest(ctx context.Context) (host string, tls, ok bool) {
	id, ok := ctx.Value(tokenIdentityKey).(tokenIdentity)
	return id.Host, id.TLS, ok
}

// DoFromContext despacha com a identidade que RequireToken pôs em ctx.
func (t DispatcherForTest) DoFromContext(ctx context.Context, method, path string, body any, code string) (int, []byte) {
	id, _ := ctx.Value(tokenIdentityKey).(tokenIdentity)
	return t.d.do(ctx, id, method, path, nil, body, code)
}

// SetDeployWaitSleepForTest troca a espera do deploy_wait; restore volta a original.
func SetDeployWaitSleepForTest(f func(ctx context.Context, d time.Duration) error) (restore func()) {
	old := deployWaitSleep
	deployWaitSleep = f
	return func() { deployWaitSleep = old }
}

// SleepCtxForTest expõe a espera real do deploy_wait.
func SleepCtxForTest(ctx context.Context, d time.Duration) error { return sleepCtx(ctx, d) }
