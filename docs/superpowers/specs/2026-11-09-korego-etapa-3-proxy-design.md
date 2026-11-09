# KoreGO Etapa 3: advanced proxy and nginx migration (design spec)

- **Date:** 2026-11-09
- **Status:** approved in conversation, awaiting review of this document
- **Base:** KoreGO v0.4.0 (stages 0–2 and "Consumo por dono" on `main`)

## 1. Goal

KoreGO's Caddy can only publish "one hostname → one upstream", so a VPS whose nginx serves real sites cannot move to KoreGO. This stage adds the proxy features those sites need and a guided, reversible migration, so that **every site moves from nginx to KoreGO's Caddy and keeps answering exactly as before**.

The reference VPS (concrete data in `docs/local/` only) runs three kinds of site behind Cloudflare:

| Example | What it needs |
|---|---|
| `exemplo.com` + `www` | `/api/*` → `127.0.0.1:8092`, `/` → `127.0.0.1:8091`; `www` → apex; only Cloudflare may connect; Cloudflare origin certificate; security headers and a CSP |
| `app.exemplo.com` | static SPA from `/var/www/app/current` (a symlink swapped on each deploy), immutable cache on `/assets/`, `index.html` never cached, gzip, CSP; only Cloudflare; origin certificate |
| `loja.exemplo.net` + `www` | proxy to `127.0.0.1:8081`, client IP taken from `CF-Connecting-IP`, 1 MB upload limit, no response buffering |

Plus the panel itself on a subdomain covered by the wildcard origin certificate.

### Success criteria
1. Each reference site can be modelled in the panel with no hidden config, and the Caddy JSON generated for it reproduces the nginx behaviour (status, redirects, headers, static files, client IP, access filter).
2. Before touching nginx, the user can run the full config in a **trial** and see per-domain results.
3. "Take over the ports" stops nginx and puts Caddy on 80/443 with at most a few seconds of connection errors, verifies every domain and **rolls back automatically** if anything fails.
4. "Return to nginx" undoes the switch in one action.
5. Domains created before this stage keep working unchanged.

## 2. Domain model

A domain gains routes, TLS mode and per-domain options. Existing fields (`hostname`, `wwwRedirect`, `forceHttps`, `isPanel`) stay.

### 2.1 Routes

- A domain has 1..20 **routes**, each `{path, target}`.
- `path` is `/` or a prefix ending in `/` (`/api/`), matched as `path*`. Matching is most-specific first: longer prefixes win, and `/` is always last. Duplicate paths are rejected.
- Every domain must have a `/` route.
- Route targets:
  - `app`: a KoreGO app (as today).
  - `address`: `host:port` (as today), with options:
    - `maxBodyBytes` (0 = unlimited): requests above it get 413;
    - `noBuffering`: flush responses immediately (`flush_interval: -1`).
  - `static`: a folder, with options:
    - `root`: absolute path that must resolve inside `/var/www`. Symlinks are allowed; the check is on the cleaned path, and Caddy itself only sees the read-only mount;
    - `spa`: unknown paths are served `/index.html`;
    - `immutablePrefix` (optional, e.g. `/assets/`): responses under it get `Cache-Control: public, max-age=31536000, immutable`.
    - With `spa` on, responses for `/index.html`, including the SPA fallback, get `Cache-Control: no-cache`.
- Migration of existing rows: each current domain becomes one `/` route with its current target. Its behaviour stays identical except for the two "always" rules in 2.3 (no `Server` header, `X-Real-IP` sent upstream); the golden files change only by those.

### 2.2 TLS

- `tls.mode`:
  - `auto`: Let's Encrypt via Caddy, as today.
  - `custom`: a certificate chain and key supplied by the user.
- **Custom certificates**:
  - Input is either pasted PEM (chain + key) or a path on the VPS. KoreGO runs as root and reads the file once at save time.
  - Validation:
    - the chain must parse;
    - the key must match the leaf;
    - the leaf must cover the hostname and, when a `www` redirect exists, the `www` name too;
    - expired certificates are rejected.
  - The PEM is stored **encrypted** with the master key (the same mechanism as app secrets) and sent to Caddy inline (`tls.certificates.load_pem`). Nothing is bind-mounted.
  - The API and UI never return the key. They show only issuer, names, `notAfter` and SHA-256 fingerprint.
  - The panel warns 30 days before `notAfter`.
  - A certificate can be shared by several domains; for example a wildcard is stored once and referenced by each domain. The table is `certificates`, and a domain holds `cert_id`.

### 2.3 Access and headers

- `cloudflareOnly`:
  - Connections whose **peer address** is not in Cloudflare's ranges get `403`, on HTTP and HTTPS.
  - The client IP is taken from `CF-Connecting-IP`, using Caddy server `trusted_proxies` with the Cloudflare ranges and `client_ip_headers`.
  - The ranges are embedded in the binary and refreshed daily from `https://www.cloudflare.com/ips-v4` and `ips-v6`. The refresh keeps the last good list on failure, and a parsed list must contain at least 10 IPv4 CIDRs or it is ignored. The list in use is stored in settings.
