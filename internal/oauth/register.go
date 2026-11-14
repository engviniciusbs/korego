package oauth

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// DefaultClientName é o nome usado quando o agente não informa um.
const DefaultClientName = "Agente sem nome"

const (
	maxRedirectURIs  = 5
	maxClientNameLen = 64
)

var (
	// ErrInvalidRedirect: nenhum retorno foi aceito, ou a quantidade é inválida.
	ErrInvalidRedirect = errors.New("redirect_uri não permitido")
	// ErrInvalidMetadata: o corpo do registro não é um JSON utilizável.
	ErrInvalidMetadata = errors.New("metadados do cliente inválidos")
)

// RedirectError carrega o retorno recusado e satisfaz errors.Is(ErrInvalidRedirect).
type RedirectError struct{ URI string }

func (e RedirectError) Error() string        { return "redirect_uri não permitido: " + e.URI }
func (e RedirectError) Is(target error) bool { return target == ErrInvalidRedirect }

// Registration é o que guardamos de um registro dinâmico; o resto é descartado.
type Registration struct {
	ClientName   string
	RedirectURIs []string
	GrantTypes   []string
	// Dropped lista os retornos recusados e descartados (sem repetição), para
	// o handler registrar no log; não é guardado.
	Dropped []string
}

var defaultGrants = []string{"authorization_code", "refresh_token"}

// SanitizeRegistration valida o corpo do registro (RFC 7591) e guarda só o
// necessário: campos desconhecidos somem, o nome é limpo e os grant_types
// ficam restritos aos dois suportados. Retornos fora da lista são descartados
// (e listados em Dropped, também no erro); só é erro quando nenhum retorno sobra.
func SanitizeRegistration(in json.RawMessage) (Registration, error) {
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
	}
	if err := json.Unmarshal(in, &body); err != nil {
		return Registration{}, ErrInvalidMetadata
	}
	if n := len(body.RedirectURIs); n == 0 || n > maxRedirectURIs {
		return Registration{}, ErrInvalidRedirect
	}
	reg := Registration{}
	seen := map[string]bool{}
	for _, u := range body.RedirectURIs {
		if seen[u] {
			continue
		}
		seen[u] = true
		if !RedirectAllowed(u) {
			reg.Dropped = append(reg.Dropped, u)
			continue
		}
		reg.RedirectURIs = append(reg.RedirectURIs, u)
	}
	if len(reg.RedirectURIs) == 0 {
		return Registration{Dropped: reg.Dropped}, RedirectError{URI: reg.Dropped[0]}
	}
	reg.ClientName = cleanName(body.ClientName)
	for _, g := range body.GrantTypes {
		if (g == "authorization_code" || g == "refresh_token") && !contains(reg.GrantTypes, g) {
			reg.GrantTypes = append(reg.GrantTypes, g)
		}
	}
	if len(reg.GrantTypes) == 0 {
		reg.GrantTypes = append([]string(nil), defaultGrants...)
	}
	return reg, nil
}

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		// Controles e caracteres de formatação (Cf: inversões bidi como
		// U+202E, espaços de largura zero como U+200B) somem do nome.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxClientNameLen {
		s = strings.TrimSpace(string(r[:maxClientNameLen]))
	}
	if s == "" {
		return DefaultClientName
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
