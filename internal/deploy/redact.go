package deploy

import (
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	mask        = "••••"
	minSecret   = 4
	tokenPrefix = "x-access-token:"
	maxPending  = 1024 // limite de espera por um "x-access-token:..." sem fim
)

var tokenRe = regexp.MustCompile(`x-access-token:[^@\s]+@`)
var tokenOpenRe = regexp.MustCompile(`x-access-token:[^@\s]*$`)

// Redactor troca segredos por •••• antes de repassar a saída. Trabalha sobre
// bytes brutos: guarda o texto cru, acha todas as ocorrências (inclusive
// aninhadas ou sobrepostas), funde as faixas e só emite o que está antes de
// qualquer faixa ou possível prefixo parcial de segredo. Retém no máximo
// maxSecretLen-1 bytes entre Writes.
type Redactor struct {
	mu      sync.Mutex
	w       io.Writer
	secrets []string
	hold    int
	buf     string
}

type span struct{ s, e int }

// NewRedactor devolve um writer que mascara secrets (>= 4 caracteres) e o
// padrão x-access-token:<...>@. Chame Close ao fim para descarregar o resto.
func NewRedactor(w io.Writer, secrets []string) *Redactor {
	var ss []string
	maxLen := len(tokenPrefix)
	for _, s := range secrets {
		if len(s) >= minSecret {
			ss = append(ss, s)
			if len(s) > maxLen {
				maxLen = len(s)
			}
		}
	}
	return &Redactor{w: w, secrets: ss, hold: maxLen - 1}
}

// spans devolve as faixas a mascarar em r.buf, já fundidas. openToken inclui um
// "x-access-token:..." ainda sem "@" (sempre, se final; senão só dentro do limite).
func (r *Redactor) spans(final bool) []span {
	var sp []span
	for _, sec := range r.secrets {
		for off := 0; off < len(r.buf); {
			i := strings.Index(r.buf[off:], sec)
			if i < 0 {
				break
			}
			sp = append(sp, span{off + i, off + i + len(sec)})
			off += i + 1
		}
	}
	for _, m := range tokenRe.FindAllStringIndex(r.buf, -1) {
		sp = append(sp, span{m[0], m[1]})
	}
	if loc := tokenOpenRe.FindStringIndex(r.buf); loc != nil && (final || len(r.buf)-loc[0] <= maxPending) {
		sp = append(sp, span{loc[0], len(r.buf)})
	}
	sort.Slice(sp, func(i, j int) bool { return sp[i].s < sp[j].s })
	var merged []span
	for _, x := range sp {
		if n := len(merged); n > 0 && x.s <= merged[n-1].e {
			if x.e > merged[n-1].e {
				merged[n-1].e = x.e
			}
			continue
		}
		merged = append(merged, x)
	}
	return merged
}

// partialStart devolve o menor índice i tal que r.buf[i:] é prefixo próprio de
// algum segredo (ou de "x-access-token:"); len(r.buf) se não houver.
func (r *Redactor) partialStart() int {
	for i := max(0, len(r.buf)-r.hold); i < len(r.buf); i++ {
		tail := r.buf[i:]
		if strings.HasPrefix(tokenPrefix, tail) {
			return i
		}
		for _, sec := range r.secrets {
			if strings.HasPrefix(sec, tail) {
				return i
			}
		}
	}
	return len(r.buf)
}

// emit escreve r.buf[:cut] mascarado e mantém o resto cru. cut é recuado para
// não cortar uma faixa ao meio.
func (r *Redactor) emit(cut int, sp []span) error {
	for _, x := range sp {
		if x.s < cut && cut < x.e {
			cut = x.s
		}
	}
	var b strings.Builder
	pos := 0
	for _, x := range sp {
		if x.e > cut {
			break
		}
		b.WriteString(r.buf[pos:x.s])
		b.WriteString(mask)
		pos = x.e
	}
	b.WriteString(r.buf[pos:cut])
	r.buf = r.buf[cut:]
	if b.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(r.w, b.String())
	return err
}

// Write mascara e repassa tudo, exceto a cauda que ainda pode ser início de segredo.
func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf += string(p)
	if err := r.emit(max(0, len(r.buf)-r.hold), r.spans(false)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush emite o que já pode sair com segurança (faixas completas mascaradas),
// mas continua retendo um possível prefixo parcial de segredo no fim. Não fecha.
func (r *Redactor) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sp := r.spans(false)
	cut := r.partialStart()
	// um token aberto também segura a saída (já está em sp como faixa até o fim)
	return r.emit(cut, sp)
}

// Close descarrega tudo, mascarando também um token sem "@" final.
func (r *Redactor) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.emit(len(r.buf), r.spans(true))
}
