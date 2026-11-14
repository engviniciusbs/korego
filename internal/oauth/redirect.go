// Package oauth reúne as regras puras do servidor de autorização OAuth 2.1
// usado pelos agentes MCP: lista de retornos permitidos, PKCE, saneamento do
// registro dinâmico e chave de limite por IP. Nada aqui toca em HTTP ou banco.
package oauth

import (
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// cursorRedirect é o gancho para o retorno do Cursor. Fica nil até capturarmos
// o retorno real do Cursor: enquanto for nil, nenhum retorno do Cursor é
// aceito. A regra, quando existir, é um único padrão exato, em que só o
// segmento do nome do servidor varia; nada de curingas soltos.
var cursorRedirect *regexp.Regexp

// vscodeRedirects são os únicos retornos https aceitos, comparados de forma exata.
var vscodeRedirects = map[string]string{
	"https://vscode.dev/redirect":          "VS Code (vscode.dev)",
	"https://insiders.vscode.dev/redirect": "VS Code Insiders (vscode.dev)",
}

// parseLoopback devolve a URL se uri é um retorno de loopback válido:
// http, host 127.0.0.1, localhost ou [::1], porta opcional válida, caminho
// limpo e absoluto, sem query, fragmento nem userinfo.
func parseLoopback(uri string) (*url.URL, bool) {
	if uri == "" || strings.ContainsAny(uri, "#\\ \t\r\n") {
		return nil, false
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || u.Opaque != "" {
		return nil, false
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return nil, false
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return nil, false
		}
	}
	// Caminho já decodificado: aceita vazio e barra final, mas recusa "..",
	// "." e "//" (qualquer forma não limpa).
	p := u.Path
	if p != "" {
		q := strings.TrimSuffix(p, "/")
		if p[0] != '/' || strings.Contains(p, "//") || (q != "" && path.Clean(q) != q) {
			return nil, false
		}
	}
	return u, true
}

// RedirectAllowed diz se o retorno pode ser usado por algum cliente.
func RedirectAllowed(uri string) bool {
	if _, ok := parseLoopback(uri); ok {
		return true
	}
	if _, ok := vscodeRedirects[uri]; ok {
		return true
	}
	return cursorRedirect != nil && cursorRedirect.MatchString(uri)
}

// RedirectMatches compara o retorno registrado com o pedido: igualdade exata,
// exceto no loopback, em que só a porta pode diferir (RFC 8252, seção 7.3).
func RedirectMatches(registered, requested string) bool {
	if !RedirectAllowed(registered) || !RedirectAllowed(requested) {
		return false
	}
	if registered == requested {
		return true
	}
	r, ok1 := parseLoopback(registered)
	q, ok2 := parseLoopback(requested)
	if !ok1 || !ok2 {
		return false
	}
	return r.Hostname() == q.Hostname() && r.Path == q.Path
}

// DescribeRedirect dá um nome legível ao destino, para a tela de consentimento.
// Só é chamada com retornos já permitidos.
func DescribeRedirect(uri string) string {
	if d, ok := vscodeRedirects[uri]; ok {
		return d
	}
	if cursorRedirect != nil && cursorRedirect.MatchString(uri) {
		return "Cursor"
	}
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	if _, ok := parseLoopback(uri); ok {
		if u.Port() == "" {
			return "este computador"
		}
		return "este computador (" + u.Host + ")"
	}
	return u.Scheme + "://" + u.Host
}
