package usage

import (
	"regexp"
	"strings"
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

// scheme://usuario:senha@host → scheme://usuario:••••@host. A senha vai até o
// último @ antes de / ou espaço (ela pode ter @). Não atravessa para uma
// segunda URL no mesmo texto, porque o "://" dela tem /.
var reUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^/\s:@]*):[^/\s]*@`)

// reAssignName é o nome de um elemento nome=valor (com ou sem - / --).
var reAssignName = regexp.MustCompile(`^-{0,2}[A-Za-z_][A-Za-z0-9_.:\-]*$`)

func scrubUserinfo(s string) string {
	return reUserinfo.ReplaceAllString(s, "${1}:"+maskStr+"@")
}

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

// masker mantém o estado "nome seguido de valor" entre os tokens.
type masker struct{ maskNext bool }

func isQuote(r byte) bool { return r == '"' || r == '\'' }

func isSep(r byte) bool { return r == ';' || r == '&' || r == '|' }

func isSpaceByte(r byte) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

// maskText percorre um texto de shell: espaços e separadores (; & |) são
// copiados como estão e cada palavra passa por word. Concatenar o resultado
// só difere do original nos valores secretos.
func (m *masker) maskText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case isSpaceByte(s[i]):
			j := i
			for j < len(s) && isSpaceByte(s[j]) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
		case isSep(s[i]):
			j := i
			for j < len(s) && isSep(s[j]) {
				j++
			}
			m.maskNext = false
			b.WriteString(s[i:j])
			i = j
		default:
			j := i
			var q byte
			for j < len(s) {
				c := s[j]
				if q != 0 {
					if c == q {
						q = 0
					}
				} else if isQuote(c) {
					q = c
				} else if isSpaceByte(c) || isSep(c) {
					break
				}
				j++
			}
			b.WriteString(m.word(s[i:j]))
			i = j
		}
	}
	return b.String()
}

// wrapped trata um texto que começa com aspas: o conteúdo é mascarado
// recursivamente e as aspas originais são mantidas.
func (m *masker) wrapped(w string) string {
	q := w[0]
	inner, closed := w[1:], false
	if len(w) >= 2 && w[len(w)-1] == q {
		inner, closed = w[1:len(w)-1], true
	}
	out := string(q) + m.maskText(inner)
	if closed {
		out += string(q)
	}
	return out
}

// secret substitui um valor secreto, mantendo as aspas que o cercam.
func secret(v string) string {
	if len(v) >= 2 && isQuote(v[0]) && v[len(v)-1] == v[0] {
		return v[:1] + maskStr + v[len(v)-1:]
	}
	return maskStr
}

// word mascara uma palavra (sem espaços fora de aspas).
func (m *masker) word(a string) string {
	if m.maskNext {
		m.maskNext = false
		if !strings.HasPrefix(a, "-") {
			return secret(a)
		}
	}
	if a == "" {
		return a
	}
	if isQuote(a[0]) {
		return m.wrapped(a)
	}
	a = scrubUserinfo(a)
	if i := strings.Index(a, "="); i > 0 {
		name, v := a[:i+1], a[i+1:]
		if sensitiveName(a[:i]) {
			return name + secret(v)
		}
		if v != "" && isQuote(v[0]) {
			return name + m.wrapped(v)
		}
		return a
	}
	// Limitação conhecida: flags curtas (-pSEGREDO, -p segredo) não são
	// reconhecidas, pois não há como saber o que "-p" significa em cada programa.
	if (strings.HasPrefix(a, "-") && sensitiveName(a)) || isBearer(a) {
		m.maskNext = true
	}
	return a
}

// assign trata um elemento do argv que é ele mesmo nome=valor com nome
// sensível (ex.: "--password=duas palavras", "API_TOKEN=a&b"): o argv não
// passa por shell, então espaços e separadores são parte do valor e tudo
// depois do = é trocado. Um script de sh -c que comece com TOKEN=…;X=… fica
// todo escondido (aceito). Valor entre aspas fica com maskText, que já mantém
// as aspas e o que vem depois delas.
func (m *masker) assign(a string) (string, bool) {
	i := strings.Index(a, "=")
	if i <= 0 || !reAssignName.MatchString(a[:i]) || !sensitiveName(a[:i]) {
		return "", false
	}
	if v := a[i+1:]; v != "" && isQuote(v[0]) {
		return "", false
	}
	m.maskNext = false
	return a[:i+1] + maskStr, true
}

// isBearer diz se a palavra é "Bearer" (ou termina em ":Bearer", como em
// "Authorization:Bearer"), sem diferença de maiúsculas: o token vem depois.
func isBearer(w string) bool {
	w = strings.ToLower(w)
	return w == "bearer" || strings.HasSuffix(w, ":bearer")
}

// MaskCmd esconde valores sensíveis do argv e limita o texto a 160 caracteres.
// Cada elemento passa primeiro pela troca da senha em URLs (scheme://u:senha@),
// antes de qualquer divisão, para que & ; | na senha não a partam. Um elemento
// nome=valor sensível tem o valor inteiro trocado; os demais com espaço (ex.:
// sh -c "...") são analisados como texto de shell, e só o valor secreto é
// trocado, nada mais é descartado além do corte final.
func MaskCmd(argv []string) string {
	var m masker
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if m.maskNext && !strings.HasPrefix(a, "-") {
			m.maskNext = false
			out = append(out, secret(a))
			continue
		}
		a = scrubUserinfo(a)
		if s, ok := m.assign(a); ok {
			out = append(out, s)
			continue
		}
		out = append(out, m.maskText(a))
	}
	s := strings.Join(out, " ")
	if r := []rune(s); len(r) > maxCmd {
		s = string(r[:maxCmd-1]) + "…"
	}
	return s
}
