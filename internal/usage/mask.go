package usage

import "strings"

const (
	maskStr = "••••"
	maxCmd  = 160
)

var sensitiveWords = map[string]bool{
	"password": true, "passwd": true, "token": true, "secret": true, "key": true,
}

// Sufixos de palavras coladas (apikey, accesstoken...). "monkey" fica de fora
// de propósito: só os nomes listados são mascarados.
var sensitiveSuffixes = []string{"apikey", "secretkey", "accesskey", "privatekey", "token", "password", "passwd", "secret"}

func sensitiveName(name string) bool {
	name = strings.ToLower(strings.TrimLeft(name, "-"))
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for _, w := range words {
		if sensitiveWords[w] {
			return true
		}
		for _, s := range sensitiveSuffixes {
			if strings.HasSuffix(w, s) {
				return true
			}
		}
	}
	return false
}

// MaskCmd esconde valores sensíveis do argv e limita o texto a 160 caracteres.
func MaskCmd(argv []string) string {
	out := make([]string, 0, len(argv))
	maskNext := false
	for _, a := range argv {
		if maskNext {
			maskNext = false
			if !strings.HasPrefix(a, "-") {
				out = append(out, maskStr)
				continue
			}
		}
		if i := strings.Index(a, "="); i > 0 && sensitiveName(a[:i]) {
			out = append(out, a[:i+1]+maskStr)
			continue
		}
		if strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && sensitiveName(a) {
			maskNext = true
		}
		out = append(out, a)
	}
	s := strings.Join(out, " ")
	if r := []rune(s); len(r) > maxCmd {
		s = string(r[:maxCmd-1]) + "…"
	}
	return s
}
