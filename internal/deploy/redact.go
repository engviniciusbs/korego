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
var tokenOpenRe = regexp.MustCompile(`x-access-token:\S*$`)

// Redactor troca segredos por •••• antes de repassar a saída. Retém no máximo
// maxSecretLen-1 bytes entre Writes para pegar segredos partidos; Close descarrega.
type Redactor struct {
	mu      sync.Mutex
	w       io.Writer
	secrets []string
	hold    int
	buf     string
}

// NewRedactor devolve um writer que mascara secrets (>= 4 caracteres) e o
// padrão x-access-token:<...>@. Chame Close ao fim para descarregar o resto.
func NewRedactor(w io.Writer, secrets []string) io.WriteCloser {
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
	sort.Slice(ss, func(i, j int) bool { return len(ss[i]) > len(ss[j]) })
	return &Redactor{w: w, secrets: ss, hold: maxLen - 1}
}

func (r *Redactor) redact(s string) string {
	for _, sec := range r.secrets {
		s = strings.ReplaceAll(s, sec, mask)
	}
	return tokenRe.ReplaceAllString(s, mask)
}

// Write mascara e repassa tudo, exceto a cauda que ainda pode ser início de segredo.
func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.redact(r.buf + string(p))
	cut := len(s) - r.hold
	if cut < 0 {
		cut = 0
	}
	// "x-access-token:" ainda sem o "@" final: espera, dentro de um limite.
	if loc := tokenOpenRe.FindStringIndex(s); loc != nil && len(s)-loc[0] <= maxPending && loc[0] < cut {
		cut = loc[0]
	}
	r.buf = s[cut:]
	if cut == 0 {
		return len(p), nil
	}
	if _, err := io.WriteString(r.w, s[:cut]); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close descarrega o que restou, mascarando também um token sem "@" final.
func (r *Redactor) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := tokenOpenRe.ReplaceAllString(r.redact(r.buf), mask)
	r.buf = ""
	if s == "" {
		return nil
	}
	_, err := io.WriteString(r.w, s)
	return err
}
