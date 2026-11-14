package oauth

import "net"

// LimitKey dá a chave do limite por IP: IPv4 como está; IPv6 reduzido ao
// prefixo /64, para que um cliente não fure o limite trocando de endereço.
func LimitKey(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if v4 := p.To4(); v4 != nil {
		return v4.String()
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
