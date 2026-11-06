package monitor

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
)

func tsN(n int) string { return fmt.Sprintf("%019d", n) }

func TestLogBatcher_LimitAndOrder(t *testing.T) {
	b := newLogBatcher("")
	for i := 1; i <= logBatchMax-1; i++ {
		assert.True(t, b.add(docker.LogLine{TS: tsN(i), Text: fmt.Sprint(i)}))
		assert.False(t, b.full())
	}
	b.add(docker.LogLine{TS: tsN(logBatchMax)})
	assert.True(t, b.full())
	out := b.flush()
	require.Len(t, out, logBatchMax)
	assert.Equal(t, tsN(1), out[0].TS)
	assert.Equal(t, tsN(logBatchMax), out[logBatchMax-1].TS)
	assert.Empty(t, b.flush())
	assert.False(t, b.full())
}

func TestLogBatcher_SkipsAlreadyPublished(t *testing.T) {
	b := newLogBatcher(tsN(10))
	assert.False(t, b.add(docker.LogLine{TS: tsN(9)}))
	assert.False(t, b.add(docker.LogLine{TS: tsN(10)}))
	assert.True(t, b.add(docker.LogLine{TS: tsN(11)}))
	// Dentro do mesmo stream, linhas com o mesmo TS não são descartadas.
	assert.True(t, b.add(docker.LogLine{TS: tsN(11)}))
	require.Len(t, b.flush(), 2)

	// Novo stream: o corte passa a ser o TS da última linha aceita.
	assert.Equal(t, tsN(11), b.resume())
	assert.False(t, b.add(docker.LogLine{TS: tsN(11)}))
	assert.True(t, b.add(docker.LogLine{TS: tsN(12)}))
}

func TestLogBatcher_ResumeWithoutLinesKeepsCut(t *testing.T) {
	b := newLogBatcher(tsN(5))
	assert.Equal(t, tsN(5), b.resume())
	assert.Equal(t, tsN(5), b.resume())
}
