package proxy

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Cert faz um handshake TLS em CertAddr com SNI host e classifica o
// certificado servido:
//   - validado pelas raízes (CertRoots, ou as do sistema) → active;
//   - handshake sem certificado válido (ainda o interno do Caddy, ou nenhum
//     emitido) → pending;
//   - sem conexão TCP → error.
func (c *Caddy) Cert(ctx context.Context, host string) CertInfo {
	return c.handshake(ctx, host, "")
}

// CertPinned é o Cert de um certificado próprio: em vez das raízes do sistema
// (o certificado de origem da Cloudflare, por exemplo, só é confiável para a
// Cloudflare), compara o certificado servido com o fingerprint guardado
// (SHA-256 do DER, hex maiúsculo em pares separados por ":") e o prazo.
func (c *Caddy) CertPinned(ctx context.Context, host, fingerprint string) CertInfo {
	return c.handshake(ctx, host, fingerprint)
}

// Fingerprint é o SHA-256 do DER no formato guardado em certificates.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func (c *Caddy) handshake(ctx context.Context, host, pinned string) CertInfo {
	timeout := c.CertTimeout
	if timeout <= 0 {
		timeout = defaultCertWait
	}
	addr := c.CertAddr
	if addr == "" {
		addr = defaultCertAddr
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return CertInfo{State: CertError, Message: "sem conexão com o proxy em " + addr + ": " + err.Error()}
	}
	cfg := &tls.Config{ServerName: host, RootCAs: c.CertRoots, MinVersion: tls.VersionTLS12}
	if pinned != "" {
		// A confiança vem do fingerprint, conferido logo abaixo.
		cfg.InsecureSkipVerify = true
	}
	conn := tls.Client(raw, cfg)
	defer conn.Close()

	if err := conn.HandshakeContext(ctx); err != nil {
		var verr *tls.CertificateVerificationError
		if errors.As(err, &verr) {
			return CertInfo{State: CertPending, Message: "certificado ainda não confiável: " + verr.Err.Error()}
		}
		return CertInfo{State: CertPending, Message: "handshake TLS falhou: " + err.Error()}
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return CertInfo{State: CertPending, Message: "nenhum certificado servido"}
	}
	leaf := certs[0]
	if pinned != "" {
		if !strings.EqualFold(Fingerprint(leaf.Raw), pinned) {
			return CertInfo{State: CertPending, Message: "o certificado servido não é o certificado escolhido (a configuração ainda não foi aplicada?)"}
		}
		if time.Now().After(leaf.NotAfter) {
			return CertInfo{State: CertError, NotAfter: leaf.NotAfter, Message: "certificado vencido"}
		}
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	return CertInfo{State: CertActive, NotAfter: leaf.NotAfter, Issuer: issuer}
}
