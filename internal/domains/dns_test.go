package domains

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCheckDNS(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)

	c := s.CheckDNS(ctx, "exemplo.com")
	assert.True(t, c.OK)
	assert.Equal(t, []string{"203.0.113.10"}, c.Resolved)
	assert.Equal(t, []string{"203.0.113.10"}, c.Expected)
	assert.Empty(t, c.Message)

	s.Resolver = fakeResolver{ips: []net.IP{net.ParseIP("198.51.100.1")}}
	c = s.CheckDNS(ctx, "exemplo.com")
	assert.False(t, c.OK)
	assert.Equal(t, []string{"198.51.100.1"}, c.Resolved)
	assert.Equal(t, "Crie um registro A para exemplo.com apontando para 203.0.113.10.", c.Message)
}

func TestCheckDNS_PublicIPPreferredAndMatches(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	s.HostIPs = func() ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("10.0.0.5")}, nil
	}
	s.PublicIP = "203.0.113.20"
	s.Resolver = fakeResolver{ips: []net.IP{net.ParseIP("198.51.100.1")}}
	c := s.CheckDNS(ctx, "exemplo.com")
	assert.False(t, c.OK)
	assert.Equal(t, []string{"2001:db8::1", "10.0.0.5", "203.0.113.20"}, c.Expected)
	assert.Equal(t, "Crie um registro A para exemplo.com apontando para 203.0.113.20.", c.Message)

	// Sem IP público: o primeiro IPv4.
	s.PublicIP = ""
	c = s.CheckDNS(ctx, "exemplo.com")
	assert.Equal(t, "Crie um registro A para exemplo.com apontando para 10.0.0.5.", c.Message)

	// O IP público também conta como esperado.
	s.PublicIP = "203.0.113.20"
	s.Resolver = fakeResolver{ips: []net.IP{net.ParseIP("203.0.113.20")}}
	assert.True(t, s.CheckDNS(ctx, "exemplo.com").OK)
}

func TestCheckDNS_ResolverError(t *testing.T) {
	s, _ := newTestService(t)
	s.Resolver = fakeResolver{err: errors.New("no such host")}
	c := s.CheckDNS(context.Background(), "exemplo.com")
	assert.False(t, c.OK)
	assert.Empty(t, c.Resolved)
	assert.Equal(t, "Não foi possível resolver exemplo.com.", c.Message)
}

func TestCheckDNS_SuggestsFirstPublicIPv4(t *testing.T) {
	s, _ := newTestService(t)
	s.HostIPs = func() ([]net.IP, error) {
		return []net.IP{net.ParseIP("172.17.0.1"), net.ParseIP("10.0.0.5"), net.ParseIP("203.0.113.10")}, nil
	}
	s.Resolver = fakeResolver{ips: []net.IP{net.ParseIP("198.51.100.1")}}
	c := s.CheckDNS(context.Background(), "exemplo.com")
	assert.False(t, c.OK)
	assert.Equal(t, "Crie um registro A para exemplo.com apontando para 203.0.113.10.", c.Message)
}

// slowResolver só responde quando o contexto acaba.
type slowResolver struct{}

func (slowResolver) LookupIP(ctx context.Context, _ string) ([]net.IP, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCheckDNS_Timeout(t *testing.T) {
	old := dnsTimeout
	dnsTimeout = 20 * time.Millisecond
	t.Cleanup(func() { dnsTimeout = old })

	s, _ := newTestService(t)
	s.Resolver = slowResolver{}
	start := time.Now()
	c := s.CheckDNS(context.Background(), "exemplo.com")
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.False(t, c.OK)
	assert.Equal(t, "Não foi possível resolver exemplo.com.", c.Message)
}
