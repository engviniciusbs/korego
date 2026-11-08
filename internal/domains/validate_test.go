package domains

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidHostname(t *testing.T) {
	for _, s := range []string{"exemplo.com", "app.exemplo.com", "a-b.exemplo.com.br", "app.korego.localhost"} {
		assert.True(t, ValidHostname(s), s)
	}
	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62) // 254
	assert.Len(t, long, 254)
	for _, s := range []string{"Exemplo.com", "exemplo", "-a.com", "a..com", long, "a_b.com", "", "a-.com", "203.0.113.10", "exemplo.com."} {
		assert.False(t, ValidHostname(s), s)
	}
	ok253 := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)
	assert.True(t, ValidHostname(ok253))
}

func TestValidAddress(t *testing.T) {
	for _, s := range []string{"127.0.0.1:5000", "localhost:3000", "[::1]:80", "api.interna:8080", "localhost:65535", "exemplo.com:1"} {
		assert.True(t, ValidAddress(s), s)
	}
	for _, s := range []string{"127.0.0.1", ":80", "x:0", "x:70000", "a b:1", "localhost:+80", "localhost:", "localhost:0", "localhost:70000", "Exemplo.com:80", "http://x:80"} {
		assert.False(t, ValidAddress(s), s)
	}
}