- `headers`:
  - A list of response header `{name, value}`, at most 30, applied to all routes of the domain. Names must be valid tokens; values must have no CR or LF.
  - The UI offers a "security" preset:
    - `Strict-Transport-Security: max-age=31536000`
    - `X-Content-Type-Options: nosniff`
    - `Referrer-Policy: strict-origin-when-cross-origin`
    - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
- `compression`: `encode zstd gzip`. On by default for new domains, off for migrated ones.
- Always:
  - the `Server` response header is removed;
  - upstreams receive `X-Real-IP: {client_ip}`, in addition to the default `X-Forwarded-*` headers.

### 2.4 Out of scope (YAGNI)

The following are left out:
- fixed-response and redirect as route targets;
- per-route headers beyond the static cache rules;
- configurable upstream timeouts (Caddy does not cut slow upstreams by default);
- importing nginx files automatically;
- arbitrary bind mounts outside `/var/www`.

## 3. Caddy

- **Container.** The `korego-caddy` container gains a read-only bind mount `/var/www:/var/www:ro`, and the folder is created if missing.
  - The proxy package owns a "spec version". When the running container's spec is older, KoreGO recreates it. The data and config volumes are kept, and the change is made only while Caddy is not on 80/443, or at a moment chosen by the user.
  - On the reference VPS this happens while nginx still owns the ports, so nothing goes down.
- **Config builder.** `BuildConfig` takes `[]Site`, where each site carries its hostnames, routes, TLS, access settings, headers and compression, plus a `Listen` mode. It emits:
  - the per-host route trees: path subroutes, `request_body`, `reverse_proxy` with `header_up`, `file_server` with `try_files` for SPA, and `headers`;
  - a `remote_ip` deny subroute for `cloudflareOnly` hosts, on both the HTTP and HTTPS servers;
  - inline certificates for `custom`, with automation policies only for `auto` hosts.

  `InSync` and the golden tests keep working with the new input.
- **Listen modes.**
  - `normal`: `:80` and `:443`.
  - `trial`: `127.0.0.1:18080` and `127.0.0.1:18443`. Automatic HTTPS cannot issue certificates in trial mode, so `auto` hosts show "certificate pending until takeover". `cloudflareOnly` also admits `127.0.0.1` and `::1`.
- **State.** When the ports are owned by another process, KoreGO now runs Caddy in `trial` mode instead of not running it. The new proxy state is `trial`, with the blocking process recorded as today.
  - The 30 s reconcile keeps the mode in sync. When the ports become free on their own, KoreGO switches to `normal` on the next tick, as today, but it never stops a foreign process by itself.

## 4. Migration

### 4.1 Trial check

`GET /api/proxy/trial-check` returns, for each domain:
- for each route path, an HTTP GET over TLS to `127.0.0.1:18443` with the right SNI and Host;
- the TLS result: handshake ok, leaf names, `notAfter`;
- status code and selected response headers;
- for static routes, whether `index.html` was served.

The panel shows this as a per-domain checklist. The check runs on demand and is not cached for more than 10 s.

### 4.2 Take over the ports

`POST /api/proxy/takeover` (fresh 2FA confirmation required) starts a job. Progress goes over the existing jobs topic, and every step is audited. Only `nginx.service` or `apache2.service` owning 80/443 is supported. Otherwise the job refuses and tells the user what to stop by hand.

1. **Preconditions:**
   - the state is `trial`;
   - every domain's trial check passes: no 5xx, the TLS handshake succeeds, or the domain is `auto`, in which case it is shown as "will issue after takeover";
   - the port owner is one of the supported units.
2. `systemctl stop <unit>`, then immediately apply the config in `normal` mode. Retry the bind for up to 5 s while the old process releases the sockets.
3. Verify every domain on `127.0.0.1:443`, the same check as in 4.1. `auto` hosts get up to 60 s to issue.
4. **On failure at step 2 or 3:**
   - apply `trial` mode again;
   - `systemctl start <unit>`;
   - confirm the unit is active;
   - report "voltou para o <unit>: <reason>".
5. **On success:** `systemctl disable <unit>`. Record `proxy.migrated_from = <unit>` and the time in settings.

### 4.3 Return to the previous server

`POST /api/proxy/rollback` (2FA confirmation) is available while `proxy.migrated_from` is set and the unit file still exists:
1. Apply `trial` mode.
2. `systemctl enable --now <unit>`.
3. Confirm the unit is active.

The job and audit work as in 4.2.

### 4.4 Operational runbook for the reference VPS (outside the product)

1. Back up `/etc/nginx`, `/etc/letsencrypt` and `/etc/ssl/cloudflare` into a dated tarball in `/root`.
2. Issue a Cloudflare origin certificate for the `loja` zone, apex and `www`, through the Cloudflare API. The zone is in SSL Full (strict), which accepts origin certificates.
3. Register the sites, the shared wildcard certificate and the per-site headers and CSP in KoreGO.
4. **Compare.** For each site and representative paths (home, an API path, an asset, an unknown SPA path), compare the public answer, which is nginx through Cloudflare, with the trial answer: status, headers ignoring `Date`, `Server` and Cloudflare-added headers, and a body hash.
   - Smoke-test the shop's login and API.
