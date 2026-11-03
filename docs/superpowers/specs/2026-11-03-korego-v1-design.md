# KoreGO v1 — Especificação de design

- **Data:** 2026-11-03
- **Status:** aprovado em conversa, aguardando revisão do documento
- **Escopo deste documento:** visão do produto, arquitetura geral e as etapas 0, 1 e 2 da v1

---

## 1. Visão e objetivo

KoreGO é um gerenciador web de VPS: visual, dinâmico e moderno, para quem não quer depender do terminal para ver e operar o que roda no servidor.

- **Para quem:** produto para outras pessoas (open source ou SaaS no futuro). Na v1 é **self-hosted, um painel por VPS**: o usuário instala o KoreGO na própria VPS.
- **Primeiro usuário:** o autor, numa VPS com o cenário descrito na seção 12.
- **Futuro previsto (fora da v1):** várias VPS num painel central e, depois, SaaS. A arquitetura isola a execução no servidor atrás de uma interface (`Node`) para que isso não exija reescrita.

### Critérios de sucesso da v1

1. Ver CPU, RAM, disco e rede ao vivo e com histórico de 7 dias, sem SSH.
2. Ver **todos** os containers e projetos Compose do host, inclusive os que o KoreGO não criou, com ações e logs ao vivo.
3. Publicar uma aplicação de um repositório GitHub privado e ter deploy automático a cada push, sem tempo fora do ar (exceto Compose).
4. Apontar um domínio para uma aplicação, ou para um endereço local qualquer, com HTTPS automático.
5. Painel exposto na internet com login seguro (senha + 2FA).

## 2. Escopo

### Dentro da v1

| Etapa | Conteúdo |
|---|---|
| **0. Fundação** | Binário Go, instalador, serviço systemd, SQLite + migrações, login de admin com 2FA, layout do painel, fronteira `Node`, hub de eventos/WebSocket |
| **1. Observar** | Monitoramento do host, containers (lista, ações, logs ao vivo), bloco PM2 só leitura |
| **2. Publicar** | Deploy (Dockerfile, Compose, Railpack, imagem), GitHub App com webhooks, domínios e SSL via Caddy, rollback |

### Fora da v1 (versões futuras, cada uma com a própria especificação)

Bancos de dados com um clique; terminal web; gerenciador de arquivos; serviços systemd, PM2 completo e cron; vários usuários e permissões (o banco já nasce preparado); várias VPS e SaaS; GitLab/Gitea (deploy keys); backups; alertas e notificações; suporte a nginx como proxy.

## 3. Arquitetura

### Abordagem escolhida: binário Go único com frontend embutido

- O frontend Next.js é exportado como site estático (`output: 'export'`) e embutido no binário com `go:embed`.
- O binário serve, pela porta **8000**, a interface, a API REST (`/api/...`) e os WebSockets (`/api/ws`).
- O KoreGO roda **no host** como serviço systemd (`korego.service`), com acesso nativo ao Docker, a `/proc` e ao sistema.
- O Caddy roda como container `korego-caddy` com `network_mode: host`, gerenciado pelo KoreGO via Admin API (`127.0.0.1:2019`, não exposta).
- Persistência em **SQLite** (`modernc.org/sqlite`, Go puro, sem CGO), acessado apenas pelo pacote `store` para permitir trocar por Postgres no futuro.

Alternativas descartadas:
- *Next.js como servidor Node + API Go:* dois runtimes, mais RAM, autenticação compartilhada, sem benefício real para um painel.
- *Tudo em containers:* o painel precisaria escapar do container para ver métricas reais, PM2 e o sistema.

### Estrutura do repositório

