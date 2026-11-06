# KoreGO — Etapa 1 (Observar) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mostrar no painel, sem SSH, as métricas do host ao vivo e com 7 dias de histórico, todos os containers do host (agrupados por projeto Compose, com detalhes, ações, logs e stats ao vivo) e os processos PM2 em modo somente leitura.

**Architecture:** A interface `node.Node` ganha `Metrics()`, `Docker()` e `PM2()`, cada uma num subpacote de `internal/node` sem dependências internas. Um novo pacote `internal/monitor` liga essas fontes ao hub de eventos e ao SQLite: coletor de métricas a cada 2s com média por minuto em `metrics_minute`, fontes sob demanda para eventos, logs e stats do Docker, e leitor PM2 com cache. A API REST só traduz requisições; o frontend usa REST para o estado inicial e o WebSocket da etapa 0 para o tempo real.

**Tech Stack:** Go 1.27, gopsutil v4, `github.com/moby/moby/client` v0.6.x + `github.com/moby/moby/api` v1.56.x (SDK oficial do Docker), chi v5, coder/websocket, modernc.org/sqlite · Next.js (export estático), TypeScript, Tailwind, TanStack Query, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-03-korego-v1-design.md` (seções 3, 5, 8, 9, 10). Base: o código da etapa 0 (`docs/superpowers/plans/2026-11-03-korego-etapa-0-fundacao.md`).

## Global Constraints

- Valem todas as restrições globais da etapa 0: módulo `github.com/engviniciusbs/korego`, `CGO_ENABLED=0`, testes Go passando em Linux **e** Windows, formato de erro `{ "error": { "code", "message" } }`, nenhum comando via shell (`exec.Command(nome, args...)`), interface só escura e sem cantos arredondados, nada sensível versionado.
- Commits: um por tarefa, prefixo `feat(etapa-1): ...` (ou `test(etapa-1)`, `ci(etapa-1)`, `docs(etapa-1)`), terminando com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `internal/node` e os subpacotes não importam nenhum outro pacote interno.
- Métricas: amostra a cada **2 s** no canal `metrics`; média por minuto em `metrics_minute`; retenção de **7 dias** com limpeza diária; gráficos de **1h, 24h e 7d**.
- Containers: lista **todos** (`all=true`), agrupada pelo label `com.docker.compose.project`. Sem esse label o grupo é **"Avulsos"**. Selo **gerenciado** quando `korego.managed=true`, senão **externo**.
- Ações: iniciar, parar, reiniciar e remover. Remover **só com o container parado**, também validado no servidor. Toda ação pede confirmação; nos externos aparece o aviso exato: **"Este container não foi criado pelo KoreGO"**.
- Variáveis de ambiente de containers: a API devolve **só os nomes**, nunca os valores. Os valores não saem do processo e não vão para logs.
- Logs: `tail 500` + `follow`, busca no texto, pausa e download. Stats (CPU e RAM) só sob demanda.
- Eventos Docker publicados em `containers`: `create`, `start`, `stop`, `die`, `destroy`, `rename`, `pause`, `unpause`, `health_status`.
- PM2: lista nome, status, CPU, RAM, reinícios e diretório via `pm2 jlist`, atualizada a cada **10 s**, sem ações.
- Docker fora do ar: métricas e login continuam funcionando; as telas de containers mostram **"Docker indisponível"** com o botão **"Tentar novamente"**.
- ID de container na API e nos tópicos: `^[a-f0-9]{12,64}$`; fora disso → 400 `INVALID_CONTAINER_ID` (ou erro `INVALID_CONTAINER_ID` publicado no tópico).
- Antes de desenhar as telas, usar a skill `frontend-design`; antes de qualquer gráfico, a skill `dataviz`; ao fim de cada tarefa de tela, revisar com `web-design-guidelines`.

## Review Focus

1. **Docker parado ou sem socket**, na inicialização ou com o painel aberto: o KoreGO sobe normalmente, `/api/metrics/*` e o login seguem respondendo, `/api/containers` responde 503 `DOCKER_UNAVAILABLE`, e a fonte de eventos reconecta sozinha quando o Docker volta → testes nas Tarefas 5 e 6.
2. **`pm2 jlist` com efeitos colaterais e segredos:** sem daemon ativo, o KoreGO não pode iniciar um (qualquer comando `pm2` faz isso); o `pm2_env` traz as variáveis de ambiente dos processos, que nunca chegam à API; a saída pode vir com linhas de aviso antes do JSON → testes na Tarefa 7.
3. **Contadores de rede e CPU:** na primeira amostra, ou se um contador zerar (interface recriada, reboot de container), a taxa é 0, nunca negativa nem astronômica → teste na Tarefa 1.
4. **Logs em movimento:** o container reinicia enquanto os logs estão abertos (o stream termina) e a leitura retoma sem duplicar linhas; ANSI, UTF-8 inválido e linhas gigantes não quebram a tela; a junção entre o `tail` da REST e o stream do WebSocket não duplica nem perde linhas → testes nas Tarefas 6 e 8.
5. **Buracos no histórico:** com o KoreGO desligado por uma hora, o gráfico mostra um buraco em vez de ligar os pontos; 7d nunca devolve mais que 336 pontos → testes nas Tarefas 2 e 8.

---

## Estrutura de arquivos

```
internal/node/node.go                 Node ganha Metrics(), Docker(), PM2()
internal/node/local.go                Local monta as três implementações
internal/node/nodetest/fake.go        Fake ganha os campos das três fontes
internal/node/metrics/                metrics.go (tipos, Source), local.go, rates.go
internal/node/docker/                 docker.go (tipos, Client, erros), local.go (SDK), cpu.go, local_integration_test.go
internal/node/docker/dockertest/      fake.go
internal/node/pm2/                    pm2.go (tipos, Reader), local.go, parse.go, testdata/
internal/store/migrations/0002_metrics.sql, internal/store/metrics.go
internal/monitor/                     collector.go, docker_sources.go, logs.go
internal/api/                         metrics.go, containers.go, pm2.go (+ server.go)
internal/config/config.go             pm2_path / KOREGO_PM2_PATH
cmd/korego/main.go                    liga coletor e fontes
web/src/lib/                          metrics.ts, containers.ts, pm2.ts, logs.ts, chart.ts, format.ts, ws.ts (useLiveStream)
web/src/components/ui/                dialog.tsx, tabs.tsx
web/src/components/charts/            line-chart.tsx, sparkline.tsx
web/src/components/containers/        container-table.tsx, container-actions.tsx, log-viewer.tsx, stats-panel.tsx
web/src/app/(painel)/                 page.tsx (visão geral), containers/page.tsx, containers/detalhe/page.tsx
web/e2e/containers.spec.ts, .github/workflows/ci.yml, README.md
```

---

### Task 1: Fonte de métricas do host

**Files:**
- Create: `internal/node/metrics/metrics.go`, `internal/node/metrics/local.go`, `internal/node/metrics/rates.go`
- Modify: `internal/node/node.go`, `internal/node/local.go`, `internal/node/nodetest/fake.go`
- Test: `internal/node/metrics/rates_test.go`, `internal/node/metrics/local_test.go`

**Interfaces:**
- Produces:
  - ```go
    type Disk struct {
        Mount      string `json:"mount"`
        FSType     string `json:"fstype"`
        UsedBytes  uint64 `json:"usedBytes"`
        TotalBytes uint64 `json:"totalBytes"`
    }
    type Sample struct {
        Time             time.Time `json:"time"`
        CPUPercent       float64   `json:"cpuPercent"`
        MemUsedBytes     uint64    `json:"memUsedBytes"`  // Total - Available
        MemTotalBytes    uint64    `json:"memTotalBytes"`
        Load1, Load5, Load15 float64 // json: load1, load5, load15
        NetRxBytesPerSec float64   `json:"netRxBytesPerSec"`
        NetTxBytesPerSec float64   `json:"netTxBytesPerSec"`
        Disks            []Disk    `json:"disks"`
    }
    type Source interface {
        Sample(ctx context.Context) (Sample, error)
    }
    ```
  - `metrics.NewLocal() *metrics.Local`: guarda os contadores anteriores de CPU (`cpu.TimesWithContext(ctx, false)`) e de rede (`net.IOCountersWithContext(ctx, true)`) e calcula taxas pela diferença. Load average com erro (Windows) vira zeros, sem falhar.
  - Funções puras em `rates.go`:
    - `cpuPercent(prev, cur cpu.TimesStat) float64`: tempo ocupado / tempo total no intervalo × 100, limitado a 0–100. Delta total ≤ 0 → 0.
    - `rate(prev, cur uint64, elapsed time.Duration) float64`: `cur < prev` ou `elapsed <= 0` → 0.
    - `includeInterface(name string) bool`: exclui `lo` e nomes com prefixo `docker`, `br-` e `veth`, que contariam o tráfego dos containers em dobro.
    - `filterDisks(parts []disk.PartitionStat) []disk.PartitionStat`: usa `disk.PartitionsWithContext(ctx, false)`, descarta `squashfs`, `overlay`, `tmpfs`, `devtmpfs` e pontos sob `/var/lib/docker` e `/snap`, e remove montagens repetidas do mesmo dispositivo, ficando com a de caminho mais curto.
  - `node.Node` passa a ter `Metrics() metrics.Source`. `nodetest.Fake` ganha `MetricsSource metrics.Source`, devolvida por `Metrics()`.

- [ ] **Step 1: Testes**

```go
func TestCPUPercent(t *testing.T)            // 50 de 100 ocupados → 50; delta zero → 0; nunca > 100
func TestRate_FirstAndReset(t *testing.T)    // rate(0, 1000, 2s) == 500; rate(5000, 100, 2s) == 0; rate(1, 2, 0) == 0
func TestIncludeInterface(t *testing.T)      // eth0, ens3 → true; lo, docker0, br-1a2b, veth9f → false
func TestFilterDisks(t *testing.T)           // "/" ext4 e "/boot" ficam; squashfs, overlay, /var/lib/docker/... saem; mesmo device em "/" e "/mnt/x" → só "/"
func TestLocal_Sample(t *testing.T)          // qualquer SO: MemTotalBytes > 0, len(Disks) >= 1; 2ª chamada após 50ms: CPU entre 0 e 100, taxas >= 0
```

- [ ] **Step 2: Rodar** `go test ./internal/node/...`: FAIL (pacote inexistente).
- [ ] **Step 3: Implementar** os tipos, `Local` e as funções puras; ligar `Metrics()` em `node.Local` e no `Fake`.
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): fonte de métricas do host com taxas de CPU e rede`

---

### Task 2: Histórico por minuto no SQLite

**Files:**
- Create: `internal/store/migrations/0002_metrics.sql`, `internal/store/metrics.go`
- Test: `internal/store/metrics_test.go`

**Interfaces:**
- Produces:
  - Tabela:
    ```sql
    CREATE TABLE metrics_minute (
        ts INTEGER PRIMARY KEY,          -- unix, início do minuto
        cpu_percent REAL NOT NULL,
        mem_used INTEGER NOT NULL, mem_total INTEGER NOT NULL,
        load1 REAL NOT NULL,
        net_rx REAL NOT NULL, net_tx REAL NOT NULL,   -- bytes/s
        disk_used INTEGER NOT NULL, disk_total INTEGER NOT NULL  -- soma das montagens
    );
    ```
  - `store.MetricMinute{TS int64; CPUPercent float64; MemUsed, MemTotal uint64; Load1, NetRx, NetTx float64; DiskUsed, DiskTotal uint64}`
  - `(*DB).UpsertMetricMinute(ctx, m MetricMinute) error`: `INSERT ... ON CONFLICT(ts) DO UPDATE`.
  - `(*DB).MetricHistory(ctx, from, to time.Time, step time.Duration) ([]MetricMinute, error)`: média por balde `ts - ts % step`, só os baldes com dados, em ordem crescente. O `TS` de cada ponto é o início do balde.
  - `(*DB).DeleteMetricsBefore(ctx, t time.Time) (int64, error)`
  - Passos usados pela API (Tarefa 3): 1h → 60 s, 24h → 300 s, 7d → 1800 s.

- [ ] **Step 1: Testes**

```go
func TestMetricMinute_UpsertReplaces(t *testing.T)   // mesmo ts duas vezes → 1 linha, valores da segunda
func TestMetricHistory_Buckets(t *testing.T)         // 10 minutos com cpu 0..9, step 5min → 2 pontos, cpu 2 e 7
func TestMetricHistory_KeepsGaps(t *testing.T)       // minutos 0–9 e 70–79, step 1min → 20 pontos, sem linhas inventadas entre 9 e 70
func TestMetricHistory_7dMaxPoints(t *testing.T)     // 7 dias completos (10080 linhas), step 30min → 336 pontos
func TestDeleteMetricsBefore(t *testing.T)           // apaga só ts < corte e devolve a contagem
```

- [ ] **Step 2: Rodar** `go test ./internal/store/`: FAIL.
- [ ] **Step 3: Implementar** a migração e os métodos.
- [ ] **Step 4: Rodar** `go test ./internal/store/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): histórico de métricas por minuto com retenção`

---

### Task 3: Coletor e API de métricas

**Files:**
- Create: `internal/monitor/collector.go`, `internal/api/metrics.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/monitor/collector_test.go`, `internal/api/metrics_test.go`

**Interfaces:**
- Consumes: `metrics.Source` (Tarefa 1), métodos de `store` (Tarefa 2), `events.Hub` (etapa 0).
- Produces:
  - ```go
    type Collector struct {
        Source   metrics.Source
        Hub      *events.Hub
        DB       *store.DB
        Interval time.Duration    // 0 = 2s
        Retention time.Duration   // 0 = 7 * 24h
        Now      func() time.Time // nil = time.Now
    }
    func (c *Collector) Run(ctx context.Context)       // bloqueia até ctx terminar
    func (c *Collector) Latest() (metrics.Sample, bool) // última amostra; seguro para uso concorrente
    ```
    A cada intervalo: lê a amostra (erro → registra no log e segue), publica em `metrics` (sempre; sem assinantes o `Publish` não faz nada) e acumula no minuto corrente (`Time.Truncate(time.Minute)`). Na virada do minuto grava a média do anterior com `UpsertMetricMinute`, e ao terminar `ctx` grava o minuto parcial. Disco do histórico: soma de `UsedBytes`/`TotalBytes` das montagens. Limpeza `DeleteMetricsBefore(now - Retention)` ao iniciar e a cada 24h.
  - `Deps` ganha `Metrics interface{ Latest() (metrics.Sample, bool) }` e `DB *store.DB`.
  - `GET /api/metrics/current` (sessão) → `Sample` em JSON. Sem amostra ainda → 503 `METRICS_NOT_READY`.
  - `GET /api/metrics/history?range=1h|24h|7d` (sessão) → `{"range":"1h","stepSeconds":60,"points":[{"t":<unix>,"cpuPercent","memUsedBytes","memTotalBytes","load1","netRxBytesPerSec","netTxBytesPerSec","diskUsedBytes","diskTotalBytes"}]}`. Outro valor → 400 `INVALID_RANGE`.
  - `main`: cria o `Collector` com `nd.Metrics()` e roda em goroutine com o contexto do servidor.

- [ ] **Step 1: Testes** (relógio e fonte falsos, intervalo de 1ms)

```go
func TestCollector_PublishesSamples(t *testing.T)     // assinante de "metrics" recebe Sample
func TestCollector_WritesMinuteAverage(t *testing.T)  // cpu 10 e 30 no minuto 0 e uma amostra no minuto 1 → linha do minuto 0 com cpu 20
func TestCollector_FlushesOnStop(t *testing.T)        // cancelar ctx grava o minuto parcial
func TestCollector_SourceErrorKeepsRunning(t *testing.T) // 1ª amostra falha, 2ª chega ao hub
func TestCollector_CleansOldRows(t *testing.T)        // linha de 8 dias atrás some ao iniciar
func TestMetricsCurrent_NotReady503(t *testing.T)
func TestMetricsHistory_Ranges(t *testing.T)          // 1h → stepSeconds 60; 24h → 300; 7d → 1800; "2h" → 400 INVALID_RANGE
```

- [ ] **Step 2: Rodar** `go test ./internal/monitor/ ./internal/api/`: FAIL.
- [ ] **Step 3: Implementar** o coletor, as rotas e a ligação no `main`.
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS. Teste manual: `go run ./cmd/korego` com `KOREGO_DATA_DIR`/`KOREGO_CONFIG_DIR` temporários, criar o admin e fazer `GET /api/metrics/current` com o cookie.
- [ ] **Step 5: Commit** `feat(etapa-1): coletor de métricas a cada 2s e API de histórico`

---

### Task 4: Cliente Docker

**Files:**
- Create: `internal/node/docker/docker.go`, `internal/node/docker/local.go`, `internal/node/docker/cpu.go`, `internal/node/docker/dockertest/fake.go`
- Modify: `internal/node/node.go`, `internal/node/local.go`, `internal/node/nodetest/fake.go`, `go.mod`
- Test: `internal/node/docker/cpu_test.go`, `internal/node/docker/convert_test.go`, `internal/node/docker/local_integration_test.go` (`//go:build integration`)

**Interfaces:**
- Produces:
  - ```go
    var (
        ErrUnavailable = errors.New("docker indisponível")
        ErrNotFound    = errors.New("container não encontrado")
        ErrConflict    = errors.New("conflito no docker")
    )
    type Port struct {
        IP          string `json:"ip"`
        PrivatePort uint16 `json:"privatePort"`
        PublicPort  uint16 `json:"publicPort"`
        Proto       string `json:"proto"`
    }
    type Container struct {
        ID, Name, Image   string           // json: id, name, image (Name sem a "/" inicial)
        State             string           // running, exited, paused, restarting, created, dead
        Status            string           // texto do Docker, ex.: "Up 3 hours"
        Health            string           // healthy, unhealthy, starting ou ""
        CreatedAt         time.Time
        Project, Service  string           // labels com.docker.compose.project/.service
        Managed           bool             // label korego.managed == "true"
        Ports             []Port
    }
    type Mount struct{ Type, Source, Destination string; ReadWrite bool }
    type Details struct {
        Container
        Command      string
        StartedAt    time.Time
        RestartCount int
        EnvNames     []string   // só os nomes, em ordem alfabética
        Networks     []string
        Mounts       []Mount
    }
    type LogLine struct {
        TS     string `json:"ts"`     // unix nanos com 19 dígitos, comparável como texto
        Stream string `json:"stream"` // stdout | stderr
        Text   string `json:"text"`   // UTF-8 válido, no máximo 16 KiB (o resto é cortado com "…")
    }
    type Stats struct {
        CPUPercent    float64 `json:"cpuPercent"`
        MemUsedBytes  uint64  `json:"memUsedBytes"`
        MemLimitBytes uint64  `json:"memLimitBytes"`
    }
    type Event struct {
        Action string `json:"action"` // start, stop, die, ...; health_status sem o sufixo ": healthy"
        ID     string `json:"id"`
        Name   string `json:"name"`
    }
    type LogOptions struct {
        Tail   int    // 0 = todas
        Since  string // TS da última linha já vista ("" = sem filtro)
        Follow bool
    }
    type Client interface {
        Ping(ctx context.Context) error
        List(ctx context.Context) ([]Container, error)                // all=true
        Inspect(ctx context.Context, id string) (Details, error)
        Start(ctx context.Context, id string) error
        Stop(ctx context.Context, id string) error                    // timeout padrão do container
        Restart(ctx context.Context, id string) error
        Remove(ctx context.Context, id string) error                  // sem force, sem apagar volumes
        Logs(ctx context.Context, id string, o LogOptions, fn func(LogLine)) error // bloqueia até o fim do stream ou de ctx
        Stats(ctx context.Context, id string, fn func(Stats)) error   // stream; bloqueia
        Events(ctx context.Context, fn func(Event)) error             // filtro type=container e as ações das Global Constraints; bloqueia
    }
    ```
    Campos JSON em camelCase: `id`, `name`, `image`, `state`, `status`, `health`, `createdAt`, `project`, `service`, `managed`, `ports`, `command`, `startedAt`, `restartCount`, `envNames`, `networks`, `mounts`.
  - `docker.NewLocal() *docker.Local`: `client.New(client.FromEnv, client.WithAPIVersionNegotiation())`, criado sem conectar, para o KoreGO subir mesmo com o Docker parado. Mapeamento de erros: falha de conexão → `ErrUnavailable`; not found → `ErrNotFound`; conflict → `ErrConflict`. Logs pedem `Timestamps: true`; sem TTY a saída é separada em stdout/stderr com `stdcopy`, com TTY tudo vai como `stdout`.
  - Funções puras: `cpuPercent(prev, cur container.StatsResponse) float64` (fórmula do `docker stats`: delta da CPU do container / delta da CPU do sistema × CPUs online × 100) e `toContainer(...)`/`toDetails(...)` (conversão dos tipos do SDK, testável sem Docker).
  - `node.Node` ganha `Docker() docker.Client`; `nodetest.Fake` ganha `DockerClient docker.Client`.
  - `dockertest.Fake`: um campo `func` por método (`ListFn`, `InspectFn`, …). Método sem função → `ErrUnavailable`.

- [ ] **Step 1: Testes**

```go
func TestCPUPercent_DockerFormula(t *testing.T)    // container +200, sistema +1000, 2 CPUs → 40; delta do sistema 0 → 0
func TestToContainer_LabelsAndName(t *testing.T)    // "/web-1" → "web-1"; label do compose → Project/Service; korego.managed=true → Managed
func TestToDetails_EnvNamesOnly(t *testing.T)       // Env ["B=2","A=segredo"] → EnvNames ["A","B"]; o texto "segredo" não aparece no JSON
func TestLogLine_InvalidUTF8AndLong(t *testing.T)   // bytes inválidos viram U+FFFD; linha de 20 KiB → 16 KiB + "…"
// integration (Docker real; o container é criado e removido no teste):
func TestLocal_Lifecycle(t *testing.T)              // cria busybox com label korego.managed=true; List mostra Managed; Stop → exited; Remove; Inspect → ErrNotFound
func TestLocal_LogsTailAndFollow(t *testing.T)      // container que imprime 1..1000: Tail 500 devolve 501..1000 e TS cresce
func TestLocal_UnavailableSocket(t *testing.T)      // DOCKER_HOST=unix:///nao/existe → Ping devolve ErrUnavailable
```

- [ ] **Step 2: Rodar** `go test ./internal/node/docker/...`: FAIL.
- [ ] **Step 3: Implementar.** `go get github.com/moby/moby/client@v0.6 github.com/moby/moby/api@v1.56`. Se a assinatura de algum método do SDK tiver mudado, adaptar a implementação sem mudar a interface `Client`.
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...` (Linux e Windows): PASS. Com Docker local: `go test -tags integration ./internal/node/docker/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): cliente Docker com lista, detalhes, ações, logs, stats e eventos`

---

### Task 5: API de containers

**Files:**
- Create: `internal/api/containers.go`
- Modify: `internal/api/server.go`
- Test: `internal/api/containers_test.go`

**Interfaces:**
- Consumes: `docker.Client` via `d.Node.Docker()`, `dockertest.Fake`, `svc.Record` (auditoria da etapa 0).
- Produces (todas exigem sessão; as que alteram estado exigem CSRF):
  - `GET /api/containers` → `{"containers":[Container...]}` em ordem de projeto e depois de nome.
  - `GET /api/containers/{id}` → `Details`.
  - `POST /api/containers/{id}/start|stop|restart` → 204.
  - `DELETE /api/containers/{id}` → 204. O handler chama `Inspect` antes e, se `State` for `running`, `restarting` ou `paused`, responde 409 `CONTAINER_RUNNING` sem chamar `Remove`. `ErrConflict` do Docker também vira 409 `CONTAINER_RUNNING`.
  - `GET /api/containers/{id}/logs?tail=N` → `{"lines":[LogLine...]}`. Padrão 500, máximo 5000; inválido → 400 `INVALID_TAIL`.
  - `GET /api/containers/{id}/logs/download` → `text/plain; charset=utf-8`, `Content-Disposition: attachment; filename="<nome>-logs.txt"`, com todas as linhas em streaming (`Tail: 0`), uma por linha no formato `<RFC3339Nano> <stream> <texto>`.
  - Erros: `ErrUnavailable` → 503 `DOCKER_UNAVAILABLE` "Não foi possível conectar ao Docker."; `ErrNotFound` → 404 `CONTAINER_NOT_FOUND`; ID fora de `^[a-f0-9]{12,64}$` → 400 `INVALID_CONTAINER_ID`.
  - Auditoria: ações `container.start`, `container.stop`, `container.restart` e `container.remove`, com `Target` = nome do container e `Metadata {"id","managed","ok"}`. Registra também as tentativas que falharam (`ok: false`).

- [ ] **Step 1: Testes** (com `dockertest.Fake`)

```go
func TestContainers_List(t *testing.T)
func TestContainers_DockerDown503(t *testing.T)          // ListFn → ErrUnavailable → 503 DOCKER_UNAVAILABLE; GET /api/metrics/current continua 200
func TestContainers_InvalidID400(t *testing.T)           // "../etc", "XYZ", 11 caracteres → 400
func TestContainers_NotFound404(t *testing.T)
func TestContainers_ActionsRequireCSRF(t *testing.T)     // POST sem X-CSRF-Token → 403
func TestContainers_RemoveRunning409(t *testing.T)       // State running → 409 e RemoveFn não é chamado
func TestContainers_ActionAudited(t *testing.T)          // stop → linha container.stop com target = nome e ok = true
func TestContainers_DetailsHasNoEnvValues(t *testing.T)  // o corpo não contém o valor de nenhuma variável
func TestContainers_LogsTailBounds(t *testing.T)         // tail=0, tail=9999, tail=abc → 400; sem tail → Tail 500 chega ao fake
func TestContainers_Download(t *testing.T)               // Content-Disposition com o nome; corpo com as linhas do fake
```

- [ ] **Step 2: Rodar** `go test ./internal/api/`: FAIL.
- [ ] **Step 3: Implementar** as rotas.
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): API de containers com ações auditadas e logs`

---

### Task 6: Tempo real do Docker (eventos, logs e stats)

**Files:**
- Create: `internal/monitor/docker_sources.go`, `internal/monitor/logs.go`
- Modify: `cmd/korego/main.go`
- Test: `internal/monitor/docker_sources_test.go`, `internal/monitor/logs_test.go`

**Interfaces:**
- Consumes: `docker.Client`, `events.SourceFunc` e `RegisterSource` (etapa 0).
- Produces (todas registradas no hub pelo `main`, ativas só enquanto houver assinante):
  - `monitor.ContainerEventsSource(c docker.Client, retry time.Duration) events.SourceFunc` (tópico `containers`, `retry` padrão 5 s): publica cada `docker.Event`. Quando `Events` falha ou termina, publica `{"action":"unavailable"}` e tenta de novo a cada `retry`; ao reconectar, publica `{"action":"reconnected"}`.
  - `monitor.ContainerLogsSource(c docker.Client, retry time.Duration) events.SourceFunc` (tópico `container-logs:<id>`, `retry` padrão 2 s): segue os logs a partir de agora (`Follow: true`, `Tail` 0, `Since` = horário de início no mesmo formato de 19 dígitos de `LogLine.TS`; a implementação do cliente converte para o formato do SDK). Junta as linhas e publica `{"lines":[LogLine...]}` a cada 100 ms ou a cada 200 linhas, o que vier antes. Quando o stream termina (container parou), publica `{"ended":true}` e tenta de novo a cada `retry` com `Since` = TS da última linha publicada, pulando linhas com TS ≤ esse valor. ID inválido → publica `{"error":"INVALID_CONTAINER_ID"}`; `ErrNotFound` → `{"error":"CONTAINER_NOT_FOUND"}`, e encerra nos dois casos.
  - `monitor.ContainerStatsSource(c docker.Client, retry time.Duration) events.SourceFunc` (tópico `container-stats:<id>`): publica cada `docker.Stats`. Fim do stream → `{"running":false}` e nova tentativa a cada `retry`.
  - `logBatcher` em `logs.go` (`add(LogLine)`, `flush() []LogLine`, limite de 200, filtro de TS ≤ último): é a parte que os testes exercitam sem tempo real.

- [ ] **Step 1: Testes** (com `dockertest.Fake` e `retry` de 1 ms)

```go
func TestEventsSource_PublishesAndReconnects(t *testing.T) // 1º Events devolve ErrUnavailable → "unavailable"; 2º emite start → "reconnected" e depois o evento
func TestLogsSource_BatchesLines(t *testing.T)            // 450 linhas de uma vez → lotes de no máximo 200, na ordem
func TestLogsSource_ResumesWithoutDuplicates(t *testing.T)// 1º stream: TS 1..3 e fim; 2º recebe Since = TS 3 e reenvia 3..5 → assinante vê 1..5 uma vez cada e um {"ended":true}
func TestLogsSource_InvalidAndMissing(t *testing.T)       // "container-logs:zz" → INVALID_CONTAINER_ID; ErrNotFound → CONTAINER_NOT_FOUND
func TestStatsSource_StoppedContainer(t *testing.T)       // stream termina → {"running":false}
func TestSources_StopWithLastSubscriber(t *testing.T)     // cancelar a assinatura cancela o ctx passado ao fake
```

- [ ] **Step 2: Rodar** `go test ./internal/monitor/`: FAIL.
- [ ] **Step 3: Implementar** e registrar no `main`: `hub.RegisterSource("containers", ...)`, `hub.RegisterSource("container-logs", ...)` e `hub.RegisterSource("container-stats", ...)`.
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): eventos, logs e stats do Docker ao vivo pelo WebSocket`

---

### Task 7: Leitor PM2 (somente leitura)

**Files:**
- Create: `internal/node/pm2/pm2.go`, `internal/node/pm2/local.go`, `internal/node/pm2/parse.go`, `internal/node/pm2/testdata/jlist.json`, `internal/node/pm2/testdata/jlist-with-warning.txt`, `internal/api/pm2.go`
- Modify: `internal/node/node.go`, `internal/node/local.go`, `internal/node/nodetest/fake.go`, `internal/config/config.go`, `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/node/pm2/parse_test.go`, `internal/node/pm2/local_test.go`, `internal/api/pm2_test.go`

**Interfaces:**
- Produces:
  - ```go
    type Process struct {
        Name          string  `json:"name"`
        PMID          int     `json:"pmId"`
        Status        string  `json:"status"`        // online, stopped, errored, ...
        CPUPercent    float64 `json:"cpuPercent"`
        MemoryBytes   uint64  `json:"memoryBytes"`
        Restarts      int     `json:"restarts"`
        Cwd           string  `json:"cwd"`
        UptimeSeconds int64   `json:"uptimeSeconds"` // 0 quando não está online
    }
    type Snapshot struct {
        Available     bool      `json:"available"`     // binário pm2 encontrado
        DaemonRunning bool      `json:"daemonRunning"`
        Processes     []Process `json:"processes"`
    }
    type Reader interface {
        Snapshot(ctx context.Context) (Snapshot, error)
    }
    ```
  - `parseJList(out []byte, now time.Time) ([]Process, error)`: ignora o que vier antes do primeiro `[` (avisos do PM2) e lê **só** `name`, `pm_id`, `monit.cpu`, `monit.memory`, `pm2_env.status`, `pm2_env.restart_time`, `pm2_env.pm_cwd` e `pm2_env.pm_uptime` (ms). O resto do `pm2_env`, incluindo as variáveis de ambiente, é descartado no parse, sem `map[string]any`.
  - `pm2.NewLocal(path string) *pm2.Local`: `path` vazio → `exec.LookPath("pm2")`; sem binário → `Snapshot{Available:false}`, sem erro. Antes de executar, confere se o daemon está ativo: lê `$PM2_HOME/pm2.pid` (padrão `$HOME/.pm2`) e verifica se o processo existe. Sem daemon → `{Available:true, DaemonRunning:false}`, **sem rodar `pm2`**. Execução: `exec.CommandContext(ctx, path, "jlist")` com timeout de 10 s e o diretório do binário no início do `PATH`, para achar o `node` em instalações via nvm. O resultado fica em cache por 10 s.
  - `node.Node` ganha `PM2() pm2.Reader`; `nodetest.Fake` ganha `PM2Reader pm2.Reader`.
  - `Config.PM2Path` (`toml:"pm2_path"`, env `KOREGO_PM2_PATH`).
  - `GET /api/pm2` (sessão) → `Snapshot`. Erro do leitor → 503 `PM2_UNAVAILABLE`.

- [ ] **Step 1: Testes**

```go
func TestParseJList(t *testing.T)               // testdata/jlist.json com 2 processos → campos corretos; uptime calculado a partir de now
func TestParseJList_WarningPrefix(t *testing.T) // linhas "[PM2] ..." antes do JSON → mesmo resultado
func TestParseJList_DropsEnv(t *testing.T)      // o JSON de Process serializado não contém "SEGREDO_TESTE", presente no pm2_env do testdata
func TestLocal_NoBinary(t *testing.T)           // path inexistente → Available false
func TestLocal_NoDaemonDoesNotExec(t *testing.T)// PM2_HOME temporário sem pm2.pid e binário falso que cria um arquivo-marcador → DaemonRunning false e o marcador não existe (t.Skip no Windows)
func TestLocal_CachesFor10s(t *testing.T)       // relógio falso: 2 chamadas em 5s → binário falso executado 1 vez (t.Skip no Windows)
func TestPM2API(t *testing.T)                   // Fake → 200 com o JSON; erro → 503 PM2_UNAVAILABLE
```

- [ ] **Step 2: Rodar** `go test ./internal/node/pm2/ ./internal/api/`: FAIL.
- [ ] **Step 3: Implementar** e ligar no `main` com `node.NewLocal` recebendo `cfg.PM2Path`.
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): leitura dos processos PM2 sem iniciar o daemon`

---

### Task 8: Camada de dados do frontend

**Files:**
- Create: `web/src/lib/metrics.ts`, `web/src/lib/containers.ts`, `web/src/lib/pm2.ts`, `web/src/lib/logs.ts`, `web/src/lib/chart.ts`
- Modify: `web/src/lib/ws.ts`, `web/src/lib/format.ts`
- Test: `web/src/lib/__tests__/containers.test.ts`, `logs.test.ts`, `chart.test.ts`, `format.test.ts`, `ws.test.ts`

**Interfaces:**
- Consumes: `api<T>()` e `getLiveSocket()` (etapa 0); JSON das Tarefas 3, 5, 6 e 7.
- Produces:
  - Tipos TypeScript espelhando `Sample`, `HistoryResponse`, `Container`, `ContainerDetails`, `LogLine`, `ContainerStats`, `PM2Snapshot`, com os mesmos nomes de campo do JSON.
  - Hooks (TanStack Query): `useMetricsCurrent()`, `useMetricsHistory(range: "1h" | "24h" | "7d")` (refetch a cada 60 s), `useContainers()`, `useContainer(id)`, `usePM2()` (refetch a cada 10 s), `useContainerAction()` (mutação: `start | stop | restart | remove`, invalida `["containers"]` ao terminar).
  - `useContainersLive()`: assina `containers` e invalida `["containers"]` no máximo 1 vez por segundo.
  - `ws.ts`: `useLiveStream(topic: string | null, onData: (d: unknown) => void)`, que entrega cada mensagem em vez de só a última (`null` não assina).
  - `containers.ts`: `groupContainers(list: Container[]): Array<{ project: string | null; label: string; containers: Container[] }>`. Projetos em ordem alfabética, "Avulsos" (`project: null`) por último, containers por nome. `canRemove(c)`: `true` só para `exited`, `created` e `dead`.
  - `logs.ts`: `class LogBuffer(max = 5000)` com `setTail(lines)`, `append(lines)` (descarta linhas com `ts` ≤ maior `ts` já visto, comparando como texto), `lines`, `filter(query)` (sem diferenciar maiúsculas) e `stripAnsi(text)`. Junção sem duplicar: a tela assina o tópico primeiro, guarda o que chegar, busca o `tail` pela REST, chama `setTail` e depois `append` com o que guardou.
  - `chart.ts`: `buildSegments(points: {t: number; v: number}[], stepSeconds: number): {t: number; v: number}[][]` corta a série onde a distância entre pontos passa de `2 × stepSeconds`; `niceMax(v: number): number` arredonda o teto do eixo para 1, 2 ou 5 × 10ⁿ.
  - `format.ts`: `formatBytes(n)` (B, KiB, MiB, GiB, TiB, 1 casa), `formatRate(bytesPerSec)` (`"1,5 MiB/s"`), `formatPercent(v)` (`"12,3%"`), números em `pt-BR`.

- [ ] **Step 1: Testes**

```ts
test("groupContainers: projetos em ordem e Avulsos por último")
test("canRemove: só exited, created e dead")
test("LogBuffer: append descarta ts repetido ou antigo")
test("LogBuffer: setTail + linhas guardadas do WS sem duplicar a junção")
test("LogBuffer: mantém no máximo 5000 linhas, descartando as mais antigas")
test("stripAnsi remove códigos de cor e mantém o texto")
test("buildSegments corta onde falta mais de 2 passos")
test("niceMax: 73 → 100, 0.4 → 0.5, 0 → 1")
test("formatRate e formatPercent em pt-BR")
test("useLiveStream entrega todas as mensagens, não só a última")
```

- [ ] **Step 2: Rodar** `cd web && pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `pnpm vitest run && pnpm lint`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-1): dados, tempo real e utilitários do frontend para observar`

---

### Task 9: Visão geral com métricas, histórico e PM2

**Files:**
- Create: `web/src/components/charts/line-chart.tsx`, `web/src/components/charts/sparkline.tsx`, `web/src/components/ui/tabs.tsx`
- Modify: `web/src/app/(painel)/page.tsx`, `web/src/components/ui/index.ts`
- Test: `web/src/app/__tests__/overview.test.tsx`, `web/src/components/charts/__tests__/line-chart.test.tsx`

**Interfaces:**
- Consumes: hooks e utilitários da Tarefa 8.
- Produces:
  - `LineChart({ series: Array<{ label: string; points: {t,v}[] }>, stepSeconds, format: (v) => string, yMax?: number })`: SVG próprio, sem biblioteca, com uma `<path>` por segmento de `buildSegments`, eixo Y com `niceMax`, linha guia e valor no hover, e `role="img"` com `aria-label` resumindo o último valor.
  - `Sparkline({ values: number[] })`: últimos 60 valores ao vivo (2 minutos).
  - `Tabs` acessível (`role="tablist"`, setas do teclado), usado no seletor 1h / 24h / 7d.
  - A visão geral mantém o painel "Servidor" e o uptime da etapa 0 e acrescenta: cartões ao vivo de CPU, RAM (usada/total), load (1/5/15) e rede (entrada/saída) com sparkline; disco por montagem com barra de uso; gráficos de histórico (CPU %, RAM, rede) com o seletor; resumo "Containers: N rodando de M", com link para `/containers/` e "Docker indisponível" quando a API devolve 503; e o bloco **"Processos PM2"** (tabela com nome, status, CPU, RAM, reinícios e diretório), oculto quando `available` é falso e com o aviso "O PM2 está instalado, mas o daemon não está ativo." quando `daemonRunning` é falso.

- [ ] **Step 1: Testes**

```ts
test("LineChart desenha um path por segmento quando há buraco")
test("visão geral mostra os valores ao vivo do canal metrics")
test("bloco PM2 some quando available é false e avisa quando o daemon está parado")
test("resumo de containers mostra Docker indisponível em 503")
```

- [ ] **Step 2: Rodar** `pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar**, com as skills `frontend-design` e `dataviz` antes do código (paleta das séries, densidade, estados vazios).
- [ ] **Step 4: Verificar** `pnpm vitest run && pnpm lint && pnpm build`: PASS. Depois `make web && go run ./cmd/korego` e conferir a tela: números mudando a cada 2 s e troca de período funcionando. Revisar com `web-design-guidelines` e corrigir os apontamentos.
- [ ] **Step 5: Commit** `feat(etapa-1): visão geral com métricas ao vivo, histórico e PM2`

---

### Task 10: Lista de containers com ações

**Files:**
- Create: `web/src/app/(painel)/containers/page.tsx`, `web/src/components/containers/container-table.tsx`, `web/src/components/containers/container-actions.tsx`, `web/src/components/ui/dialog.tsx`
- Modify: `web/src/components/shell/sidebar.tsx`, `web/src/components/ui/index.ts`
- Test: `web/src/app/__tests__/containers.test.tsx`, `web/src/components/ui/__tests__/dialog.test.tsx`

**Interfaces:**
- Consumes: `useContainers`, `useContainersLive`, `useContainerAction`, `groupContainers`, `canRemove` (Tarefa 8).
- Produces:
  - `Dialog({ open, title, children, confirmLabel, variant?: "danger", onConfirm, onCancel, loading })`: `<dialog>` nativo com `showModal()`, foco preso, Esc cancela e foco volta ao botão que abriu.
  - `ContainerActions({ container, onDone? })`: botões Iniciar (parado), Parar e Reiniciar (rodando), Remover (só quando `canRemove`; senão desabilitado com a dica "Pare o container para removê-lo"). Cada um abre o `Dialog`. Quando `managed` é falso, o diálogo mostra o aviso **"Este container não foi criado pelo KoreGO"**. Erros da API aparecem no próprio diálogo pela mensagem do `code`.
  - Página `/containers/`: um grupo por projeto (cabeçalho com nome e contagem) e "Avulsos" por último; colunas nome (link para `/containers/detalhe/?id=<id>`), selo gerenciado/externo, estado, health, imagem, portas (`ip:public→private/proto`) e ações; filtro por nome. Docker indisponível → `Alert` "Docker indisponível" com "Tentar novamente" (refetch).
  - Sidebar: item "Containers" habilitado com `href: "/containers/"`.

- [ ] **Step 1: Testes**

```ts
test("Dialog: Esc cancela e devolve o foco")
test("agrupa por projeto e mostra Avulsos")
test("externo mostra o aviso no diálogo; gerenciado não")
test("Remover desabilitado com o container rodando")
test("503 mostra Docker indisponível e Tentar novamente refaz a busca")
```

- [ ] **Step 2: Rodar** `pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar** (skill `frontend-design` antes).
- [ ] **Step 4: Verificar** `pnpm vitest run && pnpm lint && pnpm build`: PASS. Com Docker local, `docker run -d --name korego-teste busybox sleep 3600`; parar, iniciar e remover pela tela e ver a lista atualizar sozinha pelos eventos. Revisar com `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(etapa-1): lista de containers agrupada com ações e confirmação`

---

### Task 11: Detalhe do container, logs e stats

**Files:**
- Create: `web/src/app/(painel)/containers/detalhe/page.tsx`, `web/src/components/containers/log-viewer.tsx`, `web/src/components/containers/stats-panel.tsx`
- Test: `web/src/components/containers/__tests__/log-viewer.test.tsx`, `web/src/app/__tests__/container-detail.test.tsx`

**Interfaces:**
- Consumes: `useContainer`, `useLiveStream`, `LogBuffer`, `stripAnsi`, `ContainerActions`, `Tabs`, `Sparkline`.
- Produces:
  - Página `/containers/detalhe/?id=<id>` (query string, porque o export estático não gera rotas dinâmicas). O `id` vem de `useSearchParams` dentro de `<Suspense>`; ID inválido ou 404 → "Container não encontrado" com link para a lista. Abas: **Detalhes** (imagem, estado, health, portas, criação, início, reinícios, comando, redes, volumes e os nomes das variáveis com o valor mostrado como `••••••`), **Logs** e **Stats**. Ações no cabeçalho.
  - `LogViewer({ containerId, containerName })`: faz a junção descrita em `LogBuffer` (assina `container-logs:<id>`, busca `tail=500`, `setTail`, `append`). Tem campo de busca (filtra e destaca), **Pausar/Retomar** (pausado, guarda as linhas novas e mostra "N novas linhas"), **Baixar** (link para `/api/containers/{id}/logs/download`), rolagem automática só quando o usuário está no fim, `stderr` com cor própria e o aviso "Container parado; aguardando reinício…" ao receber `{"ended":true}`. Lista virtualizada não é necessária com o limite de 5000 linhas; usar `content-visibility: auto` por linha.
  - `StatsPanel({ containerId })`: assina `container-stats:<id>` só enquanto a aba está aberta. CPU % e RAM usada/limite com sparkline; `{"running":false}` → "Container parado".

- [ ] **Step 1: Testes**

```ts
test("LogViewer junta tail e stream sem duplicar")
test("LogViewer pausado não rola e conta as linhas novas")
test("LogViewer busca sem diferenciar maiúsculas")
test("LogViewer mostra aguardando reinício ao receber ended")
test("detalhe mostra nomes das variáveis e nunca valores")
test("aba Stats só assina o tópico enquanto está aberta")
```

- [ ] **Step 2: Rodar** `pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar** (skill `frontend-design` antes).
- [ ] **Step 4: Verificar** `pnpm vitest run && pnpm lint && pnpm build`: PASS. Com um container que imprime uma linha por segundo, conferir os logs ao vivo, a pausa, a busca, o download e um `docker restart` com a tela aberta. Revisar com `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(etapa-1): detalhe do container com logs ao vivo e stats`

---

### Task 12: Ponta a ponta, integração no CI e README

**Files:**
- Create: `web/e2e/containers.spec.ts`
- Modify: `.github/workflows/ci.yml`, `README.md`, `web/e2e/fundacao.spec.ts` (só se precisar extrair o setup para um helper)

**Interfaces:**
- Consumes: tudo acima.
- Produces:
  - `containers.spec.ts`: pula com `test.skip` quando `docker version` falha. Antes do teste roda `docker run -d --name korego-e2e --label com.docker.compose.project=e2e busybox sh -c 'i=0; while true; do i=$((i+1)); echo linha-$i; sleep 1; done'` e remove o container no `afterAll`. Fluxo: setup e login (helper compartilhado com `fundacao.spec.ts`) → visão geral mostra CPU com valor numérico e o seletor 7d → `/containers/` mostra o grupo `e2e` com `korego-e2e` marcado **externo** → detalhe → aba Logs mostra `linha-` e, em até 5 s, uma linha nova → Parar com o aviso "Este container não foi criado pelo KoreGO" → estado `exited` → Remover → some da lista → Segurança mostra `container.stop` e `container.remove` na auditoria.
  - CI: novo job `integration` (ubuntu, Docker do runner) com `go test -tags integration ./internal/node/docker/...` e `timeout-minutes: 15`. O job `e2e` passa a ter Docker disponível (já tem no ubuntu-latest) e roda os dois specs.
  - README: seção "O que o KoreGO mostra" (métricas, containers e PM2), estado atualizado para "etapa 1", e como rodar `go test -tags integration`.

- [ ] **Step 1: Escrever o teste e2e.**
- [ ] **Step 2: Rodar** `make web && make build && cd web && pnpm playwright test`: PASS, ou o spec de containers marcado como pulado sem Docker.
- [ ] **Step 3: Atualizar o CI e o README.**
- [ ] **Step 4: Verificar** `go test ./... && cd web && pnpm vitest run && pnpm playwright test`: PASS. Enviar o branch e conferir o CI verde, todos os jobs, com `gh run watch`.
- [ ] **Step 5: Commit** `test(etapa-1): fluxo ponta a ponta de observar e testes de integração do Docker`

---

## Depois desta etapa

- Publicar `v0.2.0` e atualizar a VPS pelo próprio painel (Configurações → Atualização), conferindo os projetos Compose existentes como grupos, os containers que o KoreGO não criou como **externos** e os processos do PM2 no bloco próprio (dados reais da VPS só em `docs/local/`).
- Escrever o plano da **Etapa 2 (Publicar)**.
