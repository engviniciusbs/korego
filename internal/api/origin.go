package api

import (
	"context"
	"log"

	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// mcpResourceMetadataPath é onde o /mcp publica os metadados do recurso protegido (RFC 9728).
const mcpResourceMetadataPath = "/.well-known/oauth-protected-resource/mcp"

// originFunc devolve a origem canônica (https://<domínio do painel>) e se ela existe.
// Erro do banco equivale a "sem origem" e fica no log.
type originFunc func(context.Context) (string, bool)

// panelOrigin devolve https://<domínio> quando o painel tem domínio com
// certificado ativo. dom nil dá ("", false, nil).
func panelOrigin(ctx context.Context, dom *domains.Service) (string, bool, error) {
	if dom == nil {
		return "", false, nil
	}
	p, err := dom.Panel(ctx)
	if err != nil {
		return "", false, err
	}
	if p.Domain == "" || p.Cert.State != proxy.CertActive {
		return "", false, nil
	}
	return "https://" + p.Domain, true, nil
}

// resolveOrigin escolhe a origem do OAuth: o override de teste, se houver,
// senão a derivada de d.Domains.
func resolveOrigin(d Deps) originFunc {
	if d.OAuthOrigin != nil {
		return d.OAuthOrigin
	}
	return func(ctx context.Context) (string, bool) {
		o, ok, err := panelOrigin(ctx, d.Domains)
		if err != nil {
			log.Printf("oauth: origem do painel indisponível: %v", err)
			return "", false
		}
		return o, ok
	}
}
