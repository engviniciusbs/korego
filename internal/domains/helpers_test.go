package domains

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeResolver devolve ips ou err para qualquer host.
type fakeResolver struct {
	ips []net.IP
	err error
}

func (f fakeResolver) LookupIP(_ context.Context, _ string) ([]net.IP, error) {
	return f.ips, f.err
}

func newTestService(t *testing.T) (*Service, *proxytest.Fake) {
	t.Helper()
	fake := &proxytest.Fake{}
	s := &Service{
		DB:        store.OpenMemory(t),
		Proxy:     fake,
		Hub:       events.NewHub(),
		Resolver:  fakeResolver{ips: []net.IP{net.ParseIP("203.0.113.10")}},
		HostIPs:   func() ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil },
		PanelPort: 8000,
	}
	return s, fake
}

// mkApp cria um projeto e uma app com a porta informada.
func mkApp(t *testing.T, db *store.DB, slug string, hostPort int) store.App {
	t.Helper()
	ctx := context.Background()
	p := &store.Project{Name: "proj-" + slug}
	require.NoError(t, db.InsertProject(ctx, p))
	a := &store.App{ProjectID: p.ID, Slug: slug, SourceType: "image", Image: "nginx:1-alpine",
		BuildType: "image", InternalPort: 80, HealthTimeoutS: 60, HostPort: hostPort}
	require.NoError(t, db.InsertApp(ctx, a))
	return *a
}

func ptr[T any](v T) *T { return &v }

// genCertPEM gera um certificado autoassinado (com os nomes DNS) e a chave
// EC num único texto PEM.
func genCertPEM(t *testing.T, notAfter time.Time, names ...string) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<60))
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: names[0]},
		NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter, DNSNames: names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	require.NoError(t, err)
	kd, err := x509.MarshalECPrivateKey(k)
	require.NoError(t, err)
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})...)
}

// withCerts liga um certs.Service (com caixa de segredos nova) ao serviço.
func withCerts(t *testing.T, s *Service) *certs.Service {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	s.Certs = &certs.Service{DB: s.DB, Box: box}
	return s.Certs
}

// addCert cadastra um certificado válido por 90 dias para os nomes.
func addCert(t *testing.T, cs *certs.Service, names ...string) store.Certificate {
	t.Helper()
	c, err := cs.Add(context.Background(), names[0], genCertPEM(t, time.Now().Add(90*24*time.Hour), names...))
	require.NoError(t, err)
	return c
}

// fixedRanges são faixas da Cloudflare fixas para os testes.
type fixedRanges []string

func (f fixedRanges) Get() []string { return f }

// autoTLS é o TLS dos sites com certificado automático.
var autoTLS = proxy.SiteTLS{Mode: proxy.TLSAuto}