```
korego/
├── cmd/korego/          main: flags, config, inicialização
├── internal/
│   ├── api/             rotas HTTP (chi) e WebSocket; só traduz requisições
│   ├── auth/            usuários, argon2id, sessões, TOTP, limite de tentativas, CSRF
│   ├── store/           SQLite, migrações, repositórios
│   ├── events/          hub pub/sub em memória (canais por tópico)
│   ├── jobs/            fila de tarefas longas com log persistido
│   ├── node/            interface Node + LocalNode
│   │   ├── metrics/     coleta do host (gopsutil)
│   │   ├── docker/      containers, ações, logs, eventos (Docker SDK)
│   │   ├── pm2/         leitura de `pm2 jlist`
│   │   └── proxy/       interface Proxy + implementação Caddy
│   ├── deploy/          pipeline origem → build → execução → rota
│   ├── github/          GitHub App (manifest flow), tokens, webhooks
│   ├── secrets/         criptografia AES-256-GCM com a chave mestra
│   └── audit/           log de auditoria
├── web/                 Next.js (App Router, TypeScript, Tailwind)
├── scripts/install.sh   instalador
└── docs/
```

### Regras de dependência

- `api` → `auth`, `deploy`, `node`, `store`, `events`, `audit`
- `deploy` → `node`, `github`, `jobs`, `store`, `secrets`, `events`
- `node` não importa nenhum pacote interno além dos próprios subpacotes.
- Cada pacote é testável isoladamente com implementações falsas das interfaces que consome.

### A fronteira `Node`

Tudo que **age** no servidor passa por `Node`:

```go
type Node interface {
    Metrics() metrics.Source
    Docker()  docker.Client
    PM2()     pm2.Reader
    Proxy()   proxy.Proxy
}
```

Na v1 só existe `LocalNode`. No futuro, um `RemoteNode` falará com um agente em outra VPS, sem mudanças em `api` ou `deploy`.

## 4. Etapa 0 — Fundação

### Autenticação

- **Primeiro acesso:** se não houver usuários, o painel abre o setup: criar admin (e-mail + senha) e configurar o TOTP obrigatório (QR code + confirmação de código). Gera 10 códigos de recuperação, exibidos uma vez.
- Tabela `users` já tem a coluna `role` (`admin` na v1); a interface de convites fica para depois.
- Senhas com **argon2id**. TOTP com `pquerna/otp`.
- Sessão em cookie `HttpOnly`, `Secure` (quando em HTTPS), `SameSite=Strict`, validade de 7 dias, armazenada no SQLite (revogável).
- Proteção CSRF em toda rota que altera estado (token em header).
- **Limite de login:** 5 tentativas por minuto por IP; após exceder, bloqueio de 15 minutos.

### Hub de eventos e WebSocket

- Endpoint único `/api/ws`, autenticado pela sessão.
- O cliente envia `subscribe`/`unsubscribe` com o nome do canal: `metrics`, `containers`, `container-logs:<id>`, `container-stats:<id>`, `deploy-logs:<id>`.
- Fontes caras (logs, stats de container) só são ativadas enquanto houver pelo menos um assinante.
- O cliente reconecta com backoff exponencial e reassina os canais.

### Formato de erros da API

```json
{ "error": { "code": "DOCKER_UNAVAILABLE", "message": "Não foi possível conectar ao Docker." } }
```

Códigos estáveis em inglês e maiúsculas; mensagens em português. O frontend decide a apresentação pelo `code`.

### Auditoria

Toda ação que altera estado (deploy, rollback, ações em container, domínios, configurações, login com sucesso/falha) gera um registro em `audit_log`: usuário, ação, alvo, IP, data.

## 5. Etapa 1 — Observar

### Métricas do host

- Coletor em `node/metrics` lê CPU (%), RAM (usada/total), disco por ponto de montagem, rede (bytes/s entrada e saída) e load average **a cada 2 segundos**, publicando no canal `metrics`.
- **Histórico:** a média de cada minuto é gravada em `metrics_minute`; retenção de **7 dias**, limpeza diária. Gráficos de 1h, 24h e 7d.
- Uptime, hostname, sistema operacional e kernel na tela inicial.

### Containers

