# KoreGO: CPU and RAM usage by owner (design spec)

- **Date:** 2026-11-08
- **Status:** approved in conversation, awaiting review of this document
- **Base:** KoreGO v0.3.0 (stages 0–2 on `main`)

## 1. Goal

Today the panel shows **how much** CPU and RAM the server is using in total. This feature shows **who** is using them, so the user can act when something spikes:
- live, refreshed every 5 s;
- for any moment in the last 7 days ("what caused the CPU spike at 3 a.m.").

### Success criteria
1. On the "Consumo" page the user sees the server's consumers grouped by **owner**, with CPU and RAM, and can expand one to see its processes.
2. Clicking a point on the CPU or RAM history chart in the overview shows the top consumers at that moment.
3. On a VPS with Compose projects, PM2, nginx and Docker, every relevant process is attributed to the right owner, without SSH.

## 2. Owners and attribution

Each process is classified by reading `/proc/<pid>/cgroup`. The **first** rule that matches decides the owner:

| Order | Owner (`kind`) | Rule | Key (`key`) | Display name |
|---|---|---|---|---|
| 1 | `container` | cgroup contains a container ID: v2 `…/docker-<64hex>.scope`, v1 `…/docker/<64hex>` | the Compose project when one exists (`project:<name>`), otherwise `container:<name>` | project name, or "Avulso: <container name>"; `managed` badge when `korego.managed=true` |
| 2 | `pm2` | the PID, or the PID of an ancestor, equals an app's `pid` in `pm2 jlist` | `pm2:<app name>` | "PM2: <name>" |
| 3 | `service` | the cgroup's last component ends in `.service` | `service:<unit>` (e.g. `service:nginx`); `pm2-*.service` becomes `service:pm2-daemon` | unit name without `.service`; "PM2 (daemon)" for the daemon |
| 4 | `session` | cgroup under `user.slice` | `session` | "Sessões de usuário" |
| 5 | `kernel` | kernel thread (`/proc/<pid>/cmdline` empty and PPID 2, or PID 2) | `kernel` | "Kernel" |
| 6 | `other` | everything else | `other` | "Outros" |

- A container's processes are matched to the container list by ID, from the Docker list cache. When Docker is unavailable or the ID is unknown, the process falls into `other`.
- PM2 is checked by PID before the service rule, because the apps run inside `pm2-root.service`. The daemon process itself and anything not matched to an app stay under `service:pm2-daemon`.
- An owner's **CPU** is the sum of its processes' CPU, as a % of the whole machine (100% = every core).
- An owner's **RAM** is the sum of its processes' RSS. This is an approximation, since shared pages get counted more than once. The interface says so in a tooltip.
- On systems other than Linux (development), every process falls into `other`. No error is raised.

## 3. Collection

- **Reader** `internal/node/procs`. It doesn't import other internal packages, and it is exposed as `Node.Procs()`.
  - Every 5 s it reads every PID in `/proc`: `stat` (utime+stime, ppid, comm), `status` (VmRSS, Uid), `cmdline` and `cgroup`.
  - CPU % is the difference in process ticks between two readings, divided by the difference in total machine ticks (`/proc/stat`) and multiplied by 100. A process seen for the first time, or a reused PID (a different start time), shows 0 in that reading.
  - A process that vanishes between listing and reading is ignored.
- **Attribution** `internal/usage`:
  - It combines the reading with `docker.List` (cache refreshed every 30 s and whenever a container `create/start/destroy/rename` event arrives) and with the `pm2.Reader` Snapshot (which already has a 10 s cache).
  - It produces `[]Owner`, sorted by descending CPU.
- **Displayed command:**
  - the executable path plus arguments, truncated to 160 characters;
  - the value after any argument whose name contains `password`, `passwd`, `token`, `secret` or `key` (in the forms `--x=valor`, `--x valor`, `X=valor`) is replaced with `••••`;
  - the process's environment variables are never read.

## 4. Live view and history

- **Live:**
  - The collector runs all the time, even with no one viewing.
  - It keeps the last snapshot in memory and publishes it on the WebSocket topic `usage` every 5 s.
  - The payload has owners **without** processes, to keep the message small. Processes come over the API when a row is expanded.
