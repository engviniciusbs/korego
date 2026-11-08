package deploy

import (
	"errors"
	"net"
	"strconv"
)

// Faixa de portas do host reservada às aplicações.
const (
	PortRangeStart = 20000
	PortRangeEnd   = 29999
)

// ErrNoFreePort indica que a faixa de portas está esgotada.
var ErrNoFreePort = errors.New("nenhuma porta livre na faixa 20000-29999")

// TryListen abre e fecha na hora um listener TCP em addr; nil significa livre.
func TryListen(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}

// FreePort devolve a primeira porta da faixa que não está em used e que
// listen("127.0.0.1:<porta>") consegue abrir.
func FreePort(used map[int]bool, listen func(addr string) error) (int, error) {
	for p := PortRangeStart; p <= PortRangeEnd; p++ {
		if used[p] {
			continue
		}
		if listen("127.0.0.1:"+strconv.Itoa(p)) == nil {
			return p, nil
		}
	}
	return 0, ErrNoFreePort
}
