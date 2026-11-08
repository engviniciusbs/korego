//go:build !linux

package proxy

import (
	"net"
	"strconv"
)

// PortOwner diz se a porta TCP está em uso. Fora de Linux não há como saber
// o processo: tenta escutar nela e, se falhar, devolve "outro processo".
func PortOwner(port int) (string, bool) {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return "outro processo", true
	}
	_ = l.Close()
	return "", false
}
