package domains

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidHostname(t *testing.T) {
	for _, s := range []string{"exemplo.com", "app.exemplo.com", "a-b.exemplo.com.br", "app.korego.localhost"} {
		assert.True(t, ValidHostname(s), s)
	}
	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62) // 254
	assert.Len(t, long, 254)
	for _, s := range []string{"Exemplo.com", "exemplo", "-a.com", "a..com", long, "a_b.com", "", "a-.com", "203.0.113.10", "exemplo.com."} {
		assert.False(t, ValidHostname(s), s)
	}
	ok253 := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)
	assert.True(t, ValidHostname(ok253))
}

func TestValidAddress(t *testing.T) {
	for _, s := range []string{"127.0.0.1:5000", "localhost:3000", "[::1]:80", "api.interna:8080", "localhost:65535", "exemplo.com:1"} {
		assert.True(t, ValidAddress(s), s)
	}
	for _, s := range []string{"127.0.0.1", ":80", "x:0", "x:70000", "a b:1", "localhost:+80", "localhost:", "localhost:0", "localhost:70000", "Exemplo.com:80", "http://x:80"} {
		assert.False(t, ValidAddress(s), s)
	}
}

// codeField devolve o código e o campo de um *ErrInvalid.
func codeField(t *testing.T, err error) (string, string) {
	t.Helper()
	var inv *ErrInvalid
	require.True(t, errors.As(err, &inv), "esperava ErrInvalid, veio %v", err)
	assert.NotEmpty(t, inv.Message)
	return inv.Code, inv.Field
}

