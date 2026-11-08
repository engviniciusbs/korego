package deploy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreePort_SkipsUsedAndBusy(t *testing.T) {
	var tried []string
	listen := func(addr string) error {
		tried = append(tried, addr)
		if addr == "127.0.0.1:20001" {
			return errors.New("ocupada")
		}
		return nil
	}
	p, err := FreePort(map[int]bool{20000: true}, listen)
	require.NoError(t, err)
	assert.Equal(t, 20002, p)
	assert.Equal(t, []string{"127.0.0.1:20001", "127.0.0.1:20002"}, tried)
}

func TestFreePort_Exhausted(t *testing.T) {
	_, err := FreePort(nil, func(string) error { return errors.New("ocupada") })
	assert.ErrorIs(t, err, ErrNoFreePort)
}

func TestTryListen_FreeThenBusy(t *testing.T) {
	p, err := FreePort(nil, TryListen)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, p, PortRangeStart)
	assert.LessOrEqual(t, p, PortRangeEnd)
}
