package domains

import (
	"context"
	"net"
	"testing"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeResolver devolve ips ou err para qualquer host.
type fakeResolver struct {
	ips []net.IP
	err error
}

func (f fakeResolver) LookupIP(_ context.Context, _ string) ([]net.IP, error) {
	return f.ips, f.err
}

func newTestService(t *testing.T) (*Service, *proxytest.Fake) {
	t.Helper()
	fake := &proxytest.Fake{}
	s := &Service{
		DB:        store.OpenMemory(t),
		Proxy:     fake,
		Hub:       events.NewHub(),
		Resolver:  fakeResolver{ips: []net.IP{net.ParseIP("203.0.113.10")}},
		HostIPs:   func() ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil },
		PanelPort: 8000,
	}
	return s, fake
}

// mkApp cria um projeto e uma app com a porta informada.
func mkApp(t *testing.T, db *store.DB, slug string, hostPort int) store.App {
	t.Helper()
	ctx := context.Background()
	p := &store.Project{Name: "proj-" + slug}
	require.NoError(t, db.InsertProject(ctx, p))
	a := &store.App{ProjectID: p.ID, Slug: slug, SourceType: "image", Image: "nginx:1-alpine",
		BuildType: "image", InternalPort: 80, HealthTimeoutS: 60, HostPort: hostPort}
	require.NoError(t, db.InsertApp(ctx, a))
	return *a
}

func ptr[T any](v T) *T { return &v }
