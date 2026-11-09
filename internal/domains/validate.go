package domains

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidHostname informa se s é um hostname aceito: minúsculo, total ≤ 253,
// ao menos dois rótulos e cada rótulo em ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$.
// Endereços IP não são hostnames.
func ValidHostname(s string) bool {
	if s == "" || len(s) > 253 || net.ParseIP(s) != nil {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// ValidAddress informa se s é um endereço manual host:porta, com host IP,
// localhost ou hostname válido e porta 1–65535.
func ValidAddress(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" || port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return false
	}
	return host == "localhost" || net.ParseIP(host) != nil || ValidHostname(host)
}

// validRoutes confere as rotas da entrada e devolve as rotas a gravar, na
// ordem recebida. Só os campos do tipo de destino são mantidos.
func (s *Service) validRoutes(ctx context.Context, in []RouteInput) ([]store.DomainRoute, error) {
	if len(in) > proxy.MaxRoutes {
		return nil, invalidCode(CodeInvalidRoute, "routes", fmt.Sprintf("Use no máximo %d rotas por domínio.", proxy.MaxRoutes))
	}
	out := make([]store.DomainRoute, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, r := range in {
		f := fmt.Sprintf("routes[%d].", i)
		p := strings.TrimSpace(r.Path)
		if !proxy.ValidPath(p) {
			return nil, invalidCode(CodeInvalidPath, f+"path",
				"Caminho inválido. Use / ou um prefixo que comece e termine com /, como /api/, com letras minúsculas, números e . _ ~ -.")
		}
		if seen[p] {
			return nil, invalidCode(CodeInvalidRoute, f+"path", fmt.Sprintf("O caminho %s aparece mais de uma vez.", p))
		}
		seen[p] = true
		rt := store.DomainRoute{Position: i, Path: p, TargetType: r.TargetType}
		switch r.TargetType {
		case TargetApp:
			if r.AppID == nil {
				return nil, invalidCode(CodeInvalidRoute, f+"appId", "Escolha a aplicação da rota.")
			}
			if _, err := s.DB.GetApp(ctx, *r.AppID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, invalidCode(CodeInvalidRoute, f+"appId", "Aplicação não encontrada.")
				}
				return nil, err
			}
			id := *r.AppID
			rt.AppID = &id
		case TargetAddress:
			rt.Address = strings.TrimSpace(r.Address)
			if !ValidAddress(rt.Address) {
				return nil, invalidCode(CodeInvalidRoute, f+"address", "Endereço inválido. Use host:porta, como 127.0.0.1:5000.")
			}
		case TargetStatic:
			root := strings.TrimSpace(r.Root)
			if !proxy.ValidStaticRoot(root) {
				return nil, invalidCode(CodeInvalidRoot, f+"root",
					"Pasta inválida. Use um caminho absoluto dentro de /var/www, como /var/www/site.")
			}
			rt.Root = path.Clean(root)
			rt.SPA = r.SPA
			rt.ImmutablePrefix = strings.TrimSpace(r.ImmutablePrefix)
			if rt.ImmutablePrefix != "" && !proxy.ValidImmutablePrefix(rt.ImmutablePrefix) {
				return nil, invalidCode(CodeInvalidPath, f+"immutablePrefix",
					"Prefixo de cache inválido. Use um caminho que comece e termine com /, como /assets/.")
			}
		default:
			return nil, invalidCode(CodeInvalidRoute, f+"targetType", "Destino inválido. Use app, address ou static.")
		}
		if r.TargetType != TargetStatic {
			if r.MaxBodyBytes < 0 {
				return nil, invalidCode(CodeInvalidRoute, f+"maxBodyBytes", "O limite do corpo não pode ser negativo (0 = sem limite).")
			}
			rt.MaxBodyBytes, rt.NoBuffering = r.MaxBodyBytes, r.NoBuffering
		}
		out = append(out, rt)
	}
	if !seen["/"] {
		return nil, invalidCode(CodeInvalidRoute, "routes", "A rota / é obrigatória.")
	}
	return out, nil
}

// validHeaders confere os cabeçalhos de resposta: até proxy.MaxHeaders, nome
// em token HTTP, valor sem quebra de linha e nomes sem repetição (sem
// diferenciar maiúsculas).
func validHeaders(in []store.Header) ([]store.Header, error) {
	if len(in) > proxy.MaxHeaders {
		return nil, invalidCode(CodeInvalidHeader, "headers", fmt.Sprintf("Use no máximo %d cabeçalhos por domínio.", proxy.MaxHeaders))
	}
	out := make([]store.Header, 0, len(in))
	names := make(map[string]bool, len(in))
	for i, h := range in {
		f := fmt.Sprintf("headers[%d].", i)
		if !proxy.ValidHeaderName(h.Name) {
			return nil, invalidCode(CodeInvalidHeader, f+"name", "Nome de cabeçalho inválido. Use letras, números e hífens, sem espaços.")
		}
		if strings.ContainsAny(h.Value, "\r\n") {
			return nil, invalidCode(CodeInvalidHeader, f+"value", "O valor do cabeçalho não pode ter quebra de linha.")
		}
		key := strings.ToLower(h.Name)
		if names[key] {
			return nil, invalidCode(CodeInvalidHeader, f+"name", fmt.Sprintf("O cabeçalho %s aparece mais de uma vez.", h.Name))
		}
		names[key] = true
		out = append(out, store.Header{Name: h.Name, Value: h.Value})
	}
	return out, nil
}

// validTLS confere a escolha do certificado para os hosts do domínio (o
// hostname e o par www). Devolve o modo, o certificado e o PEM decifrado
// (só custom).
func (s *Service) validTLS(ctx context.Context, in TLSInput, hosts []string) (string, *int64, string, error) {
	switch in.Mode {
	case "", proxy.TLSAuto:
		return proxy.TLSAuto, nil, "", nil
	case proxy.TLSCustom:
	default:
		return "", nil, "", invalidCode(CodeInvalidCert, "tls.mode", "Modo de certificado inválido. Use auto ou custom.")
	}
	if in.CertID == nil {
		return "", nil, "", invalidCode(CodeInvalidCert, "tls.certId", "Escolha o certificado próprio do domínio.")
	}
	if s.Certs == nil {
		return "", nil, "", invalidCode(CodeInvalidCert, "tls.certId", "Certificados próprios não estão disponíveis.")
	}
	c, err := s.DB.CertificateByID(ctx, *in.CertID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil, "", invalidCode(CodeInvalidCert, "tls.certId", "Certificado não encontrado.")
	}
	if err != nil {
		return "", nil, "", err
	}
	if !c.NotAfter.After(s.now()) {
		return "", nil, "", invalidCode(CodeCertExpired, "tls.certId", "O certificado escolhido está expirado.")
	}
	for _, h := range hosts {
		if !certs.Covers(c.Names, h) {
			return "", nil, "", invalidCode(CodeCertMismatch, "tls.certId", fmt.Sprintf("O certificado escolhido não cobre %s.", h))
		}
	}
	pemText, err := s.Certs.PEM(ctx, c.ID)
	if err != nil {
		log.Printf("domínios: ler certificado %d: %v", c.ID, err)
		return "", nil, "", invalidCode(CodeInvalidCert, "tls.certId", "O certificado não pôde ser lido. Cadastre-o de novo.")
	}
	id := c.ID
	return proxy.TLSCustom, &id, pemText, nil
}

// placeholderCF são faixas usadas só na conferência final quando não há
// faixas da Cloudflare carregadas (a validação não depende delas).
var placeholderCF = []string{"192.0.2.0/24"}

// placeholderUpstream é o destino das rotas app na conferência final (a app
// pode ainda não ter porta).
const placeholderUpstream = "127.0.0.1:1"

// buildCheck é a conferência final: o domínio sozinho precisa ser aceito pelo
// proxy.BuildConfig. As checagens acima dão o campo e o código; esta pega o
// que escapar delas, para nada que o proxy recusaria chegar ao banco.
func (s *Service) buildCheck(d store.Domain, pemText string) error {
	site, err := siteOf(d, func(int64) string { return placeholderUpstream })
	if err == nil {
		site.TLS.CertPEM = pemText
		cf := s.cfRanges()
		if len(cf) == 0 {
			cf = placeholderCF
		}
		_, err = proxy.BuildConfig([]proxy.Site{site}, proxy.ListenNormal, cf)
	}
	if err != nil {
		return invalid("", fmt.Sprintf("O domínio não pôde ser montado no proxy: %v", err))
	}
	return nil
}