- Lista via Docker SDK, agrupada pelo label `com.docker.compose.project`; containers sem projeto ficam em "Avulsos".
- Mostra **todos** os containers do host. Selo **gerenciado** (label `korego.managed=true`) ou **externo**.
- Detalhes: imagem, status, health, portas, criação, variáveis (valores mascarados), redes, volumes.
- **Ações:** iniciar, parar, reiniciar; remover (só parados). Todas com confirmação. Em externos, aviso extra: "Este container não foi criado pelo KoreGO".
- **Logs ao vivo:** `follow` com `tail 500`, busca no texto, pausa e download.
- **Stats por container:** CPU e RAM via Docker stats, só sob demanda.
- **Tempo real:** o KoreGO assina os eventos do Docker (start, stop, die, destroy, health_status) e publica no canal `containers`.

### PM2 (somente leitura)

- Se `pm2` existir no host, o bloco "Processos PM2" lista nome, status, CPU, RAM, reinícios e diretório (via `pm2 jlist`, atualizado a cada 10s). Sem ações na v1.

### Indisponibilidade

- Docker fora do ar: métricas e login continuam funcionando; telas de containers mostram "Docker indisponível" com "Tentar novamente".

## 6. Etapa 2 — Publicar

### Modelo de dados

- **Projeto:** agrupamento visual (nome, descrição).
- **Aplicação:** pertence a um projeto. Campos: nome (slug validado por `^[a-z0-9][a-z0-9-]{0,62}$`), origem, tipo de build, branch, caminho do Dockerfile/Compose, porta interna, serviço de entrada (Compose), variáveis de ambiente (criptografadas), healthcheck timeout (padrão 60s), deploy automático (sim/não).
- **Origens:** repositório GitHub (via App), URL Git pública, imagem Docker.
- **Tipos de build:** `dockerfile`, `compose`, `railpack`, `image`.
- **Deploy:** aplicação, commit SHA e mensagem, gatilho (`manual`, `webhook`, `rollback`), status (`queued`, `building`, `starting`, `running`, `failed`, `superseded`, `cancelled`), início, fim, imagem gerada, caminho do log.

### Pipeline de deploy

Executado como job; até **2 builds simultâneos** (configurável); **um deploy ativo por aplicação**.

1. **Buscar:** `git clone --depth 1 --branch <branch>` com token de instalação do GitHub App (ou URL pública) em `/var/lib/korego/builds/<deploy-id>`. Para `image`, pula.
2. **Build:**
   - `dockerfile`: `docker build` → `korego/<app>:<sha-curto>`
   - `railpack`: Railpack via BuildKit → `korego/<app>:<sha-curto>`
   - `image`: `docker pull <imagem>`
   - `compose`: `docker compose -p korego-<app> build`
3. **Subir:** novo container `korego-<app>-<sha-curto>` com labels `korego.managed=true`, `korego.app=<app>`, publicando a porta interna em **`127.0.0.1:<porta livre>`** (faixa 20000–29999).
4. **Verificar saúde:** espera o healthcheck do container, ou, sem ele, uma conexão TCP bem-sucedida na porta; limite configurável (padrão 60s).
5. **Trocar:** atualiza a rota no Caddy para a nova porta; depois para e remove o container anterior. **Sem tempo fora do ar.** Se a verificação falhar, o novo container é removido, o anterior continua no ar e o deploy fica `failed`.
6. **Limpar:** mantém as **3 imagens mais recentes** por aplicação (para rollback) e apaga o diretório de build.

**Compose:** o KoreGO gera `docker-compose.korego.yml` (override) publicando `<serviço de entrada>:<porta>` em `127.0.0.1:<porta livre>` e roda `docker compose -p korego-<app> -f <compose> -f <override> up -d`. Há recriação dos containers (alguns segundos fora do ar) — limitação documentada na interface.

**Rollback:** redeploy de uma das 3 imagens anteriores, pulando as etapas 1–2 (não disponível para Compose na v1).

**Logs:** transmitidos ao vivo pelo canal `deploy-logs:<id>` e gravados em `/var/lib/korego/logs/deploy-<id>.log`.

