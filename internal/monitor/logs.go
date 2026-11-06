package monitor

import "github.com/engviniciusbs/korego/internal/node/docker"

const logBatchMax = 200

// logBatcher junta linhas de log em lotes de até logBatchMax e descarta as
// que já foram publicadas em um stream anterior (TS ≤ corte).
// Não é seguro para uso concorrente.
type logBatcher struct {
	after   string // corte: TS da última linha publicada antes do stream atual
	last    string // TS da última linha aceita
	pending []docker.LogLine
}

// newLogBatcher cria um lote que ignora linhas com TS ≤ after ("" = sem corte).
func newLogBatcher(after string) *logBatcher {
	return &logBatcher{after: after, last: after}
}

// add guarda l se ela for nova; devolve false quando l foi descartada.
// O corte vale só para o que veio antes do stream atual: linhas do mesmo
// stream com o mesmo TS são mantidas.
func (b *logBatcher) add(l docker.LogLine) bool {
	if b.after != "" && l.TS <= b.after {
		return false
	}
	b.pending = append(b.pending, l)
	if l.TS > b.last {
		b.last = l.TS
	}
	return true
}

// full informa se o lote chegou ao limite e deve ser publicado.
func (b *logBatcher) full() bool { return len(b.pending) >= logBatchMax }

// flush devolve as linhas pendentes (nil se não houver) e esvazia o lote.
func (b *logBatcher) flush() []docker.LogLine {
	out := b.pending
	b.pending = nil
	return out
}

// resume prepara um novo stream: o corte passa a ser o TS da última linha
// aceita, que é também o Since a pedir ao Docker.
func (b *logBatcher) resume() string {
	b.after = b.last
	return b.after
}
