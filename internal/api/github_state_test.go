package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateStore_SingleUseTTLAndCap(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := newStateStore(func() time.Time { return now })

	a, err := s.issue(7)
	require.NoError(t, err)
	assert.Len(t, a, 64)
	b, err := s.issue(8)
	require.NoError(t, err)
	assert.NotEqual(t, a, b)

	uid, ok := s.take(a)
	assert.True(t, ok)
	assert.EqualValues(t, 7, uid)
	_, ok = s.take(a)
	assert.False(t, ok, "uso único")
	_, ok = s.take("")
	assert.False(t, ok)

	// Expirado: recusado e removido.
	now = now.Add(stateTTL)
	_, ok = s.take(b)
	assert.False(t, ok)

	// Inserir poda os expirados.
	c, _ := s.issue(1)
	now = now.Add(stateTTL + time.Second)
	_, _ = s.issue(1)
	assert.Equal(t, 1, s.len())
	_, ok = s.take(c)
	assert.False(t, ok)

	// Limite: o mais antigo sai.
	s = newStateStore(func() time.Time { return now })
	first, _ := s.issue(1)
	var last string
	for i := 0; i < maxStates; i++ {
		now = now.Add(time.Millisecond)
		last, _ = s.issue(int64(i))
	}
	assert.Equal(t, maxStates, s.len())
	_, ok = s.take(first)
	assert.False(t, ok)
	_, ok = s.take(last)
	assert.True(t, ok)
}

func TestDeliverySet_Evicts(t *testing.T) {
	d := newDeliverySet(3)
	for i := 0; i < 3; i++ {
		assert.False(t, d.seen(strconv.Itoa(i)))
	}
	assert.True(t, d.seen("0"))
	assert.False(t, d.seen("3")) // despeja o menos recente ("1")
	assert.False(t, d.seen("1"))
	d.forget("1")
	assert.False(t, d.seen("1"))
	assert.False(t, d.seen(""), "sem id não deduplica")
	assert.False(t, d.seen(""))
}