**Concorrência de webhooks:** se chegar push enquanto um deploy da mesma aplicação está rodando, o novo entra na fila; se já houver um na fila, o antigo vira `superseded`. Fica só o mais recente.

**Reinício do KoreGO:** na inicialização, deploys em `building`/`starting` viram `failed` com motivo "interrompido".

### GitHub App

- **Conexão (manifest flow):** botão "Conectar GitHub" envia o manifest com nome `KoreGO-<hostname>`, permissões mínimas (`contents: read`, `metadata: read`) e evento `push`. O GitHub devolve App ID, chave privada e webhook secret, que são guardados criptografados.
- O usuário instala o App nos repositórios desejados; o KoreGO lista os repositórios acessíveis pela instalação.
- **Tokens:** JWT do App → token de instalação de curta duração, gerado a cada clone.
- **Webhook:** `POST /api/webhooks/github`, rota pública. Verifica `X-Hub-Signature-256` (HMAC SHA-256, comparação em tempo constante); rejeita com 401 se inválida. Para `push` na branch configurada de uma aplicação com deploy automático, enfileira o deploy.
- Requer o painel acessível por HTTPS público (seção 7).

### Domínios e SSL

- Interface `Proxy` (`node/proxy`) com implementação Caddy. Operações: aplicar rota, remover rota, listar, estado do certificado, reaplicar tudo.
- **Domínio → destino:** uma aplicação do KoreGO ou um **endereço manual** (`host:porta`, ex.: `127.0.0.1:5000`), que cobre PM2, containers externos e a migração do nginx.
- **Verificação de DNS:** ao salvar, resolve o domínio e compara com os IPs da VPS; se não bater, mostra o aviso com o registro A necessário, mas permite salvar.
- **SSL automático** pelo Caddy (Let's Encrypt, desafio HTTP-01). O painel mostra o estado: pendente, ativo (com expiração), erro.
- Opções por domínio: redirecionar `www` ↔ domínio principal; forçar HTTPS (padrão: ligado).
- **Fonte da verdade:** a tabela `domains` no SQLite. O KoreGO reaplica toda a configuração no Caddy na inicialização e sempre que detectar que o Caddy reiniciou (verificação a cada 30s; se o container estiver parado, tenta subir de novo).
- **Portas 80/443 ocupadas:** o KoreGO não sobe o Caddy e mostra no painel "Proxy inativo: as portas 80/443 estão em uso por <processo>", com link para o guia de migração.

## 7. Instalação, configuração e atualização

### Instalador (`scripts/install.sh`)

1. Verifica: root, Ubuntu/Debian, arquitetura (amd64/arm64), Docker e Docker Compose (oferece instalar se faltarem), portas 80/443 e 8000.
2. **Não altera serviços existentes.** Se 80/443 estiverem ocupadas, segue sem proxy.
3. Baixa o binário da release mais recente, verifica o SHA256 e instala em `/usr/local/bin/korego`.
4. Cria `/var/lib/korego` (dados, builds, logs) e `/etc/korego` (`config.toml` e `master.key` com permissão 0600).
5. Instala e inicia `korego.service`.
6. Exibe `http://<IP>:8000` para o setup inicial.

### Configuração do painel

- No setup, opcionalmente informa o domínio do painel (ex.: `korego.exemplo.com`); o KoreGO cria a rota no Caddy.
- Depois que o painel estiver acessível por HTTPS, oferece **desativar a porta 8000 pública** (passa a escutar só em `127.0.0.1:8000`).

### Atualização

- O painel consulta as releases do GitHub e mostra "Atualizar para vX.Y.Z".
- Baixa o binário, verifica SHA256, troca o arquivo mantendo o anterior como `korego.prev`, reinicia o serviço. Se o serviço não responder em `/api/health` em 30s, restaura `korego.prev`.

### Releases

GitHub Actions: build do frontend, build Go para `linux/amd64` e `linux/arm64`, checksums SHA256 e publicação da release.

## 8. Frontend

- Next.js (App Router) com `output: 'export'`, TypeScript, Tailwind CSS, TanStack Query para a API REST, cliente WebSocket próprio com reconexão.
- **Direção visual:** inspirado no Coolify, porém **mais escuro** e **sem bordas arredondadas** (cantos retos em todos os elementos).
- **Na implementação:** usar a skill `frontend-design` para idealizar a interface e a skill `web-design-guidelines` para revisar a interface quanto a diretrizes e boas práticas.
- Telas da v1: setup inicial, login (senha + TOTP), visão geral (métricas + resumo), containers (lista agrupada, detalhe, logs), projetos e aplicações (criar, configurar, variáveis, deploys, logs, rollback), domínios, configurações (GitHub, domínio do painel, porta 8000, atualização, sessões, auditoria).

## 9. Segurança

- Nenhum comando montado com interpolação de shell: Docker via SDK; `git`, `docker compose`, `railpack` e `pm2` via `exec` com argumentos separados; nomes, branches e imagens validados.
- Segredos (variáveis de ambiente, chave privada do GitHub App, webhook secret) criptografados com AES-256-GCM usando `/etc/korego/master.key`.
- Valores de variáveis mascarados na interface e nunca enviados em logs.
- Sessões revogáveis; tela com sessões ativas.
- Admin API do Caddy e Docker socket nunca expostos.
- Containers publicados apenas em `127.0.0.1`.

## 10. Testes

- **Unitários (Go):** cada pacote com fakes de `Node`, `Proxy`, cliente GitHub e `store`.
- **Integração (`-tags integration`):** Docker e Caddy reais (CI e Docker Desktop local): deploy completo de repositório de exemplo, troca de porta sem queda, falha de healthcheck mantendo a versão anterior, rollback, reaplicação de rotas após reinício do Caddy.
- **Webhook:** payloads reais do GitHub com assinaturas válidas e inválidas.
- **Frontend:** Vitest nos componentes principais; Playwright no fluxo setup → login com 2FA → containers → criar app por imagem → adicionar domínio.

## 11. Organização do trabalho

- Cada etapa (0, 1, 2) vira um conjunto de tarefas no plano de implementação.
- **Commits organizados por etapa e por tarefa**, com mensagens que identificam a etapa (ex.: `feat(etapa-0): ...`), para manter o controle do progresso.
- Repositório **público** no GitHub: `engviniciusbs/korego`.
- **Nada sensível é versionado:** IPs, domínios pessoais, credenciais, dados de servidores reais e arquivos `.env` ficam só localmente (`docs/local/`, `.env`, `/etc/korego`), todos no `.gitignore`. Exemplos na documentação usam valores fictícios (`exemplo.com`, `203.0.113.10`).

## 12. Cenário de referência e migração de um nginx existente

### Cenário típico que a v1 precisa suportar

Uma VPS Ubuntu que já tem: projetos Docker Compose publicados em `127.0.0.1`, apps Node rodando no PM2, nginx no host ocupando 80/443 com certificados do certbot e nenhum firewall ativo. O KoreGO precisa mostrar tudo isso sem alterar nada na instalação e oferecer a migração do nginx como passo explícito.

Os dados concretos da VPS do autor ficam apenas localmente em `docs/local/` (fora do Git).

### Roteiro de migração nginx → Caddy (tarefa operacional, após o KoreGO instalado)

1. Backup de `/etc/nginx` e `/etc/letsencrypt`.
2. Ler os `proxy_pass` atuais de cada site e cadastrar os domínios no KoreGO como **endereço manual** para as mesmas portas locais.
3. `systemctl stop nginx && systemctl disable nginx`; o KoreGO sobe o Caddy em 80/443 e emite os certificados (interrupção esperada: menos de 1 minuto).
4. Verificar cada domínio por HTTPS.
5. **Volta:** parar `korego-caddy`, `systemctl enable --now nginx`.

### Recomendações de segurança para a VPS (o painel pode sugerir no futuro)

- Usar apenas chave SSH, desativando login por senha.
- Ativar o firewall liberando apenas 22, 80 e 443 (e 8000 só até o domínio do painel estar ativo).
