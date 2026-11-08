package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
)

// Cert faz um handshake TLS em CertAddr com SNI host e classifica o
// certificado servido:
//   - validado pelas raízes (CertRoots, ou as do sistema) → active;
//   - handshake sem certificado válido (ainda o interno do Caddy, ou nenhum
//     emitido) → pending;
//   - sem conexão TCP → error.
func (c *Caddy) Cert(ctx context.Context, host string) CertInfo {
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
	conn := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: c.CertRoots, MinVersion: tls.VersionTLS12})
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
	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	return CertInfo{State: CertActive, NotAfter: leaf.NotAfter, Issuer: issuer}
}
