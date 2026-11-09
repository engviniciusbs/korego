package migrate

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
)

// calls registra a ordem das chamadas ao systemd, ao proxy e à verificação.
type calls struct {
	mu  sync.Mutex
	log []string
}

func (c *calls) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.log = append(c.log, s)
}

func (c *calls) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.log)
}

// fakeSystemd é um ServiceManager em memória: Stop desativa, Start ativa.
type fakeSystemd struct {
	c                                        *calls
	mu                                       sync.Mutex
	active                                   map[string]bool
	enabled                                  map[string]bool
	exists                                   map[string]bool
	StopErr, StartErr, EnableErr, DisableErr error
	// StartNoop: Start devolve nil mas a unit não fica ativa.
	StartNoop bool
	// PanicExists: Exists entra em pânico.
	PanicExists bool
	existsCalls int
	// onChange roda depois que Stop/Start mudam o estado (com o lock do fake: não chame o fake).
	onChange func(unit string, active bool)
}

func newFakeSystemd(c *calls) *fakeSystemd {
	return &fakeSystemd{c: c,
		active:  map[string]bool{"nginx": true},
		enabled: map[string]bool{"nginx": true},
		exists:  map[string]bool{"nginx": true, "apache2": true},
	}
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	f.c.add("stop " + unit)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StopErr != nil {
		return f.StopErr
	}
	f.active[unit] = false
	if f.onChange != nil {
		defer f.onChange(unit, false)
	}
	return nil
}

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	f.c.add("start " + unit)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StartErr != nil {
		return f.StartErr
	}
	if !f.StartNoop {
		f.active[unit] = true
		if f.onChange != nil {
			defer f.onChange(unit, true)
		}
	}
	return nil
}

func (f *fakeSystemd) Enable(_ context.Context, unit string) error {
	f.c.add("enable " + unit)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EnableErr != nil {
		return f.EnableErr
	}
	f.enabled[unit] = true
	return nil
}

func (f *fakeSystemd) Disable(_ context.Context, unit string) error {
	f.c.add("disable " + unit)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DisableErr != nil {
		return f.DisableErr
	}
	f.enabled[unit] = false
	return nil
}

func (f *fakeSystemd) Active(_ context.Context, unit string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active[unit], nil
}

func (f *fakeSystemd) Exists(_ context.Context, unit string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.PanicExists {
		panic("Exists quebrou")
	}
	f.existsCalls++
	return f.exists[unit]
}

func (f *fakeSystemd) UnitForProcess(p string) (string, bool) { return UnitForProcess(p) }

func (f *fakeSystemd) isActive(unit string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active[unit]
}

func (f *fakeSystemd) isEnabled(unit string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enabled[unit]
}

// fakeDomains implementa Domains com sites fixos e conta Pause/Resume.
type fakeDomains struct {
	sites   []proxy.Site
	mu      sync.Mutex
	status  proxy.Status
	paused  bool
	pauses  int
	resumed []proxy.Status
	// dns: host → aponta para cá (ausente = true).
	dns map[string]bool
	// skipped: domínios fora da config (SitesReport).
	skipped []proxy.SkippedSite
	// reconcile: o que Reconcile devolve (nil = status atual).
	reconcile func(ctx context.Context) proxy.Status
	// onResume roda depois de cada Resume (fora do lock).
	onResume func()
}

func (d *fakeDomains) DNSReady(_ context.Context, host string) (bool, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ok, set := d.dns[host]; set && !ok {
		return false, "Crie um registro A para " + host
	}
	return true, ""
}

func (d *fakeDomains) Reconcile(ctx context.Context) proxy.Status {
	if d.reconcile != nil {
		st := d.reconcile(ctx)
		d.mu.Lock()
		d.status = st
		d.mu.Unlock()
		return st
	}
	return d.Status()
}

