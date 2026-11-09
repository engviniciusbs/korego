package migrate

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// Endereços verificados: o Caddy em ensaio e o Caddy nas portas normais.
const (
	TrialAddr  = "127.0.0.1:18443"
	NormalAddr = "127.0.0.1:443"
)

const (
	checkDialTimeout   = 5 * time.Second
	checkRequestTimout = 10 * time.Second
	checkConcurrency   = 4
	checkBodyLimit     = 64 << 10
)

// checkHeaders são os cabeçalhos de resposta mostrados no resultado.
var checkHeaders = []string{"Content-Type", "Cache-Control", "Location", "Content-Encoding", "Server", "Strict-Transport-Security"}

// PathCheck é o resultado de um GET numa rota.
type PathCheck struct {
	Path    string            `json:"path"`
	Static  bool              `json:"static"`
	OK      bool              `json:"ok"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Index: rota estática respondeu 200 com HTML (o index.html).
	Index bool   `json:"index"`
	Error string `json:"error,omitempty"`
}

// CertSummary resume o certificado folha servido.
type CertSummary struct {
	Names    []string  `json:"names"`
	NotAfter time.Time `json:"notAfter,omitempty"`
	Issuer   string    `json:"issuer,omitempty"`
}

// SiteCheck é o resultado da verificação de um site.
type SiteCheck struct {
	Host string `json:"host"`
	// Auto: o site usa certificado automático.
	Auto bool `json:"auto"`
	// OK: o site passa (handshake ok e toda rota abaixo de 500; em ensaio, um
	// site auto sem handshake passa como Pending).
	OK bool `json:"ok"`
	// Pending: certificado automático pendente até assumir as portas.
	Pending bool `json:"pending"`
	TLSOK   bool `json:"tlsOk"`
	// LeafOK: a folha cobre o host e está no prazo.
	LeafOK  bool        `json:"leafOk"`
	Paths   []PathCheck `json:"paths"`
	Leaf    CertSummary `json:"leaf"`
	Message string      `json:"message,omitempty"`
}

// isAuto diz se o site usa certificado automático.
func isAuto(s proxy.Site) bool {
	return s.TLS.Mode == "" || s.TLS.Mode == proxy.TLSAuto
}

// Check verifica cada site em addr: um handshake TLS com SNI = host (sem
// validar a cadeia; a folha vai no resultado) e, por rota, um GET no caminho
// da rota com Host = host, sem seguir redirecionamentos.
//
// Passa (D5) com o handshake ok, a folha cobrindo o host e no prazo, e todo
// status abaixo de 500 (403 inclusive: um site cloudflareOnly recusa o
// loopback no modo normal, e um site comum pode responder 403 na raiz; a
// folha é o que prova o certificado). Com trial, um
// site auto (o certificado só sai depois de assumir as portas) é Pending:
// falha de handshake não o reprova e a folha (emissor interno) não é
// conferida. A saída segue a ordem de sites.
func Check(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
	out := make([]SiteCheck, len(sites))
	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup
	for i, s := range sites {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = checkSite(ctx, s, addr, trial)
		}()
	}
	wg.Wait()
	return out
}

func checkSite(ctx context.Context, s proxy.Site, addr string, trial bool) SiteCheck {
	sc := SiteCheck{Host: s.Host, Auto: isAuto(s), Paths: []PathCheck{}}
	sc.Pending = trial && sc.Auto
	tlsCfg := &tls.Config{
		ServerName:         s.Host,
		InsecureSkipVerify: true, //nolint:gosec // a folha é reportada, não validada
		NextProtos:         []string{"http/1.1"},
	}
	dial := func(ctx context.Context) (*tls.Conn, error) {
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: checkDialTimeout}, Config: tlsCfg}
		dctx, cancel := context.WithTimeout(ctx, checkDialTimeout)
		defer cancel()
		c, err := d.DialContext(dctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return c.(*tls.Conn), nil
	}

	conn, err := dial(ctx)
	if err != nil {
		msg := "handshake TLS falhou: " + err.Error()
		if sc.Pending {
			sc.OK = true
			sc.Message = "certificado pendente até assumir as portas"
			return sc
		}
		sc.Message = msg
		return sc
	}
	sc.TLSOK = true
	leafErr := "o servidor não enviou certificado"
	if st := conn.ConnectionState(); len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		sc.Leaf = CertSummary{Names: leaf.DNSNames, NotAfter: leaf.NotAfter, Issuer: leaf.Issuer.CommonName}
		if sc.Leaf.Names == nil {
			sc.Leaf.Names = []string{}
		}
		leafErr = leafProblem(leaf, s.Host, time.Now())
	}
	_ = conn.Close()
	sc.LeafOK = leafErr == ""
	if !sc.LeafOK && !sc.Pending {
		sc.Message = "certificado: " + leafErr
		return sc
	}

	tr := &http.Transport{
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: checkRequestTimout,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   checkRequestTimout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	sc.OK = true
	for _, r := range s.Routes {
		pc := checkPath(ctx, client, s.Host, r)
		sc.Paths = append(sc.Paths, pc)
		if !pc.OK && sc.OK {
			sc.OK = false
			if pc.Error != "" {
				sc.Message = fmt.Sprintf("GET %s: %s", pc.Path, pc.Error)
			} else {
				sc.Message = fmt.Sprintf("GET %s: %d", pc.Path, pc.Status)
			}
		}
	}
	if sc.OK && sc.Pending {
		sc.Message = "certificado pendente até assumir as portas"
	}
	return sc
}

// leafProblem diz por que a folha não serve para host ("" = serve).
func leafProblem(leaf *x509.Certificate, host string, now time.Time) string {
	if err := leaf.VerifyHostname(host); err != nil {
		return "a folha não cobre " + host
	}
	if now.After(leaf.NotAfter) {
		return "certificado vencido desde " + leaf.NotAfter.UTC().Format(time.RFC3339)
	}
	if now.Before(leaf.NotBefore) {
		return "a folha só vale a partir de " + leaf.NotBefore.UTC().Format(time.RFC3339)
	}
	return ""
}

func checkPath(ctx context.Context, client *http.Client, host string, r proxy.SiteRoute) PathCheck {
	pc := PathCheck{Path: r.Path, Static: r.Static != nil}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+r.Path, nil)
	if err != nil {
		pc.Error = err.Error()
		return pc
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		pc.Error = err.Error()
		return pc
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, checkBodyLimit))
	pc.Status = resp.StatusCode
	pc.OK = resp.StatusCode < 500
	pc.Headers = map[string]string{}
	for _, h := range checkHeaders {
		if v := resp.Header.Get(h); v != "" {
			pc.Headers[h] = v
		}
	}
	pc.Index = pc.Static && resp.StatusCode == http.StatusOK &&
		strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html")
	return pc
}
