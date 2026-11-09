//go:build integration && linux

package migrate

// Integração da troca nginx → Caddy com processos reais: o korego-caddy do
// pacote proxy e um nginx:1-alpine (container korego-it-nginx) em rede host
// segurando 80/443. O ServiceManager do teste mapeia a unit "nginx" para
// docker stop/start desse container.
//
// Precisa de Linux, Docker com rede host, das portas 80/443/18080/18443/2019
// livres e de root: o dono das portas é resolvido por /proc/<pid>/fd (o
// nginx e o Caddy rodam como root) e o site estático fica em /var/www. Sem
// root o teste é pulado; com KOREGO_IT_STRICT=1 (CI) ele falha em vez de
// pular.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	itNginx      = "korego-it-nginx"
	itNginxImage = "nginx:1-alpine"
	itNginxBody  = "nginx-it"
	itWWW        = "/var/www/korego-it"
	itProxyHost  = "site-it.korego.localhost"
	itStaticHost = "static-it.korego.localhost"
)

// itSkip pula o teste com a mensagem, ou falha com KOREGO_IT_STRICT=1.
func itSkip(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("KOREGO_IT_STRICT") == "1" {
		t.Fatalf("KOREGO_IT_STRICT=1: "+format, args...)
	}
	t.Skipf(format, args...)
}

func dockerCLI(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// selfSigned gera um certificado ECDSA autoassinado para os nomes; devolve
// o PEM do certificado, o da chave e o DER.
func selfSigned(t *testing.T, cn string, names ...string) (certPEM, keyPEM string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		DNSNames:              names,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	kb, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	return certPEM, keyPEM, der
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// leafFingerprint faz o handshake TLS em addr com SNI = host e devolve a
// impressão SHA-256 da folha servida.
func leafFingerprint(t *testing.T, addr, host string) string {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, //nolint:gosec // a folha é comparada pela impressão
	})
	require.NoError(t, err, "handshake em %s (%s)", addr, host)
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	require.NotEmpty(t, certs)
	return fingerprint(certs[0].Raw)
}

