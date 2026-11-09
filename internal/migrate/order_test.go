package migrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O evento final (done) só sai depois de retomar os domínios e liberar a vaga:
// quem relê GET /api/proxy ao recebê-lo já vê migrating=false.
func TestFinalEventAfterRelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(h *harness)
		run  func(e *Engine) Result
	}{
		{"assumir ok", func(*harness) {}, func(e *Engine) Result { return e.Takeover(context.Background()) }},
		{"assumir desfeito", func(h *harness) { h.portsTaken = true }, func(e *Engine) Result { return e.Takeover(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.prep(h)
			ch, cancel := h.hub.Subscribe(TopicMigration)
			t.Cleanup(cancel)
			doneBeforeResume := false
			h.dom.onResume = func() {
				for {
					select {
					case ev := <-ch:
						if ev.Data.(Event).Done {
							doneBeforeResume = true
						}
					default:
						return
					}
				}
			}
			res := tc.run(h.e)
			require.NotEmpty(t, res.Outcome)
			assert.False(t, doneBeforeResume, "done publicado antes de retomar os domínios")
			assert.False(t, h.e.Running(), "vaga ainda tomada ao fim")

			// O done chega depois do Resume; ao observá-lo, Running já é false.
			sawDone := doneBeforeResume
			for !sawDone {
				ev := <-ch
				if e := ev.Data.(Event); e.Done {
					sawDone = true
					assert.False(t, h.e.Running())
				}
			}
		})
	}
}