5. At a low-traffic hour, take over. Then repeat the public comparison.
6. **Panel.** Create the panel DNS record (proxied) through the Cloudflare API. Register the panel domain with the wildcard certificate and `cloudflareOnly`. Verify login, WebSocket and the GitHub webhook URL. Only then turn off the public port 8000.
7. Delete the retired API site from nginx and its certificate from certbot. Leave the rest of nginx and certbot untouched as the rollback path; removing them is the user's call later.

## 5. API

All endpoints require a session, and all changes are audited.

- `POST /api/domains`, `PUT /api/domains/{id}`:
  - The body adds:
    - `routes: [{path, targetType, appId?, address?, maxBodyBytes?, noBuffering?, root?, spa?, immutablePrefix?}]`
    - `tls: {mode, certId?}`
    - `cloudflareOnly`
    - `headers: [{name, value}]`
    - `compression`
  - Old-style bodies (`targetType`, `appId`, `address` without `routes`) are still accepted and mean one `/` route.
- `GET /api/certificates`, `POST /api/certificates`, `DELETE /api/certificates/{id}`:
  - `POST` takes `{name, pem}` or `{name, path}`.
  - Responses carry `{id, name, issuer, names[], notAfter, fingerprint, usedBy[]}`; the key is never returned.
  - `DELETE` is refused while the certificate is in use.
- `GET /api/proxy` now also reports `mode` (`normal` or `trial`), `migratedFrom` and `migratedAt`.
- `GET /api/proxy/trial-check`, `POST /api/proxy/takeover`, `POST /api/proxy/rollback`.
- Errors are 400 with field codes (`INVALID_ROUTE`, `INVALID_PATH`, `INVALID_ROOT`, `INVALID_HEADER`, `INVALID_CERT`, `CERT_MISMATCH`, `CERT_EXPIRED`, `CERT_IN_USE`), 409 `PROXY_NOT_TRIAL` and `UNSUPPORTED_PORT_OWNER`, and 403 when the 2FA confirmation is missing.

## 6. Screens

Dark theme only and no rounded corners, as in the rest of the panel. Use the `frontend-design` skill before designing and `web-design-guidelines` at the end.

- **Domain editor:** a dialog or page with three tabs.
  - **Rotas:** an ordered list of path and target. The options appear according to the target type. A domain with a single `/` route looks like today's form.
  - **Certificado:**
    - "Automático (Let's Encrypt)" or "Próprio", where you pick a stored certificate or add one by pasting PEM or giving a server path;
    - a summary with issuer, names and validity.
  - **Segurança:** a "Só Cloudflare" switch, a header list with a "Modelo segurança" button, and a "Compressão" switch.
- **Domains list:** badges for "Certificado próprio", "Só Cloudflare" and an expiry warning.
- **Certificados:** a section listing stored certificates, where they are used and when they expire, with add and delete.
- **Proxy status banner:**
  - `trial`: "Ensaio: o Caddy roda em portas de teste enquanto <processo> ocupa 80/443", the per-domain checklist, and an **"Assumir as portas"** button behind a confirmation dialog that requires 2FA. The job's progress shows live.
  - Active, migrated: "Migrado do <unit> em <data>" and a **"Voltar para o <unit>"** button.

## 7. Tests

- **Unit (Go):**
  - `BuildConfig` golden files for the three reference shapes, with example names, plus trial mode and legacy single-route domains. For v0.3.0-style input the only difference is the 2.3 "always" rules.
  - Route ordering and validation: prefixes, duplicates, a missing `/`, roots outside `/var/www` including `..` and symlink text tricks, and header injection.
  - Certificate validation: mismatched key, names not covering the host or the `www`, expired, bad PEM. Assert the key is never present in API JSON.
  - Cloudflare range refresh: good list, truncated list ignored, fetch failure keeps the last list.
  - Takeover and rollback state machine with fakes for systemd, proxy and checks: success, bind failure leading to rollback, verification failure leading to rollback, rollback failure reported, unsupported owner refused.
  - Store migration of existing domains to routes.
- **Integration** (`-tags integration`, Linux, CI):
  - Takeover and rollback talk to the port owner through a `ServiceManager` interface (systemd in production). The test implementation stops and starts a real `nginx` container on the host network that owns 80/443.
  - Caddy goes to trial, the trial check passes, takeover moves it to 80/443, and rollback restores it.
  - A static folder is really served, with SPA fallback and cache headers.
  - A custom certificate is served on the TLS handshake.
- **Frontend (Vitest):**
  - the route editor: add, reorder, the options per type, validation messages;
  - the certificate picker never shows a key;
  - the trial banner and the takeover confirmation.
- **End to end:** create a domain with two routes and a header, then see it in the list. The trial and takeover flow is covered by the integration test.

## 8. Outside this scope

- Removing nginx or certbot from the VPS.
- Other proxies as port owners (Traefik, HAProxy).
- Non-Cloudflare CDN filters.
- Per-route access rules.
