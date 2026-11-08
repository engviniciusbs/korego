package domains

import (
	"context"
	"fmt"
	"net"
)

// Resolver resolve um hostname nos seus IPs.
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

// netResolver usa net.DefaultResolver com a rede "ip" (A e AAAA).
type netResolver struct{}

func (netResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// InterfaceIPs devolve os endereços global unicast das interfaces da máquina.
func InterfaceIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("listar endereços das interfaces: %w", err)
	}
	var out []net.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if ok && ipn.IP.IsGlobalUnicast() {
			out = append(out, ipn.IP)
		}
	}
	return out, nil
}

// DNSCheck é o resultado da verificação de DNS de um hostname.
type DNSCheck struct {
	OK       bool     `json:"ok"`
	Resolved []string `json:"resolved"`
	Expected []string `json:"expected"`
	Message  string   `json:"message,omitempty"`
}

// expectedIPs junta HostIPs e PublicIP, sem repetição, na ordem.
func (s *Service) expectedIPs() []string {
	hostIPs := s.HostIPs
	if hostIPs == nil {
		hostIPs = InterfaceIPs
	}
	ips, _ := hostIPs() // sem IPs das interfaces, sobra o público
	out := []string{}
	seen := map[string]bool{}
	add := func(ip net.IP) {
		if ip == nil {
			return
		}
		k := ip.String()
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, ip := range ips {
		add(ip)
	}
	add(net.ParseIP(s.PublicIP))
	return out
}

// suggestedIP escolhe o IP do registro A sugerido: o público, se houver,
// senão o primeiro IPv4 esperado, senão o primeiro esperado.
func (s *Service) suggestedIP(expected []string) string {
	if ip := net.ParseIP(s.PublicIP); ip != nil {
		return ip.String()
	}
	for _, e := range expected {
		if ip := net.ParseIP(e); ip != nil && ip.To4() != nil {
			return e
		}
	}
	if len(expected) > 0 {
		return expected[0]
	}
	return "o IP deste servidor"
}

// CheckDNS resolve host e informa se algum IP aponta para esta máquina.
func (s *Service) CheckDNS(ctx context.Context, host string) DNSCheck {
	expected := s.expectedIPs()
	c := DNSCheck{Resolved: []string{}, Expected: expected}

	r := s.Resolver
	if r == nil {
		r = netResolver{}
	}
	ips, err := r.LookupIP(ctx, host)
	if err != nil {
		c.Message = fmt.Sprintf("Não foi possível resolver %s.", host)
		return c
	}
	want := map[string]bool{}
	for _, e := range expected {
		want[e] = true
	}
	for _, ip := range ips {
		k := ip.String()
		c.Resolved = append(c.Resolved, k)
		if want[k] {
			c.OK = true
		}
	}
	if !c.OK {
		c.Message = fmt.Sprintf("Crie um registro A para %s apontando para %s.", host, s.suggestedIP(expected))
	}
	return c
}