// httpsGet faz GET https://host+path conectando em addr (SNI e Host = host).
func httpsGet(t *testing.T, addr, host, path string) (int, http.Header, string) {
	t.Helper()
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{ServerName: host, InsecureSkipVerify: true}, //nolint:gosec // teste
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get("https://" + host + path)
	require.NoError(t, err, "GET https://%s%s via %s", host, path, addr)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

// httpGet faz GET http://127.0.0.1:80/ com o Host dado; erro de conexão volta como err.
func httpGet(host string) (int, string, error) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:80/", nil)
	if err != nil {
		return 0, "", err
	}
	req.Host = host
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

// listening diz se há quem aceite conexão TCP em addr.
func listening(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado esperando %s", what)
}

// containerUnit é o ServiceManager do teste: a unit "nginx" é o container
// korego-it-nginx. Start só volta com 80 e 443 aceitando conexões, como o
// systemctl start de um nginx (Type=forking) só volta depois do bind.
type containerUnit struct {
	mu      sync.Mutex
	enabled bool
	// afterStop roda depois de cada Stop (o teste de falha de bind pega a 443).
	afterStop func()
	// beforeStart roda antes de cada Start.
	beforeStart func()
}

var _ ServiceManager = (*containerUnit)(nil)

func (u *containerUnit) check(unit string) error {
	if unit != "nginx" {
		return fmt.Errorf("unit inesperada %q", unit)
	}
	return nil
}

func (u *containerUnit) Stop(ctx context.Context, unit string) error {
	if err := u.check(unit); err != nil {
		return err
	}
	if _, err := dockerCLI(ctx, "stop", "-t", "10", itNginx); err != nil {
		return err
	}
	u.mu.Lock()
	hook := u.afterStop
	u.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (u *containerUnit) Start(ctx context.Context, unit string) error {
	if err := u.check(unit); err != nil {
		return err
	}
	u.mu.Lock()
	hook := u.beforeStart
	u.mu.Unlock()
	if hook != nil {
		hook()
	}
	if _, err := dockerCLI(ctx, "start", itNginx); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for !listening("127.0.0.1:80") || !listening("127.0.0.1:443") {
		if time.Now().After(deadline) {
			return errors.New("o nginx não escutou em 80/443")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (u *containerUnit) Enable(_ context.Context, unit string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.enabled = true
	return u.check(unit)
}

func (u *containerUnit) Disable(_ context.Context, unit string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.enabled = false
	return u.check(unit)
}

func (u *containerUnit) isEnabled() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.enabled
}

func (u *containerUnit) Active(ctx context.Context, unit string) (bool, error) {
	if err := u.check(unit); err != nil {
		return false, err
	}
	out, err := dockerCLI(ctx, "inspect", "-f", "{{.State.Running}}", itNginx)
	if err != nil {
		return false, err
	}
	return out == "true", nil
}

func (u *containerUnit) Exists(_ context.Context, unit string) bool { return unit == "nginx" }

func (u *containerUnit) UnitForProcess(p string) (string, bool) { return UnitForProcess(p) }

// startNginx sobe o korego-it-nginx em rede host com 80 e 443 (certificado
// autoassinado) respondendo itNginxBody; devolve a impressão do certificado.
func startNginx(t *testing.T, ctx context.Context) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o755))
	certPEM, keyPEM, der := selfSigned(t, "nginx-it", "nginx-it.korego.localhost")
	conf := `server {
    listen 80 default_server;
    listen 443 ssl default_server;
    ssl_certificate /etc/nginx/korego-it/cert.pem;
    ssl_certificate_key /etc/nginx/korego-it/key.pem;
    default_type text/plain;
    location / { return 200 "` + itNginxBody + `"; }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "default.conf"), []byte(conf), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cert.pem"), []byte(certPEM), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.pem"), []byte(keyPEM), 0o600))
	_, err := dockerCLI(ctx, "run", "-d", "--name", itNginx, "--network", "host",
		"-v", filepath.Join(dir, "default.conf")+":/etc/nginx/conf.d/default.conf:ro",
		"-v", dir+":/etc/nginx/korego-it:ro",
		itNginxImage)
	require.NoError(t, err)
	waitFor(t, "o nginx responder em :80", 30*time.Second, func() bool {
		_, body, err := httpGet("qualquer.korego.localhost")
		return err == nil && body == itNginxBody
	})
	waitFor(t, "o nginx escutar em :443", 10*time.Second, func() bool { return listening("127.0.0.1:443") })
	return fingerprint(der)
}

// staticRelease grava releases/<n>/index.html com o conteúdo dado.
func staticRelease(t *testing.T, base string, n int, body string) {
	t.Helper()
	dir := filepath.Join(base, "releases", fmt.Sprint(n))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>"+body+"</title>"+body), 0o644))
}

func TestTakeoverRollback_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		itSkip(t, "precisa de root: o dono das portas (nginx e Caddy, root) só é resolvido por /proc/<pid>/fd como root, e o site estático fica em %s", itWWW)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		itSkip(t, "precisa do CLI docker no PATH: %v", err)
	}
	ctx := context.Background()
	d := docker.NewLocal()
	require.NoError(t, d.Ping(ctx))

	for _, port := range []int{80, 443, 18080, 18443} {
		if owner, busy := proxy.PortOwner(port); busy {
			itSkip(t, "a porta %d já está em uso por %s", port, owner)
		}
	}

	// Limpeza sempre, inclusive em falha: nginx de teste, korego-caddy e /var/www/korego-it.
	cleanup := func() {
		bg := context.Background()
		_, _ = dockerCLI(bg, "rm", "-f", itNginx)
		_ = d.RemoveForce(bg, proxy.ContainerName)
		_ = os.RemoveAll(itWWW)
	}
	cleanup()
	t.Cleanup(cleanup)

	// Site estático em /var/www/korego-it/migrate/current → releases/1.
	base := filepath.Join(itWWW, "migrate")
	if err := os.MkdirAll(base, 0o755); err != nil {
		itSkip(t, "não foi possível criar %s: %v", base, err)
	}
	staticRelease(t, base, 1, "static-v1")
	require.NoError(t, os.Symlink("releases/1", filepath.Join(base, "current")))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			_, _ = io.WriteString(w, "upstream-api")
			return
		}
		_, _ = io.WriteString(w, "upstream-root")
	}))
	t.Cleanup(upstream.Close)
	upAddr := strings.TrimPrefix(upstream.URL, "http://")

	// Um certificado próprio cobre os dois sites: nenhum é auto, então a
	// conferência de DNS não se aplica e não há ACME.
	certPEM, keyPEM, certDER := selfSigned(t, "korego-it", itProxyHost, itStaticHost)
	tlsCustom := proxy.SiteTLS{Mode: proxy.TLSCustom, CertPEM: certPEM + keyPEM}
	sites := []proxy.Site{
		{
			Host: itProxyHost,
			Routes: []proxy.SiteRoute{
				{Path: "/api/", Upstream: upAddr},
				{Path: "/", Upstream: upAddr},
			},
			TLS:     tlsCustom,
			Headers: []proxy.Header{{Name: "X-Teste", Value: "1"}},
		},
		{
			Host:   itStaticHost,
			Routes: []proxy.SiteRoute{{Path: "/", Static: &proxy.StaticTarget{Root: filepath.Join(base, "current"), SPA: true}}},
			TLS:    tlsCustom,
		},
	}

	nginxFP := startNginx(t, ctx)

	// O dono das portas visto do host é o processo nginx do container.
	owner, busy := proxy.PortOwner(443)
	require.True(t, busy)
	require.True(t, strings.HasPrefix(owner, "nginx (pid "), "dono da 443: %q", owner)
	unit, ok := UnitForProcess(owner)
	require.True(t, ok)
	require.Equal(t, "nginx", unit)

	// 1. Com o nginx em 80/443, Ensure dá trial (o nginx é alheio pelo PID).
	c := proxy.NewCaddy(d, &proxy.AdminClient{BaseURL: proxy.DefaultAdminURL})
	db := store.OpenMemory(t)
	c.Settings = db // modo gravado, como na produção
	c.UnitFor = UnitForProcess
	var st proxy.Status
	waitFor(t, "o Caddy ficar em trial", 3*time.Minute, func() bool { // inclui o pull da imagem
		st = c.Ensure(ctx)
		require.Contains(t, []proxy.State{proxy.StateStarting, proxy.StateTrial}, st.State, "Ensure: %+v", st)
		return st.State == proxy.StateTrial
	})
	require.Equal(t, proxy.ListenTrial, st.Mode)
	require.True(t, strings.HasPrefix(st.Process, "nginx (pid "), "processo: %q", st.Process)
	mode, _, err := db.GetSetting(ctx, proxy.SettingMode)
	require.NoError(t, err)
	require.Equal(t, "trial", mode, "ensaio gravado")
	unitSaved, _, err := db.GetSetting(ctx, proxy.SettingTrialUnit)
	require.NoError(t, err)
	require.Equal(t, "nginx", unitSaved)

	// 2. Sites aplicados em ensaio; o nginx continua em 80.
	require.NoError(t, c.Apply(ctx, sites, proxy.ListenTrial, nil))
	_, body, err := httpGet(itProxyHost)
	require.NoError(t, err)
	require.Equal(t, itNginxBody, body)

	// 3. Check no 18443 passa para os dois sites.
	checks := Check(ctx, sites, TrialAddr, true)
	require.Len(t, checks, 2)
	for _, sc := range checks {
		require.True(t, sc.OK, "ensaio de %s: %+v", sc.Host, sc)
		assert.True(t, sc.TLSOK && sc.LeafOK, "%s: %+v", sc.Host, sc)
	}
	assert.True(t, checks[1].Paths[0].Index, "o ensaio do estático deve servir o index.html: %+v", checks[1])
	assert.Equal(t, fingerprint(certDER), leafFingerprint(t, TrialAddr, itProxyHost))

	sd := &containerUnit{enabled: true}
	dom := &fakeDomains{sites: sites, status: st}
	e := &Engine{Domains: dom, Proxy: c, Systemd: sd, DB: db}
	require.NoError(t, e.PrecheckTakeover(ctx))

	// 4a. Falha de bind (Review Focus #2): o nginx para, mas outro processo
	// pega a 443 antes do Caddy. A troca volta: Caddy em ensaio e nginx de
	// novo em 80/443, resultado rolled_back.
	t.Run("falha de bind volta para o nginx", func(t *testing.T) {
		var thief net.Listener
		sd.mu.Lock()
		sd.afterStop = func() {
			l, err := net.Listen("tcp", ":443")
			if err != nil {
				t.Errorf("pegar a 443 depois do stop: %v", err)
				return
			}
			thief = l
		}
		sd.beforeStart = func() {
			if thief != nil {
				_ = thief.Close()
			}
		}
		sd.mu.Unlock()
		defer func() {
			sd.mu.Lock()
			sd.afterStop, sd.beforeStart = nil, nil
			sd.mu.Unlock()
			if thief != nil {
				_ = thief.Close()
			}
		}()

		res := e.Takeover(ctx)
		require.Equal(t, OutcomeRolledBack, res.Outcome, "resultado: %+v", res)
		// O motivo vem na mensagem: o bind em :443 falhou porque a porta estava em uso.
		assert.Contains(t, res.Message, "voltou para o nginx: o Caddy não conseguiu escutar em 80/443: ")
		assert.Contains(t, res.Message, "address already in use")

		active, err := sd.Active(ctx, "nginx")
		require.NoError(t, err)
		assert.True(t, active, "o nginx deve estar rodando")
		assert.True(t, sd.isEnabled(), "o nginx continua ativado no boot")
		_, body, err := httpGet(itProxyHost)
		require.NoError(t, err)
		assert.Equal(t, itNginxBody, body, "o nginx volta a responder em :80")
		assert.Equal(t, nginxFP, leafFingerprint(t, NormalAddr, itProxyHost), "o nginx volta a responder em :443")

		st := c.Ensure(ctx)
		assert.Equal(t, proxy.StateTrial, st.State, "o Caddy volta ao ensaio: %+v", st)
		assert.Equal(t, proxy.StateTrial, dom.Status().State)
		_, ok, err := e.DB.GetSetting(ctx, SettingMigratedFrom)
		require.NoError(t, err)
		assert.False(t, ok, "nada é registrado numa troca desfeita")
		for _, sc := range Check(ctx, sites, TrialAddr, true) {
			assert.True(t, sc.OK, "ensaio depois da volta, %s: %+v", sc.Host, sc)
		}
	})
	if t.Failed() {
		return
	}

	// 4b. Troca de verdade: o Caddy assume 80/443 e serve os dois sites.
	res := e.Takeover(ctx)
	require.Equal(t, OutcomeOK, res.Outcome, "resultado: %+v", res)
	active, err := sd.Active(ctx, "nginx")
	require.NoError(t, err)
	assert.False(t, active, "o nginx deve estar parado")
	assert.False(t, sd.isEnabled(), "o nginx é desativado no boot")
	from, ok, err := e.DB.GetSetting(ctx, SettingMigratedFrom)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "nginx", from)

	st = c.Ensure(ctx)
	assert.Equal(t, proxy.StateActive, st.State, "Ensure depois da troca: %+v", st)
	assert.Equal(t, proxy.ListenNormal, st.Mode)
	mode, _, err = db.GetSetting(ctx, proxy.SettingMode)
	require.NoError(t, err)
	assert.Equal(t, "normal", mode, "a troca grava o modo normal")
	owner, busy = proxy.PortOwner(443)
	require.True(t, busy)
	assert.False(t, strings.HasPrefix(owner, "nginx "), "a 443 é do Caddy agora: %q", owner)

	code, hdr, body := httpsGet(t, NormalAddr, itProxyHost, "/api/x")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "upstream-api", body)
	assert.Equal(t, "1", hdr.Get("X-Teste"))
	code, _, body = httpsGet(t, NormalAddr, itProxyHost, "/")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "upstream-root", body)
	code, _, body = httpsGet(t, NormalAddr, itStaticHost, "/rota/da/spa")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "static-v1")

	// TLS próprio: o handshake em 443 apresenta o certificado do teste.
	assert.Equal(t, fingerprint(certDER), leafFingerprint(t, NormalAddr, itProxyHost))
	assert.Equal(t, fingerprint(certDER), leafFingerprint(t, NormalAddr, itStaticHost))

	// 5. Voltar: o Caddy sai de 80/443 e o nginx responde em 80 de novo.
	require.NoError(t, e.PrecheckRollback(ctx))
	res = e.Rollback(ctx)
	require.Equal(t, OutcomeOK, res.Outcome, "resultado: %+v", res)
	active, err = sd.Active(ctx, "nginx")
	require.NoError(t, err)
	assert.True(t, active)
	assert.True(t, sd.isEnabled(), "voltar reativa o nginx no boot")
	_, body, err = httpGet(itProxyHost)
	require.NoError(t, err)
	assert.Equal(t, itNginxBody, body)
	assert.Equal(t, nginxFP, leafFingerprint(t, NormalAddr, itProxyHost))
	_, ok, err = e.DB.GetSetting(ctx, SettingMigratedFrom)
	require.NoError(t, err)
	assert.False(t, ok, "voltar apaga o registro da migração")

	st = c.Ensure(ctx)
	assert.Equal(t, proxy.StateTrial, st.State, "o Caddy volta ao ensaio: %+v", st)
	for _, sc := range Check(ctx, sites, TrialAddr, true) {
		assert.True(t, sc.OK, "ensaio depois de voltar, %s: %+v", sc.Host, sc)
	}
}
