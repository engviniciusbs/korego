package deploy

import (
	"context"
	"io"
	"sync"
)

// fakeRunner grava cada Cmd e devolve saídas/erros roteirizados por chamada.
type fakeRunner struct {
	mu      sync.Mutex
	cmds    []Cmd
	outputs []string // saída da n-ésima chamada
	errs    map[int]error
}

func (f *fakeRunner) Run(_ context.Context, c Cmd, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.cmds)
	f.cmds = append(f.cmds, c)
	if n < len(f.outputs) {
		_, _ = io.WriteString(out, f.outputs[n])
	}
	return f.errs[n]
}
