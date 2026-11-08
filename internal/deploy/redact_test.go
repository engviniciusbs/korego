package deploy

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactor_SplitWritesAndToken(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"segredo123"})
	_, _ = r.Write([]byte("antes seg"))
	_, _ = r.Write([]byte("redo123 depois\n"))
	require.NoError(t, r.Close())
	assert.Equal(t, "antes •••• depois\n", buf.String())

	buf.Reset()
	r = NewRedactor(&buf, nil)
	_, _ = r.Write([]byte("clone https://x-access-token:abc@github.com/o/r\n"))
	require.NoError(t, r.Close())
	assert.Equal(t, "clone https://••••github.com/o/r\n", buf.String())
	assert.NotContains(t, buf.String(), "abc")
}

func TestRedactor_EverySplitPoint(t *testing.T) {
	secret := "s3cr3t-valor"
	text := "linha um " + secret + " linha dois\nfim " + secret
	for i := 0; i <= len(text); i++ {
		var buf bytes.Buffer
		r := NewRedactor(&buf, []string{secret, "ab"})
		_, _ = r.Write([]byte(text[:i]))
		_, _ = r.Write([]byte(text[i:]))
		require.NoError(t, r.Close())
		assert.NotContains(t, buf.String(), "s3cr3t", "split %d", i)
		assert.Equal(t, "linha um •••• linha dois\nfim ••••", buf.String(), "split %d", i)
	}
}

func TestRedactor_TokenPatternSplit(t *testing.T) {
	text := "url https://x-access-token:ghs_ABCDEF123456@github.com/o/r fim"
	for i := 0; i <= len(text); i++ {
		var buf bytes.Buffer
		r := NewRedactor(&buf, nil)
		_, _ = r.Write([]byte(text[:i]))
		_, _ = r.Write([]byte(text[i:]))
		require.NoError(t, r.Close())
		assert.NotContains(t, buf.String(), "ghs_", "split %d", i)
		assert.Equal(t, "url https://••••github.com/o/r fim", buf.String(), "split %d", i)
	}
}

func TestRedactor_ConcurrentAndLong(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"topsecret"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = r.Write([]byte(strings.Repeat("x", 100) + "\n"))
			}
		}()
	}
	wg.Wait()
	require.NoError(t, r.Close())
	assert.Equal(t, 8*50*101, buf.Len())
}
