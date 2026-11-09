package usage

import (
	"context"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/pm2"
	"github.com/engviniciusbs/korego/internal/node/procs"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	defaultInterval   = 5 * time.Second
	dockerRefresh     = 30 * time.Second // refresh periódico da lista de containers
	dockerMinRefresh  = 5 * time.Second  // intervalo mínimo entre listas (id desconhecido)
	dockerStaleAfter  = 5 * time.Minute  // por quanto tempo o último mapa bom vale com o Docker fora
	dockerListTimeout = 3 * time.Second
	pm2CacheTTL       = 60 * time.Second // por quanto tempo o mapa pid → app (ou o erro) vale
	flushTimeout      = 5 * time.Second
	topN              = 15

	// Topic é o tópico do hub com o consumo ao vivo.
	Topic = "usage"
)

// Snapshot é uma leitura completa: donos com processos.
type Snapshot struct {
	Time     time.Time
	Owners   []Owner
	DockerOK bool
}

// LiveOwner é um dono no tópico "usage", sem a lista de processos.
type LiveOwner struct {
	Key        string  `json:"key"`
	Kind       Kind    `json:"kind"`
	Label      string  `json:"label"`
	Managed    *bool   `json:"managed,omitempty"`
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
	ProcCount  int     `json:"procCount"`
}

// LivePayload é o que se publica no tópico "usage".
type LivePayload struct {
	Time     time.Time   `json:"time"`
	DockerOK bool        `json:"dockerOk"`
	Owners   []LiveOwner `json:"owners"`
}

// Collector lê os processos periodicamente, agrupa por dono, publica no hub
// (tópico "usage") e grava a média por minuto no banco.
type Collector struct {
	Procs    procs.Source
	Docker   docker.Client
	PM2      pm2.Reader
	Hub      *events.Hub
	DB       *store.DB
	Interval time.Duration    // 0 = 5s
	Now      func() time.Time // nil = time.Now

	mu     sync.RWMutex
	latest Snapshot
	has    bool
}

// Latest devolve uma cópia da última leitura. Seguro para uso concorrente.
func (c *Collector) Latest() (Snapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.latest
	s.Owners = cloneOwners(c.latest.Owners)
	return s, c.has
}