func TestValidate_Routes(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	cs := withCerts(t, s)
	app := mkApp(t, s.DB, "web", 20001)
	apexOnly := addCert(t, cs, "exemplo.com")
	both := addCert(t, cs, "loja.exemplo.net", "www.loja.exemplo.net")
	// Vencido: gravado direto (o serviço de certificados recusaria).
	expiredID, err := s.DB.CreateCertificate(ctx, store.Certificate{Name: "velho", Issuer: "x",
		Names: []string{"exemplo.com"}, NotAfter: time.Now().Add(-time.Hour), Fingerprint: "AA", PEMEnc: "x"})
	require.NoError(t, err)

	base := func() DomainInput {
		return DomainInput{Hostname: "exemplo.com", WWWRedirect: WWWNone, Routes: []RouteInput{
			{Path: "/", TargetType: TargetApp, AppID: &app.ID},
			{Path: "/static/", TargetType: TargetStatic, Root: "/var/www/site"},
		}}
	}
	manyRoutes := []RouteInput{{Path: "/", TargetType: TargetAddress, Address: "127.0.0.1:5000"}}
	for i := 0; i < 20; i++ {
		manyRoutes = append(manyRoutes, RouteInput{Path: fmt.Sprintf("/r%d/", i), TargetType: TargetAddress, Address: "127.0.0.1:5000"})
	}
	manyHeaders := make([]store.Header, 31)
	for i := range manyHeaders {
		manyHeaders[i] = store.Header{Name: fmt.Sprintf("X-H%d", i), Value: "1"}
	}

	cases := []struct {
		name  string
		mut   func(*DomainInput)
		code  string
		field string
	}{
		{"sem /", func(in *DomainInput) { in.Routes = in.Routes[1:] }, CodeInvalidRoute, "routes"},
		{"/api sem barra final", func(in *DomainInput) { in.Routes[1].Path = "/api" }, CodeInvalidPath, "routes[1].path"},
		{"//", func(in *DomainInput) { in.Routes[1].Path = "//" }, CodeInvalidPath, "routes[1].path"},
		{"caminho repetido", func(in *DomainInput) { in.Routes[1].Path = "/" }, CodeInvalidRoute, "routes[1].path"},
		{"21 rotas", func(in *DomainInput) { in.Routes = manyRoutes }, CodeInvalidRoute, "routes"},
		{"root com ..", func(in *DomainInput) { in.Routes[1].Root = "/var/www/../etc" }, CodeInvalidRoot, "routes[1].root"},
		{"root vizinho", func(in *DomainInput) { in.Routes[1].Root = "/var/www-evil" }, CodeInvalidRoot, "routes[1].root"},
		{"root relativo", func(in *DomainInput) { in.Routes[1].Root = "relative" }, CodeInvalidRoot, "routes[1].root"},
		{"prefixo imutável", func(in *DomainInput) { in.Routes[1].ImmutablePrefix = "assets" }, CodeInvalidPath, "routes[1].immutablePrefix"},
		{"tipo de rota", func(in *DomainInput) { in.Routes[1].TargetType = "x" }, CodeInvalidRoute, "routes[1].targetType"},
		{"app ausente", func(in *DomainInput) { in.Routes[0].AppID = nil }, CodeInvalidRoute, "routes[0].appId"},
		{"endereço com chaves", func(in *DomainInput) {
			in.Routes[0] = RouteInput{Path: "/", TargetType: TargetAddress, Address: "{env.X}:80"}
		}, CodeInvalidRoute, "routes[0].address"},
		{"corpo negativo", func(in *DomainInput) { in.Routes[0].MaxBodyBytes = -1 }, CodeInvalidRoute, "routes[0].maxBodyBytes"},
		{"valor com CRLF", func(in *DomainInput) {
			in.Headers = []store.Header{{Name: "X-A", Value: "a\r\nSet-Cookie: x=1"}}
		}, CodeInvalidHeader, "headers[0].value"},
		{"nome com espaço", func(in *DomainInput) { in.Headers = []store.Header{{Name: "X A", Value: "1"}} }, CodeInvalidHeader, "headers[0].name"},
		{"nome repetido", func(in *DomainInput) {
			in.Headers = []store.Header{{Name: "X-A", Value: "1"}, {Name: "x-a", Value: "2"}}
		}, CodeInvalidHeader, "headers[1].name"},
		{"31 cabeçalhos", func(in *DomainInput) { in.Headers = manyHeaders }, CodeInvalidHeader, "headers"},
		{"custom sem certificado", func(in *DomainInput) { in.TLS = &TLSInput{Mode: proxy.TLSCustom} }, CodeInvalidCert, "tls.certId"},
		{"certificado inexistente", func(in *DomainInput) { in.TLS = &TLSInput{Mode: proxy.TLSCustom, CertID: ptr(int64(999))} }, CodeInvalidCert, "tls.certId"},
		{"modo desconhecido", func(in *DomainInput) { in.TLS = &TLSInput{Mode: "x"} }, CodeInvalidCert, "tls.mode"},
		{"não cobre o par www", func(in *DomainInput) {
			in.WWWRedirect = WWWToApex
			in.TLS = &TLSInput{Mode: proxy.TLSCustom, CertID: &apexOnly.ID}
		}, CodeCertMismatch, "tls.certId"},
		{"não cobre o hostname", func(in *DomainInput) { in.TLS = &TLSInput{Mode: proxy.TLSCustom, CertID: &both.ID} }, CodeCertMismatch, "tls.certId"},
		{"vencido", func(in *DomainInput) { in.TLS = &TLSInput{Mode: proxy.TLSCustom, CertID: &expiredID} }, CodeCertExpired, "tls.certId"},
		{"hostname com chaves", func(in *DomainInput) { in.Hostname = "{x}.exemplo.com" }, CodeInvalidDomain, "hostname"},
	}
	for _, c := range cases {
		in := base()
		c.mut(&in)
		_, _, err := s.Create(ctx, in)
		code, field := codeField(t, err)
		assert.Equal(t, c.code, code, c.name)
		assert.Equal(t, c.field, field, c.name)
	}
	list, err := s.DB.ListDomains(ctx)
	require.NoError(t, err)
	assert.Empty(t, list, "nada é gravado")
	assert.Zero(t, fake.Applies(), "nada é aplicado")

	// A base e o certificado que cobre o par www passam.
	_, _, err = s.Create(ctx, base())
	require.NoError(t, err)
	_, _, err = s.Create(ctx, DomainInput{Hostname: "loja.exemplo.net", WWWRedirect: WWWToApex,
		TargetType: TargetAddress, Address: "127.0.0.1:5000",
		TLS: &TLSInput{Mode: proxy.TLSCustom, CertID: &both.ID}})
	require.NoError(t, err)
}
