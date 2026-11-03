package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthURL(t *testing.T) {
	for in, want := range map[string]string{
		":8000":             "http://127.0.0.1:8000/api/health",
		"0.0.0.0:9000":      "http://127.0.0.1:9000/api/health",
		"[::]:9000":         "http://127.0.0.1:9000/api/health",
		"203.0.113.10:8000": "http://203.0.113.10:8000/api/health",
	} {
		got, err := healthURL(in)
		require.NoError(t, err)
		assert.Equal(t, want, got, in)
	}
	_, err := healthURL("sem-porta")
	assert.Error(t, err)
}