- **History:**
  - At every minute boundary the collector computes each owner's **average** CPU and RAM over that minute.
  - It stores the **top 15 by CPU ∪ top 15 by RAM**, plus one "demais" row (`key = rest`) with the sum of the others, in a new table:

    ```sql
    CREATE TABLE usage_minute (
        ts INTEGER NOT NULL,          -- unix, início do minuto
        owner_key TEXT NOT NULL,
        kind TEXT NOT NULL,
        label TEXT NOT NULL,
        cpu_percent REAL NOT NULL,
        mem_bytes INTEGER NOT NULL,
        PRIMARY KEY (ts, owner_key)
    );
    ```

  - Retention is **7 days**. The cleanup runs in the same daily job as `metrics_minute`.
  - An owner that shows up partway through a minute is averaged over the samples in which it existed.

## 5. API (session required)

- `GET /api/usage` returns `{time, owners:[{key, kind, label, managed?, cpuPercent, memBytes, procCount, processes:[{pid, name, cmd, user, cpuPercent, memBytes}]}]}`. Processes are sorted by CPU, at most 50 per owner, and there is a `truncated` flag when there are more.
- `GET /api/usage/at?t=<unix>&range=1h|24h|7d&metric=cpu|mem` returns the top 10 owners by the chosen metric (`cpu` by default), using the average CPU and RAM in the bucket that contains `t`. Buckets are the same as the overview charts: 60 s, 300 s, 1800 s.
- `GET /api/usage/history?owner=<key>&range=1h|24h|7d` returns `{stepSeconds, points:[{t, cpuPercent, memBytes}]}` for one owner. When data is missing, the gap stays empty, as in the existing charts.
- Errors:
  - 400 `INVALID_RANGE`, `INVALID_OWNER`, `INVALID_TIME`, `INVALID_METRIC`;
  - 503 `USAGE_NOT_READY` until the first snapshot exists.

## 6. Screens

- **New "Consumo" page** (`/consumo/`), in the menu below "Visão geral":
  - A table of owners with: type icon, name, managed/external badge (containers), CPU with a bar, RAM with a bar, and number of processes.
  - It can be sorted by CPU (the default) or by RAM, and filtered by type. It updates live.
  - Expanding a row loads that owner's processes (PID, name, masked command, user, CPU, RAM) and shows a small CPU/RAM history chart for the owner, with 1h/24h/7d tabs.
  - Docker unavailable: containers show up under "Outros", and the page shows a small notice.
- **Overview:**
  - A "Maiores consumidores" panel with the top 5 by CPU, live, and a link to `/consumo/`.
  - Clicking a point on the CPU or RAM history chart opens a panel called **"Quem estava usando"** with the top 5 at that moment (`/api/usage/at` with `metric` matching the clicked chart).
- Same rules as the rest of the interface: dark only, no rounded corners, `frontend-design` before and `web-design-guidelines` at the end.

## 7. Tests

- **Unit (Go):**
  - the cgroup classifier, with real fixtures: v2 container, v1 container, `nginx.service`, `pm2-root.service`, `user.slice/session-3.scope`, kernel thread, and an empty or unknown cgroup;
  - PM2 matching by PID and by ancestor;
  - the CPU calculation over two readings, including a reused PID;
  - command masking (the three forms, plus truncation);
  - the history rules: average over the minute, top 15 ∪ top 15 plus "demais", retention;
  - the API: response formats, errors, and buckets matching the charts.
- **Integration** (`-tags integration`, Linux, CI): a test container with a Compose label shows up as an owner `project:<name>` with its process.
- **Frontend (Vitest):**
  - the table sorts and filters;
  - expanding a row loads processes;
  - "Quem estava usando" opens with the clicked point.
- **End to end:** the Consumo page shows the e2e container under its project, and expanding it shows the process.

## 8. Outside this scope

- Killing or restarting a process from the panel.
- Disk I/O and network per owner.
- Alerts ("notify when X goes above Y").
- Per-process history (only owners get history).
