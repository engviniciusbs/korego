# KoreGO — Consumo por dono Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show who is using the server's CPU and RAM, grouped by owner (Compose project/container, PM2 app, system service, sessions, kernel, others). The view is live every 5 s, with a 7-day per-minute history and a "Quem estava usando" window on the overview charts.

**Architecture:**
- A new reader `node/procs` reads `/proc` every 5 s. On non-Linux it falls back to gopsutil.
- A pure package `usage` attributes each process to an owner via its cgroup, the container list and the PM2 PIDs, and masks secrets in commands.
- A collector in `usage` keeps the last snapshot, publishes the `usage` topic, and writes the minute averages (top 15 by CPU ∪ top 15 by RAM, plus "demais") to `usage_minute`.
- The API and two screens consume it: the new "Consumo" page and additions to the overview.

**Tech Stack:** Go 1.27, gopsutil v4 (non-Linux fallback only), modernc.org/sqlite, chi, coder/websocket · Next.js (static export), TypeScript, TanStack Query, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-08-korego-consumo-por-dono-design.md`

## Global Constraints

- All earlier stages' constraints still apply:
  - `CGO_ENABLED=0`;
  - Go tests pass on Linux **and** Windows (`t.Skip`/build tags for Linux-only code);
  - error format `{ "error": { "code", "message" } }`;
  - no shell commands;
  - dark-only UI with no rounded corners;
  - nothing sensitive versioned (examples use `exemplo.com`/`203.0.113.10`).
- Commits: one per task, prefix `feat(consumo): ...` (or `test`, `docs`), ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `internal/node` and its subpackages import no other internal package. `usage` may import `node/*`, `store`, `events`.
- Collection every **5 s**. Live payload (topic `usage`) carries owners **without** processes.
- History: per-minute average; stores **top 15 by CPU ∪ top 15 by RAM** plus one row `owner_key = "rest"` (label "Demais"); retention **7 days**, cleaned in the same daily job as `metrics_minute`.
- Owner precedence and keys, exactly as spec §2:
  - `project:<name>` / `container:<name>`
  - `pm2:<app>`
  - `service:<unit without .service>` (`pm2-*.service` → `service:pm2-daemon`)
  - `session`
  - `kernel`
  - `other`
- Owner CPU = sum of process CPU as % of the whole machine (100% = every core), clamped to 0–100. Owner RAM = sum of RSS.
- Command shown:
  - executable plus args, truncated to **160** chars;
  - the value after any arg whose name contains `password`, `passwd`, `token`, `secret` or `key` (forms `--x=v`, `--x v`, `X=v`, case-insensitive) becomes `••••`;
  - process environment is never read.
- `GET /api/usage` lists at most **50** processes per owner, sorted by CPU, with a `truncated` flag.
- Chart buckets for `/api/usage/at` and `/history`: 1h → 60 s, 24h → 300 s, 7d → 1800 s (same as the overview). `/at` returns the top **10**.
- Use `frontend-design` before UI work and `web-design-guidelines` at the end of each UI task.

## Decisions (where the spec leaves a choice)

- **C1 — Container cache refresh.** The container list cache refreshes every 30 s **and** right away when a process carries a container ID that isn't in the cache, rate-limited to once per 5 s. The spec also mentions refreshing on Docker events. Subscribing to the `containers` topic would start the on-demand events source just for this, so the unknown-ID trigger replaces it with the same result: a new container shows up within 5 s.
- **C2 — Non-Linux collection.** On non-Linux, `node/procs` collects via gopsutil, with an empty cgroup. So every process lands in `other`, keyed by name: `other` stays one owner, and its process list shows names.

## Review Focus

1. **Short-lived processes, PID reuse and a fresh start.**
   - A process seen for the first time, or a PID reused by a different process (a different start time), reports 0% CPU for that reading. It never reports a giant spike.
   - An owner's total never goes above 100%.
   - Test → Task 1, Task 2.
2. **Docker down or unknown container.** The processes go to `other` with no error. Collection and history keep working, and the page shows the "Docker indisponível" notice. Test → Task 2, Task 4.
3. **Secrets in command lines.** `--password=x`, `--db-password x`, `API_TOKEN=x` and `--secret-key=x` are masked, and the environment is never read. Test → Task 2.
4. **Clicking a chart point with no usage data** (KoreGO was off at that time, or the point is older than 7 days). This shows "Sem dados para este momento", not an error. Test → Task 5 (API returns an empty list), Task 7 (UI).
5. **A process-heavy server** (thousands of processes, very long cmdlines).
   - The WS payload stays owner-only.
   - `/api/usage` caps at 50 processes per owner and truncates commands.
   - The 5 s loop doesn't overlap itself: a slow reading skips the next tick, it doesn't queue.
   - Test → Task 4, Task 5.

---

## File structure

```
internal/node/procs/          procs.go (Proc, Source), local_linux.go, local_other.go, parse.go (stat/status/cgroup/cmdline parsers), testdata/proc/...
internal/node/node.go, local.go, nodetest/fake.go   Node gains Procs()
internal/node/pm2/            Process gains PID (parse pm_id's "pid")
internal/usage/               classify.go (owner rules), mask.go, aggregate.go (Owner, Aggregate), collector.go (loop, cache, minute rows)
internal/store/migrations/0004_usage.sql, internal/store/usage.go
internal/api/usage.go (+ server.go), cmd/korego/main.go
web/src/lib/usage.ts
web/src/app/(painel)/consumo/page.tsx, web/src/components/usage/owner-table.tsx, owner-row.tsx, who-was-using.tsx
web/src/components/charts/line-chart.tsx (onPointClick), web/src/app/(painel)/page.tsx, web/src/components/shell/sidebar.tsx
web/e2e/containers.spec.ts (or a new consumo step), .github/workflows/ci.yml, README.md
```

---

### Task 1: Process reader (`node/procs`)

**Files:**
- Create: `internal/node/procs/procs.go`, `parse.go`, `local_linux.go`, `local_other.go`, `testdata/proc/` (fixture tree), `parse_test.go`, `local_linux_test.go`, `local_test.go`
- Modify: `internal/node/node.go`, `internal/node/local.go`, `internal/node/nodetest/fake.go`

**Interfaces:**
- Produces:
  - ```go
    type Proc struct {
        PID, PPID   int
        Name        string   // comm
        Cmdline     []string // argv, raw (masking happens in usage)
        UID         int      // -1 when unknown
        User        string   // resolved name, "" when unknown
        Cgroup      string   // unified (v2) path, or the v1 path that has a docker id; "" off Linux
        CPUPercent  float64  // % of the whole machine since the previous reading, 0..100
        RSSBytes    uint64
        StartTicks  uint64   // stat field 22; identifies PID reuse
    }
    type Source interface{ Snapshot(ctx context.Context) ([]Proc, error) }
    func NewLocal() *Local
    ```
  - Pure parsers in `parse.go`, testable on every OS:
    - `parseStat(b []byte) (pid, ppid int, comm string, utime, stime, start uint64, err error)`: handles `comm` containing spaces and parentheses by using the last `)`.
    - `parseStatusRSSUID(b []byte) (rss uint64, uid int)`.
    - `parseCgroup(b []byte) string`: prefers the `0::` line. Otherwise it returns the first line whose path contains `docker`, otherwise the first path.
    - `parseCmdline(b []byte) []string`: NUL-separated, with the trailing NUL dropped.
    - `parseProcStatTotal(b []byte) uint64`: sum of the `cpu ` line.
  - Linux `Local` takes an injectable root (`/proc` by default) and keeps the previous `(StartTicks, utime+stime)` per PID plus the previous total ticks. CPU% is `Δproc / Δtotal × 100`, clamped to 0..100. A first-seen PID or a changed `StartTicks` gives 0. User names are resolved from `/etc/passwd` (injectable path) and cached. A process that vanishes mid-read is skipped.
  - Non-Linux `Local` (C2): gopsutil `process.Processes`, plus `Times`, `MemoryInfo`, `Name`, `Cmdline`. CPU uses the same delta logic with `cpu.Times` total. Cgroup is `""`.
  - `node.Node` gains `Procs() procs.Source`, and `nodetest.Fake` gains `ProcsSource procs.Source`.

- [ ] **Step 1: Tests**

```go
func TestParseStat_CommWithSpacesAndParens(t *testing.T) // "123 (my (weird) proc) S 1 ..." → pid 123, ppid 1, comm "my (weird) proc", utime/stime/start from the right fields
func TestParseCgroup_V2V1Empty(t *testing.T)            // "0::/system.slice/docker-<64hex>.scope" → that path; v1 file with "…:/docker/<64hex>" → that path; empty → ""
func TestParseCmdline(t *testing.T)                     // "nginx\x00-g\x00daemon off;\x00" → ["nginx","-g","daemon off;"]; "" → nil
func TestLocal_CPUDeltaAndReuse(t *testing.T)           // linux fixture root: 1st snapshot all 0%; 2nd with Δproc 50 of Δtotal 200 → 25%; same PID with a new StartTicks → 0%
func TestLocal_VanishedProcessSkipped(t *testing.T)     // pid dir without stat → skipped, no error
func TestLocal_Real(t *testing.T)                       // any OS: Snapshot twice → len > 0, a process with our own PID present, CPU in 0..100
```

- [ ] **Step 2: Run** `go test ./internal/node/procs/`: FAIL.
- [ ] **Step 3: Implement** as specified, with Linux fixture tests behind `//go:build linux`.
- [ ] **Step 4: Run** `go test ./... && go vet ./...`, `GOOS=linux go vet ./...`, and the Linux container run of `./internal/node/procs/`: PASS.
- [ ] **Step 5: Commit** `feat(consumo): leitura de processos com CPU por intervalo`

---

### Task 2: Owner attribution and command masking (`usage`)

**Files:**
- Create: `internal/usage/classify.go`, `mask.go`, `aggregate.go`, tests `classify_test.go`, `mask_test.go`, `aggregate_test.go`
- Modify: `internal/node/pm2/pm2.go`, `parse.go`, `parse_test.go`, `testdata/jlist.json` (add `pid`)

**Interfaces:**
- Consumes: `procs.Proc` (Task 1), `docker.Container` (`ID`, `Name`, `Project`, `Managed`), `pm2.Process` (gains `PID int \`json:"pid"\``, parsed from the jlist item's `pid`).
- Produces:
  - ```go
    type Kind string // "container" | "pm2" | "service" | "session" | "kernel" | "other"
    type Ref struct{ Key, Label string; Kind Kind; Managed *bool }
    type Lookup struct {
        Containers map[string]docker.Container // full 64-hex id → container
        PM2        map[int]string              // app pid → app name
        Parent     map[int]int                 // pid → ppid from this snapshot
    }
    func Classify(p procs.Proc, l Lookup) Ref
    func MaskCmd(argv []string) string // masked, joined with spaces, ≤160 chars (cut with "…")
    type ProcView struct{ PID int; Name, Cmd, User string; CPUPercent float64; MemBytes uint64 }
    type Owner struct {
        Ref
        CPUPercent float64; MemBytes uint64; ProcCount int
        Processes  []ProcView // sorted by CPU desc, max 50
        Truncated  bool
    }
    func Aggregate(ps []procs.Proc, l Lookup) []Owner // sorted by CPU desc, then MemBytes desc, then Key
    ```
  - Classification follows spec §2 order. The container ID regex is `docker-([0-9a-f]{64})\.scope` and `/docker/([0-9a-f]{64})`.
    - Known ID with a Compose project → `project:<project>`, label = project, Managed = any container in the project managed.
    - Known ID without a project → `container:<name>`, label "Avulso: <name>".
    - Unknown ID → `other`.
  - PM2: walk the PID's ancestors via `Parent` (max depth 32). If any of them is in `PM2`, the owner is `pm2:<app>`.
  - Service: the last cgroup segment ends with `.service`; `pm2-*.service` → `service:pm2-daemon`, label "PM2 (daemon)".
  - Session: path contains `/user.slice/`.
  - Kernel: PID 2, or PPID 2 with an empty Cmdline.
  - Owner CPU sum clamped to 100.
  - A JSON-ready `Owner` is defined in the API task (Task 5). `usage` types have no JSON tags.

- [ ] **Step 1: Tests**

```go
func TestClassify_Precedence(t *testing.T) // table: v2 container with project → project:blog (managed false); v1 container without project → container:web-1 "Avulso: web-1"; unknown id → other;
                                           // node child of a PM2 app pid inside pm2-root.service → pm2:bot-discord; the pm2 daemon itself → service:pm2-daemon;
                                           // nginx.service → service:nginx; user.slice/session-3.scope → session; kthread (ppid 2, empty cmdline) → kernel; "" cgroup → other
func TestMaskCmd(t *testing.T)             // ["app","--password=s3cr3t"] → "app --password=••••"; ["app","--db-password","s3cr3t"] → "app --db-password ••••";
                                           // ["env","API_TOKEN=abc"] → "env API_TOKEN=••••"; ["x","--secret-key=v"] → masked; ["x","--monkey=v"] NOT masked (only the listed names);
                                           // 400-char argv → len ≤ 160 ending with "…"
func TestAggregate_SumsSortsCaps(t *testing.T) // 60 procs in one owner → ProcCount 60, Processes 50, Truncated true; CPU sum clamped to 100; owners ordered by CPU then RAM
func TestParseJList_PID(t *testing.T)          // pm2 testdata gains "pid": 4242 → Process.PID 4242
```

Note on `TestMaskCmd`: "key" means a token in the arg name like `secret-key`, `api_key` or `apikey`, matched case-insensitively against the name part only. That makes `--monkey` a deliberate non-match. Implement it as: split the name into words on `-`/`_`, then match a word equal to one of `password`, `passwd`, `token`, `secret`, `key`, or a word ending in `key` or `token`, e.g. `apikey`. That rule is exactly what the test pins.

- [ ] **Step 2: Run** `go test ./internal/usage/ ./internal/node/pm2/`: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(consumo): dono de cada processo e mascaramento de comandos`

---

### Task 3: History table

**Files:**
- Create: `internal/store/migrations/0004_usage.sql`, `internal/store/usage.go`, `internal/store/usage_test.go`
- Modify: `internal/store/migrate_test.go` (version 4)

**Interfaces:**
- Produces:
  - The table exactly as in spec §4 (`usage_minute`, PK `(ts, owner_key)`), plus `CREATE INDEX idx_usage_minute_owner ON usage_minute(owner_key, ts);`
  - `store.UsageRow{TS int64; Key, Kind, Label string; CPUPercent float64; MemBytes uint64}`
  - `(*DB).InsertUsageMinute(ctx, rows []UsageRow) error`: one transaction, upsert on conflict.
  - `(*DB).UsageAt(ctx, from, to time.Time, step time.Duration, metric string, limit int) ([]UsageRow, error)`:
    - averages per owner over `[from, to)`, after rounding `from` up to the step like `MetricHistory`;
    - `metric` is `cpu` or `mem` and sets the ordering;
    - the `rest` row is included only if it is in the top `limit`.
  - `(*DB).UsageHistory(ctx, key string, from, to time.Time, step time.Duration) ([]UsageRow, error)`: buckets like `MetricHistory`; gaps stay missing.
  - `(*DB).DeleteUsageBefore(ctx, t) (int64, error)`.

- [ ] **Step 1: Tests**

```go
func TestUsage_UpsertAndAt(t *testing.T)      // two minutes, 3 owners → UsageAt over 2 minutes with step 60s gives per-owner averages, ordered by cpu; metric=mem changes order; limit respected
func TestUsage_AtEmptyWindow(t *testing.T)    // window with no rows → empty slice, no error
func TestUsage_HistoryBucketsAndGaps(t *testing.T)
func TestUsage_DeleteBefore(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/store/`: FAIL. **Step 3: Implement.** **Step 4: Run** it again: PASS.
- [ ] **Step 5: Commit** `feat(consumo): histórico por minuto de consumo por dono`

---

### Task 4: Usage collector

**Files:**
- Create: `internal/usage/collector.go`, `collector_test.go`
- Modify: `cmd/korego/main.go`, `internal/monitor/collector.go` (the daily cleanup also calls `DeleteUsageBefore`, or the usage collector runs its own daily cleanup; pick one, say which in the report, and make sure retention is 7 days)

**Interfaces:**
- Consumes: Task 1 `procs.Source`, Task 2 `Aggregate`/`Lookup`, Task 3 store, `docker.Client.List`, `pm2.Reader.Snapshot`, `events.Hub`.
- Produces:
  - ```go
    type Collector struct {
        Procs procs.Source; Docker docker.Client; PM2 pm2.Reader
        Hub *events.Hub; DB *store.DB
        Interval time.Duration // 0 = 5s
        Now func() time.Time
    }
    func (c *Collector) Run(ctx context.Context)
    func (c *Collector) Latest() (Snapshot, bool)
    type Snapshot struct{ Time time.Time; Owners []usage.Owner; DockerOK bool }
    ```
  - Loop behaviour:
    - Each tick takes one reading, refreshes the lookups (C1), aggregates, stores `Latest`, and publishes on topic `usage` the payload `{time, dockerOk, owners:[{key,kind,label,managed,cpuPercent,memBytes,procCount}]}` with no processes.
    - Ticks never overlap: if a reading is still running, the next tick is skipped.
    - Docker `List` errors set `DockerOK=false` and keep the last good cache for up to 5 minutes, then empty it.
    - PM2 errors leave an empty PM2 map.
  - Minute accumulation:
    - Each owner key gets sum/count, and is averaged over the samples it appeared in.
    - At the minute boundary it writes the top 15 by CPU ∪ top 15 by RAM, plus `rest` (sum of the remaining owners' averages, label "Demais", kind `other`).
    - On ctx cancel it flushes the partial minute, using a fresh short context.
  - `main` creates and runs it with `nd.Procs()`, `nd.Docker()`, `nd.PM2()`, the hub and the DB, and stops it before `db.Close`.

- [ ] **Step 1: Tests** (fake procs/docker/pm2, injected clock, 1 ms interval)

```go
func TestCollector_PublishesOwnersWithoutProcesses(t *testing.T)
func TestCollector_MinuteTopAndRest(t *testing.T)        // 20 owners in one minute → written rows = (top15 cpu ∪ top15 mem) + "rest", rest = sum of the others
func TestCollector_DockerDownGoesToOther(t *testing.T)    // List errors from the start → container procs counted under "other", DockerOK false, history still written
func TestCollector_UnknownContainerTriggersRefresh(t *testing.T) // proc with a new container id → List called again (≤ once per 5s)
func TestCollector_NoOverlap(t *testing.T)                // Snapshot blocks longer than the interval → only one in flight
func TestCollector_FlushOnStop(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/usage/`: FAIL. **Step 3: Implement** and wire `main`. **Step 4: Run** `go test ./... && go vet ./...`, plus the Linux `-race` container run of `./internal/usage/`: PASS.
- [ ] **Step 5: Commit** `feat(consumo): coleta a cada 5s, tópico usage e histórico`

---

### Task 5: Usage API

**Files:**
- Create: `internal/api/usage.go`, `usage_test.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`

**Interfaces:**
- Consumes: `Collector.Latest()`, `store.UsageAt`/`UsageHistory`.
- Produces (session required):
  - `GET /api/usage` → `{time, dockerOk, owners:[{key,kind,label,managed,cpuPercent,memBytes,procCount,truncated,processes:[{pid,name,cmd,user,cpuPercent,memBytes}]}]}`. No snapshot yet → 503 `USAGE_NOT_READY`.
  - `GET /api/usage/at?t=&range=&metric=` → `{t, stepSeconds, metric, owners:[{key,kind,label,cpuPercent,memBytes}]}`, max 10. The window is the chart bucket containing `t`: `from = t - t%step`, `to = from + step`. No rows gives `owners: []` with 200.
  - `GET /api/usage/history?owner=&range=` → `{stepSeconds, points:[{t,cpuPercent,memBytes}]}`, window `now - range .. now`.
  - Errors: 400 `INVALID_RANGE`, `INVALID_TIME` (missing, non-integer, or more than 1 minute in the future), `INVALID_METRIC`, `INVALID_OWNER` (empty or longer than 200 chars, or not matching `^(project|container|pm2|service):.+$|^(session|kernel|other|rest)$`); 503 `USAGE_NOT_READY`.
  - `Deps` gains `Usage interface{ Latest() (usage.Snapshot, bool) }`.

- [ ] **Step 1: Tests**

```go
func TestUsageAPI_Current(t *testing.T)        // fake snapshot → JSON fields; processes ≤ 50 and truncated flag; cmd already masked
func TestUsageAPI_NotReady(t *testing.T)
func TestUsageAPI_AtBucketAndEmpty(t *testing.T) // rows at 12:00..12:29 → t=12:17 range=7d returns the 12:00 bucket averages; t with no data → owners []
func TestUsageAPI_Validation(t *testing.T)     // each 400 code
func TestUsageAPI_History(t *testing.T)
```

- [ ] **Step 2–4:** FAIL → implement → `go test ./... && go vet ./...` PASS.
- [ ] **Step 5: Commit** `feat(consumo): API de consumo atual, por momento e histórico do dono`

---

### Task 6: "Consumo" page

**Files:**
- Create: `web/src/lib/usage.ts`, `web/src/app/(painel)/consumo/page.tsx`, `web/src/components/usage/owner-table.tsx`, `owner-row.tsx`, tests under `__tests__/`
- Modify: `web/src/components/shell/sidebar.tsx` (item "Consumo" `/consumo/` right below "Visão geral")

**Interfaces:**
- Consumes: the Task 5 JSON and the `usage` topic (Task 4 payload).
- Produces:
  - `usage.ts`:
    - types matching the API;
    - `useUsage()`: REST seed plus the `usage` topic merged into the query data. The topic has no processes, so keep the processes of expanded owners from the last REST fetch.
    - `useUsageProcesses(key, enabled)`: refetches `/api/usage` every 5 s while enabled and selects one owner.
    - `useUsageAt(t, range, metric)`, `useUsageHistory(key, range)`;
    - `kindLabel(kind)` and `kindIcon(kind)`.
  - The page, per spec §6:
    - The table is sortable by CPU (default) or RAM, with a type filter.
    - Bars are relative to 100% for CPU and to total RAM for memory. Total RAM comes from `useMetricsCurrent().memTotalBytes`.
    - A managed/external badge shows for containers.
    - A row expands to show its processes (masked cmd) plus a small `LineChart` with CPU/RAM history and the existing `Tabs` for 1h/24h/7d.
    - When `dockerOk` is false, show the notice "Docker indisponível: os containers aparecem em Outros."
    - The RAM column has the tooltip "Soma da memória residente dos processos; páginas compartilhadas podem ser contadas mais de uma vez."

- [ ] **Step 1: Tests** (Vitest)

```ts
test("ordena por CPU e alterna para RAM")
test("filtra por tipo")
test("expandir carrega os processos com comando mascarado e o gráfico do dono")
test("aviso de Docker indisponível")
```

- [ ] **Step 2–4:** FAIL → implement (skill `frontend-design` first) → `pnpm vitest run && pnpm lint && pnpm build` PASS. Review with `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(consumo): página Consumo com donos, processos e histórico`

---

### Task 7: Overview — top consumers and "Quem estava usando"

**Files:**
- Create: `web/src/components/usage/who-was-using.tsx`, test
- Modify: `web/src/components/charts/line-chart.tsx` (prop `onPointClick?: (t: number) => void`, fired on click/Enter at the hovered point, with a pointer cursor when set), `web/src/app/(painel)/page.tsx`, tests

**Interfaces:**
- Consumes: Task 6 hooks.
- Produces:
  - A "Maiores consumidores" panel with the top 5 by CPU from `useUsage()`, live, linking to `/consumo/`.
  - The CPU and RAM history charts on the overview pass `onPointClick`. It opens `WhoWasUsing` (a `Dialog` or popover) with `useUsageAt(t, range, metric)`: CPU chart → `cpu`, RAM chart → `mem`. It lists the top 5 with their values. Empty → "Sem dados para este momento." Loading and error states are handled.

- [ ] **Step 1: Tests**

```ts
test("LineChart chama onPointClick com o t do ponto (clique e Enter)")
test("clicar no gráfico de CPU abre Quem estava usando ordenado por CPU")
test("sem dados mostra a mensagem")
test("painel Maiores consumidores mostra 5 e link para Consumo")
```

- [ ] **Step 2–4:** FAIL → implement → `pnpm vitest run && pnpm lint && pnpm build` PASS. Review with `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(consumo): maiores consumidores e Quem estava usando na visão geral`

---

### Task 8: Integration, e2e and README

**Files:**
- Create: `internal/usage/collector_integration_test.go` (`//go:build integration && linux`)
- Modify: `web/e2e/containers.spec.ts` (or a new `consumo` step in it), `.github/workflows/ci.yml` (integration job also runs `./internal/usage/...`), `README.md`

**Interfaces:**
- Produces:
  - Integration test:
    - real `procs.NewLocal()` + real Docker;
    - start `busybox sleep 300` with label `com.docker.compose.project=consumo-it`;
    - within 15 s, an owner `project:consumo-it` exists whose processes include `sleep`;
    - clean up the container afterwards.
  - e2e (inside the existing containers spec, which already runs a container labelled with Compose project `e2e`):
    - open `/consumo/`;
    - row "e2e" is visible within 20 s;
    - expanding it shows a process whose command contains `linha-`. That's the busybox loop the spec already uses; check its exact command.
  - README: a "Consumo por dono" section explaining what the page shows and the RSS caveat.

- [ ] **Step 1: Write** the integration test and the e2e step.
- [ ] **Step 2: Run locally** what is possible: Windows unit and e2e skip/pass; the Linux container for the integration test needs the Docker socket and `--pid host` to see container processes. Report what ran.
- [ ] **Step 3: Update** CI and the README.
- [ ] **Step 4: Verify** `go test ./...`, `pnpm vitest run`, then push the branch (the controller does this) and confirm CI is green with the new tests executed.
- [ ] **Step 5: Commit** `test(consumo): integração com Docker real e e2e da página Consumo`