func (d *fakeDomains) Sites(context.Context) ([]proxy.Site, error) { return slices.Clone(d.sites), nil }
func (d *fakeDomains) SitesReport(context.Context) ([]proxy.Site, []proxy.SkippedSite, error) {
	return slices.Clone(d.sites), slices.Clone(d.skipped), nil
}
func (d *fakeDomains) CFRanges() []string { return []string{"203.0.113.0/24"} }
func (d *fakeDomains) Status() proxy.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}
func (d *fakeDomains) Pause() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.paused {
		panic("Pause duplo")
	}
	d.paused = true
	d.pauses++
}
func (d *fakeDomains) Resume(st proxy.Status) {
	d.mu.Lock()
	if !d.paused {
		d.mu.Unlock()
		panic("Resume sem Pause")
	}
	d.paused = false
	d.resumed = append(d.resumed, st)
	if st.State != "" {
		d.status = st
	}
	hook := d.onResume
	d.mu.Unlock()
	if hook != nil {
		hook()
	}
}

// clock é um relógio falso: Sleep avança o tempo.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

func (c *clock) elapsed(since time.Time) time.Duration { return c.Now().Sub(since) }

type harness struct {
	e     *Engine
	c     *calls
	sd    *fakeSystemd
	px    *proxytest.Fake
	dom   *fakeDomains
	clk   *clock
	start time.Time
	db    *store.DB
	hub   *events.Hub
	// verify decide o resultado da verificação por host (nil = tudo ok).
	verify func(host string, addr string) SiteCheck
	// portsTaken: Switch(normal) falha (outro processo pegou 443).
	portsTaken bool
	mu         sync.Mutex
	mode       proxy.Listen
}

func site(host string, tls string) proxy.Site {
	return proxy.Site{Host: host, TLS: proxy.SiteTLS{Mode: tls},
		Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:20001"}}}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{c: &calls{}, mode: proxy.ListenTrial}
	h.sd = newFakeSystemd(h.c)
	h.clk = &clock{now: time.Unix(1_800_000_000, 0)}
	h.start = h.clk.Now()
	h.dom = &fakeDomains{
		sites:  []proxy.Site{site("exemplo.com", proxy.TLSCustom), site("loja.exemplo.net", proxy.TLSCustom)},
		status: proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "nginx (pid 7)"},
	}
	h.px = &proxytest.Fake{}
	h.px.EnsureFn = func(context.Context) proxy.Status {
		h.mu.Lock()
		defer h.mu.Unlock()
		// As portas são do nginx enquanto ele roda; senão do Caddy em modo normal.
		if h.sd.isActive("nginx") {
			return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "nginx (pid 7)"}
		}
		if h.mode == proxy.ListenNormal {
			return proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
		}
		// Portas livres em ensaio: o Caddy segue em ensaio (só Switch sai).
		return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Message: proxy.PortsFreeMessage}
	}
	h.px.SwitchFn = func(_ context.Context, _ []proxy.Site, mode proxy.Listen, _ []string) error {
		h.c.add("switch " + string(mode))
		h.mu.Lock()
		defer h.mu.Unlock()
		if mode == proxy.ListenNormal && (h.portsTaken || h.sd.isActive("nginx")) {
			return fmt.Errorf("listen tcp :443: bind: address already in use")
		}
		h.mode = mode
		return nil
	}
	h.px.ApplyFn = func(context.Context, []proxy.Site, proxy.Listen, []string) error {
		t.Error("o motor deve usar Switch, nunca Apply")
		return nil
	}
	h.db = store.OpenMemory(t)
	h.hub = events.NewHub()
	h.e = &Engine{
		Domains: h.dom, Proxy: h.px, Systemd: h.sd, DB: h.db, Hub: h.hub,
		Now: h.clk.Now, Sleep: h.clk.Sleep,
		Check: func(_ context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
			h.c.add("check " + addr)
			out := make([]SiteCheck, 0, len(sites))
			for _, s := range sites {
				if h.verify != nil {
					out = append(out, h.verify(s.Host, addr))
					continue
				}
				out = append(out, SiteCheck{Host: s.Host, OK: true})
			}
			return out
		},
	}
	return h
}

func (h *harness) setting(t *testing.T, key string) (string, bool) {
	t.Helper()
	v, ok, err := h.db.GetSetting(context.Background(), key)
	require.NoError(t, err)
	return v, ok
}

// subscribe coleta os eventos do tópico de migração.
func (h *harness) subscribe(t *testing.T) func() []Event {
	t.Helper()
	ch, cancel := h.hub.Subscribe(TopicMigration)
	t.Cleanup(cancel)
	return func() []Event {
		var out []Event
		for {
			select {
			case ev := <-ch:
				out = append(out, ev.Data.(Event))
			default:
				return out
			}
		}
	}
}