func cloneOwners(os []Owner) []Owner {
	if os == nil {
		return nil
	}
	out := make([]Owner, len(os))
	for i, o := range os {
		if o.Managed != nil {
			m := *o.Managed
			o.Managed = &m
		}
		o.Processes = append([]ProcView(nil), o.Processes...)
		out[i] = o
	}
	return out
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// loopState é o estado da goroutine de Run; não precisa de trava.
type loopState struct {
	containers map[string]docker.Container // id → container; nil = vazio
	tried      bool                        // já houve uma chamada a List
	triedAt    time.Time                   // última chamada a List
	okAt       time.Time                   // último List com sucesso
	dockerOK   bool                        // o último List deu certo

	pm2Map   map[int]string // último resultado do PM2 (nil com erro ou daemon parado)
	pm2Tried bool
	pm2At    time.Time // última chamada ao PM2

	// erros já registrados no log, para não repetir a cada leitura
	procsErr, dockerErr, pm2Err bool
}

// Run coleta até ctx terminar; ao sair grava o minuto parcial.
func (c *Collector) Run(ctx context.Context) {
	interval := c.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	st := &loopState{}

	var cur *minuteAcc
	// A primeira leitura não tem anterior, então a CPU de todos sai 0%: ela é
	// publicada e fica em Latest (a RAM aparece logo), mas não entra no minuto.
	skipFirst := true
	flush := func(fctx context.Context) {
		if cur == nil {
			return
		}
		if rows := cur.rows(); c.DB != nil && len(rows) > 0 {
			if err := c.DB.InsertUsageMinute(fctx, rows); err != nil {
				log.Printf("gravar consumo do minuto: %v", err)
			}
		}
		cur = nil
	}
	defer func() {
		// ctx já terminou: grava o minuto parcial com um contexto próprio.
		fctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		defer cancel()
		flush(fctx)
	}()

	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		if snap, ok := c.collect(ctx, st); ok && skipFirst {
			skipFirst = false
		} else if ok {
			m := snap.Time.Truncate(time.Minute)
			if cur != nil && !cur.minute.Equal(m) {
				flush(ctx)
			}
			if cur == nil {
				cur = &minuteAcc{minute: m, owners: map[string]*ownerSum{}}
			}
			cur.add(snap.Owners)
		}
		if ctx.Err() != nil {
			return
		}
		// A leitura roda nesta goroutine, então nunca há duas ao mesmo tempo.
		// Um tick que chegou enquanto ela rodava é descartado: leitura lenta
		// pula o próximo tick em vez de emendar outra.
		select {
		case <-tick.C:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// collect faz uma leitura: processos, lookups, agregação, Latest e publicação.
func (c *Collector) collect(ctx context.Context, st *loopState) (Snapshot, bool) {
	ps, err := c.Procs.Snapshot(ctx)
	if ctx.Err() != nil {
		return Snapshot{}, false
	}
	if err != nil {
		if !st.procsErr {
			log.Printf("ler processos: %v", err)
			st.procsErr = true
		}
		return Snapshot{}, false
	}
	st.procsErr = false

	now := c.now()
	l := Lookup{
		Containers: c.containers(ctx, st, ps, now),
		PM2:        c.pm2Apps(ctx, st, now),
	}
	if ctx.Err() != nil {
		return Snapshot{}, false
	}
	snap := Snapshot{Time: now, Owners: Aggregate(ps, l), DockerOK: st.dockerOK}

	c.mu.Lock()
	c.latest, c.has = snap, true
	c.mu.Unlock()
	if c.Hub != nil {
		c.Hub.Publish(Topic, livePayload(snap))
	}
	return snap, true
}

func livePayload(s Snapshot) LivePayload {
	p := LivePayload{Time: s.Time, DockerOK: s.DockerOK, Owners: make([]LiveOwner, 0, len(s.Owners))}
	for _, o := range s.Owners {
		p.Owners = append(p.Owners, LiveOwner{
			Key: o.Key, Kind: o.Kind, Label: o.Label, Managed: o.Managed,
			CPUPercent: o.CPUPercent, MemBytes: o.MemBytes, ProcCount: o.ProcCount,
		})
	}
	return p
}

// containers devolve o mapa de containers (decisão C1): lista de novo a cada
// 30 s, ou já quando um processo traz um id fora do mapa (no máximo a cada
// 5 s, e só se o último List deu certo: com o Docker fora ou travado, é uma
// tentativa a cada 30 s). Com erro no List, mantém o último mapa bom por até
// 5 min.
func (c *Collector) containers(ctx context.Context, st *loopState, ps []procs.Proc, now time.Time) map[string]docker.Container {
	since := now.Sub(st.triedAt)
	need := !st.tried || since >= dockerRefresh || since < 0 // since < 0: relógio voltou
	if !need && st.dockerOK && since >= dockerMinRefresh && hasUnknownContainer(ps, st.containers) {
		need = true
	}
	if need {
		c.refreshContainers(ctx, st)
	}
	if !st.dockerOK && st.containers != nil && now.Sub(st.okAt) > dockerStaleAfter {
		st.containers = nil
	}
	return st.containers
}

// refreshContainers chama List com limite de 3 s. triedAt (e okAt) são
// marcados quando a chamada volta, para que uma chamada lenta não encurte o
// intervalo até a próxima.
func (c *Collector) refreshContainers(ctx context.Context, st *loopState) {
	st.tried = true
	if c.Docker == nil {
		st.triedAt, st.dockerOK = c.now(), false
		return
	}
	lctx, cancel := context.WithTimeout(ctx, dockerListTimeout)
	list, err := c.Docker.List(lctx)
	cancel()
	now := c.now()
	st.triedAt = now
	if err != nil {
		st.dockerOK = false
		if !st.dockerErr && ctx.Err() == nil {
			log.Printf("consumo: listar containers: %v", err)
			st.dockerErr = true
		}
		return
	}
	m := make(map[string]docker.Container, len(list))
	for _, ct := range list {
		m[ct.ID] = ct
	}
	st.containers, st.okAt, st.dockerOK, st.dockerErr = m, now, true, false
}

func hasUnknownContainer(ps []procs.Proc, known map[string]docker.Container) bool {
	for _, p := range ps {
		if id := ContainerID(p.Cgroup); id != "" {
			if _, ok := known[id]; !ok {
				return true
			}
		}
	}
	return false
}

// pm2Apps devolve pid → app. Com erro ou daemon parado, mapa vazio. O
// resultado, inclusive o erro, fica em cache por 60 s: o cache do leitor do
// PM2 é de só 10 s, e sem isto o coletor rodaria "pm2 jlist" a cada 10 s
// para sempre. Um app novo no PM2 leva até 1 min para ganhar dono próprio.
func (c *Collector) pm2Apps(ctx context.Context, st *loopState, now time.Time) map[int]string {
	if c.PM2 == nil {
		return nil
	}
	if since := now.Sub(st.pm2At); st.pm2Tried && since >= 0 && since < pm2CacheTTL {
		return st.pm2Map
	}
	snap, err := c.PM2.Snapshot(ctx)
	if ctx.Err() != nil {
		// Cancelado no meio: não guarda nada, a próxima leitura tenta de novo.
		return st.pm2Map
	}
	st.pm2Tried, st.pm2At, st.pm2Map = true, now, nil
	if err != nil {
		if !st.pm2Err {
			log.Printf("consumo: ler PM2: %v", err)
			st.pm2Err = true
		}
		return nil
	}
	st.pm2Err = false
	if !snap.DaemonRunning {
		return nil
	}
	m := make(map[int]string, len(snap.Processes))
	for _, p := range snap.Processes {
		if p.PID > 0 {
			m[p.PID] = p.Name
		}
	}
	st.pm2Map = m
	return m
}

// ownerSum acumula um dono dentro do minuto.
type ownerSum struct {
	cpu, mem float64
	n        int
	kind     Kind
	label    string
}

// minuteAcc acumula as leituras de um minuto.
type minuteAcc struct {
	minute time.Time
	owners map[string]*ownerSum
}

func (a *minuteAcc) add(os []Owner) {
	for _, o := range os {
		s := a.owners[o.Key]
		if s == nil {
			s = &ownerSum{}
			a.owners[o.Key] = s
		}
		s.cpu += o.CPUPercent
		s.mem += float64(o.MemBytes)
		s.n++
		s.kind, s.label = o.Kind, o.Label
	}
}

type ownerAvg struct {
	key      string
	kind     Kind
	label    string
	cpu, mem float64
}

// rows devolve as linhas do minuto: a média de cada dono nas leituras em que
// apareceu, só para top 15 por CPU ∪ top 15 por RAM, mais "rest" com a soma
// das médias dos demais (só quando há algum).
func (a *minuteAcc) rows() []store.UsageRow {
	avgs := make([]ownerAvg, 0, len(a.owners))
	for k, s := range a.owners {
		if s.n == 0 {
			continue
		}
		n := float64(s.n)
		avgs = append(avgs, ownerAvg{key: k, kind: s.kind, label: s.label, cpu: s.cpu / n, mem: s.mem / n})
	}
	if len(avgs) == 0 {
		return nil
	}
	selected := make(map[string]bool, 2*topN)
	pick := func(val func(ownerAvg) float64) {
		sort.Slice(avgs, func(i, j int) bool {
			vi, vj := val(avgs[i]), val(avgs[j])
			if vi != vj {
				return vi > vj
			}
			return avgs[i].key < avgs[j].key
		})
		for i := 0; i < len(avgs) && i < topN; i++ {
			selected[avgs[i].key] = true
		}
	}
	pick(func(o ownerAvg) float64 { return o.cpu })
	pick(func(o ownerAvg) float64 { return o.mem })

	sort.Slice(avgs, func(i, j int) bool { return avgs[i].key < avgs[j].key })
	ts := a.minute.Unix()
	rows := make([]store.UsageRow, 0, len(selected)+1)
	var restCPU, restMem float64
	rest := 0
	for _, o := range avgs {
		if !selected[o.key] {
			restCPU += o.cpu
			restMem += o.mem
			rest++
			continue
		}
		rows = append(rows, store.UsageRow{
			TS: ts, Key: o.key, Kind: string(o.kind), Label: o.label,
			CPUPercent: o.cpu, MemBytes: uint64(math.Round(o.mem)),
		})
	}
	if rest > 0 {
		rows = append(rows, store.UsageRow{
			TS: ts, Key: "rest", Kind: string(KindOther), Label: "Demais",
			CPUPercent: restCPU, MemBytes: uint64(math.Round(restMem)),
		})
	}
	return rows
}
