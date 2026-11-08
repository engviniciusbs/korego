package usage

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	maskStr = "••••"
	maxCmd  = 160
)

var sensitiveWords = map[string]bool{
	"password": true, "passwd": true, "pass": true, "passphrase": true, "pwd": true,
	"token": true, "secret": true, "key": true, "auth": true, "authorization": true,
	"bearer": true, "credential": true, "credentials": true, "jwt": true,
	"cookie": true, "dsn": true,
}

// Sufixos de palavras coladas (apikey, accesstoken...). "monkey" fica de fora
// de propósito: só os nomes listados são mascarados.
var sensitiveSuffixes = []string{"apikey", "secretkey", "accesskey", "privatekey", "token", "password", "passwd", "secret"}

// scheme://usuario:senha@host → scheme://usuario:••••@host
var reUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^/\s:@]*):[^@/\s]*@`)

func sensitiveName(name string) bool {
	name = strings.ToLower(strings.TrimLeft(name, "-"))
	words := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ':'
	})
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

// splitKeep divide s em pedaços alternando espaço e não-espaço, respeitando
// aspas simples e duplas; concatenar os pedaços devolve s.
func splitKeep(s string) []string {
	var parts []string
	var cur strings.Builder
	var quote rune
	space := false
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for i, r := range s {
		isSp := quote == 0 && unicode.IsSpace(r)
		if i > 0 && isSp != space {
			flush()
		}
		space = isSp
		cur.WriteRune(r)
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote = r
		}
	}
	flush()
	return parts
}

// masker mantém o estado "nome seguido de valor" entre os tokens.
type masker struct{ maskNext bool }

// token mascara um token sem espaços (fora de aspas).
func (m *masker) token(a string) string {
	if m.maskNext {
		m.maskNext = false
		if !strings.HasPrefix(a, "-") {
			return maskStr
		}
	}
	a = reUserinfo.ReplaceAllString(a, "${1}:"+maskStr+"@")
	if i := strings.Index(a, "="); i > 0 {
		if sensitiveName(a[:i]) {
			return a[:i+1] + maskStr
		}
		return a
	}
	// Limitação conhecida: flags curtas (-pSEGREDO, -p segredo) não são
	// reconhecidas, pois não há como saber o que "-p" significa em cada programa.
	if strings.HasPrefix(a, "-") && sensitiveName(a) {
		m.maskNext = true
	}
	return a
}

// MaskCmd esconde valores sensíveis do argv e limita o texto a 160 caracteres.
// Elementos com espaço (ex.: sh -c "...") são divididos em tokens, mantendo o
// texto original; nada é descartado além do corte final.
func MaskCmd(argv []string) string {
	var m masker
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if strings.IndexFunc(a, unicode.IsSpace) < 0 {
			out = append(out, m.token(a))
			continue
		}
		if m.maskNext && !strings.HasPrefix(a, "-") {
			m.maskNext = false
			out = append(out, maskStr)
			continue
		}
		var b strings.Builder
		for _, p := range splitKeep(a) {
			if r := []rune(p)[0]; unicode.IsSpace(r) {
				b.WriteString(p)
			} else {
				b.WriteString(m.token(p))
			}
		}
		out = append(out, b.String())
	}
	s := strings.Join(out, " ")
	if r := []rune(s); len(r) > maxCmd {
		s = string(r[:maxCmd-1]) + "…"
	}
	return s
}
