# KoreGO

KoreGO é um gerenciador de VPS auto-hospedado: um único binário Go que serve a API e o painel web (Next.js exportado e embutido). Ele roda na própria máquina que você quer gerenciar, sem dependências externas além do sistema operacional.

## Estado

**Etapa 2 (Publicar)** concluída, sobre a **Etapa 1 (Observar)** e a **Etapa 0 (Fundação)**: binário único com painel embutido, primeiro acesso com criação do administrador (senha + 2FA obrigatório), sessões, auditoria, visão geral do servidor e autoatualização com verificação de checksum e rollback. A Etapa 1 acrescentou métricas ao vivo, containers Docker e PM2; a Etapa 2 acrescenta publicação de aplicações, domínios com HTTPS automático e deploy pelo GitHub (veja "Publicar").

Roadmap:

- **Etapa 1 (Observar)**: métricas, containers e PM2 (pronta).
- **Etapa 2 (Publicar)**: aplicações, deploy, domínios e HTTPS (pronta).

## Instalação

Em um servidor Linux (amd64 ou arm64) com systemd:

```sh
curl -fsSL https://github.com/engviniciusbs/korego/releases/latest/download/install.sh | sudo sh
```

O instalador baixa o binário da última release, confere o checksum, instala em `/usr/local/bin/korego` e ativa o serviço `korego.service`.

### Primeiro acesso

1. Abra `http://<IP>:8000` (por exemplo, `http://203.0.113.10:8000`).
2. Leia o token de setup no servidor:

   ```sh
   sudo cat /etc/korego/setup-token
   ```

3. Informe o token, o e-mail e a senha do administrador (mínimo de 12 caracteres).
4. Escaneie o QR code com um aplicativo autenticador (ou digite o segredo) e confirme o código de 6 dígitos. **O 2FA é obrigatório.**
5. Guarde os 10 códigos de recuperação: eles são exibidos uma única vez e cada um funciona uma só vez.

Depois do setup, o login passa a exigir senha e código do autenticador (ou um código de recuperação).

## O que o KoreGO mostra

- **Métricas**: CPU, memória, load, discos e rede ao vivo (amostra a cada 2 s), com histórico de 7 dias e gráficos de 1h, 24h e 7d.
- **Containers**: todos os containers do Docker, agrupados por projeto Compose (os sem projeto ficam em "Avulsos"). Cada um mostra o selo **gerenciado** ou **externo**, e dá para iniciar, parar, reiniciar e remover (remover só com o container parado; toda ação pede confirmação). O detalhe traz logs ao vivo (busca, pausa e download) e stats de CPU e RAM sob demanda. Das variáveis de ambiente, só os nomes aparecem.
- **PM2**: lista somente leitura dos processos (status, CPU, RAM, reinícios e diretório), atualizada a cada 10 s.

### PM2 de outro usuário

O serviço roda como `root`, então por padrão o KoreGO procura o PM2 no `PATH` e o daemon em `/root/.pm2`. Se o PM2 roda com outro usuário, informe o binário e o `PM2_HOME` dele em `/etc/korego/config.toml` (exemplo com um usuário `deploy`):

```toml
pm2_path = "/home/deploy/.nvm/versions/node/v22.11.0/bin/pm2"
pm2_home = "/home/deploy/.pm2"
```

Também valem as variáveis `KOREGO_PM2_PATH` e `KOREGO_PM2_HOME`. Sem `pm2_home`, vale o `PM2_HOME` do ambiente e, sem ele, `$HOME/.pm2`. O KoreGO só executa o `pm2 jlist` quando o daemon desse `PM2_HOME` está ativo, para nunca iniciar um daemon novo.

Se o Docker estiver parado, as métricas e o login continuam funcionando e as telas de containers mostram "Docker indisponível".

## Consumo por dono

A página **Consumo** (`/consumo/`) mostra **quem** está usando a CPU e a RAM do servidor, e não só o total. A cada 5 s o KoreGO lê os processos em `/proc` e atribui cada um a um dono pelo cgroup, nesta ordem:

| Dono | Como é reconhecido | Nome exibido |
| --- | --- | --- |
| Projeto Compose | processo de um container com o label `com.docker.compose.project` | o nome do projeto (ex.: `loja`) |
| Container avulso | processo de um container sem projeto | `Avulso: <container>` |
| App do PM2 | o PID, ou um ancestral, é um app do `pm2 jlist` | `PM2: <app>` |
| Serviço | unidade systemd (`nginx.service`); o `pm2-*.service` vira "PM2 (daemon)" | o nome da unidade (ex.: `nginx`) |
| Sessões de usuário | processos sob `user.slice` (SSH, terminais) | Sessões de usuário |
| Kernel | threads do kernel | Kernel |
| Outros | todo o resto | Outros |

- **Tabela de donos**: CPU e RAM com barras e número de processos. Ordena por CPU ou RAM, filtra por tipo e atualiza ao vivo. Containers mostram o selo **gerenciado** ou **externo**.
- **Detalhe do dono**: os processos (PID, nome, comando, usuário, CPU e RAM) e um histórico de CPU e RAM de 1h, 24h ou 7d. Valores sensíveis no comando (`--password ••••`, `TOKEN=••••`, `postgres://app:••••@db`) aparecem mascarados.
- **Visão geral**: o painel "Maiores consumidores" traz os 5 donos com mais CPU. Clicar num ponto do gráfico de CPU ou RAM abre "Quem estava usando" com os maiores daquele momento.
- **Histórico**: média por minuto dos 15 maiores em CPU e dos 15 maiores em RAM, mais uma linha "Demais" com a soma do resto, guardada por 7 dias.

A CPU é a % da máquina inteira (100% = todos os núcleos). A **RAM** de um dono é a soma da memória residente (RSS) dos processos dele. É uma aproximação: páginas compartilhadas (bibliotecas, memória de processos pai e filhos depois do `fork`) entram mais de uma vez, então a soma dos donos pode passar da RAM em uso no servidor.

Se o Docker estiver parado, os processos dos containers aparecem em "Outros" e a página avisa. Fora do Linux, sem cgroup, tudo cai em "Outros".

## Publicar

A Etapa 2 publica aplicações a partir do painel, sem sair dele.

- **Aplicações**: cada app tem um tipo de deploy, uma origem (repositório do GitHub ou URL git) e variáveis de ambiente (os valores nunca são exibidos nem registrados).
- **Tipos de deploy**:
  - **Dockerfile**: constrói a imagem com o `Dockerfile` do repositório.
  - **Railpack**: constrói sem Dockerfile, com o [Railpack](https://railpack.com) (precisa do binário `railpack`; o instalador oferece instalá-lo).
  - **Compose**: sobe o `docker-compose.yml` do repositório.
- **Deploy automático**: ao conectar o **GitHub App** do KoreGO, um `push` na branch configurada dispara o deploy. Também dá para publicar manualmente pelo botão de deploy.
- **Domínios**: cada domínio aponta para uma aplicação ou para um endereço manual (`127.0.0.1:3000`). O KoreGO gerencia um Caddy em `80/443` e emite os certificados por HTTP-01, então o DNS já precisa apontar para o servidor. Para migrar um nginx existente, veja [docs/migracao-nginx.md](docs/migracao-nginx.md).
- **Rollback**: apps Dockerfile e Railpack voltam para a imagem do deploy anterior com um clique.
- **Limitações do Compose**: o deploy derruba e recria os serviços (há **downtime**) e **não há rollback** automático.

O instalador confere o `git` (instala pelo `apt-get`) e oferece instalar o Railpack. Controle com `KOREGO_RAILPACK=auto|skip|install`; com `KOREGO_YES=1` ele não pergunta nada.

### Configuração

Opções em `/etc/korego/config.toml`; cada uma também vale como variável de ambiente.

| Opção | Variável | Padrão | Descrição |
| --- | --- | --- | --- |
| `listen` | `KOREGO_LISTEN` | `:8000` | Endereço do painel. |
| `max_builds` | `KOREGO_MAX_BUILDS` | `2` | Máximo de deploys construindo ao mesmo tempo. |
| `public_ip` | `KOREGO_PUBLIC_IP` | vazio | IP público, usado na verificação de DNS dos domínios (vazio = IPs das interfaces). |
| `pm2_path` | `KOREGO_PM2_PATH` | vazio | Binário do `pm2` (vazio = `PATH`). |
| `pm2_home` | `KOREGO_PM2_HOME` | vazio | `PM2_HOME` do usuário que roda o PM2. |
| `github_api_url` | `KOREGO_GITHUB_API_URL` | API do GitHub | Servidor da API do GitHub App (testes, GitHub Enterprise). |
| `github_web_url` | `KOREGO_GITHUB_WEB_URL` | github.com | Servidor web do GitHub App (testes, GitHub Enterprise). |

## Desenvolvimento local

Requisitos: Go 1.27, Node 22 e pnpm 10.

```sh
# terminal 1: frontend com hot reload
cd web
pnpm install
pnpm dev

# terminal 2: backend, repassando o frontend para o servidor de desenvolvimento
KOREGO_DEV_PROXY=http://localhost:3000 KOREGO_DATA_DIR=./tmp KOREGO_CONFIG_DIR=./tmp go run ./cmd/korego
```

O painel fica em `http://localhost:8000`. Para gerar o binário completo (frontend embutido):

```sh
make web     # compila o frontend e copia para internal/webui/dist
make build   # go build -o bin/korego ./cmd/korego
```

Variáveis de ambiente: `KOREGO_LISTEN` (padrão `:8000`), `KOREGO_DATA_DIR`, `KOREGO_CONFIG_DIR` e as da tabela de configuração acima.

## Testes

```sh
# Go
go test ./... && go vet ./...

# Web (unitários e lint)
cd web && pnpm vitest run && pnpm lint

# Integração com o Docker (precisa do Docker em execução)
# Integração da Etapa 2 (Docker e Caddy; só em Linux)
# Consumo por dono (procs reais + Docker; só em Linux, fora de container)
go test -tags integration ./internal/node/docker/... ./internal/node/proxy/... ./internal/deploy/... ./internal/usage/...

# Instalador (container ubuntu:24.04 com docker, apt-get e curl simulados)
sh scripts/test-install.sh && shellcheck scripts/*.sh

# Ponta a ponta (Playwright), com o binário já compilado em bin/
make web && make build
cd web && pnpm exec playwright install chromium && pnpm test:e2e
```

O teste ponta a ponta sobe o binário em `:18000` com dados temporários e percorre: setup, 2FA, visão geral, logout, login com código de recuperação e auditoria. Um segundo spec cria um container `busybox` de teste e percorre consumo (só em Linux: o projeto `e2e` aparece como dono, com o processo do container), lista, logs, parar, remover e auditoria; ele é pulado se o Docker não estiver disponível. O terceiro (`publicar`) cria um projeto e uma aplicação pela imagem `nginx:1-alpine`, faz o deploy, aponta `site-e2e.korego.localhost` pelo Caddy, refaz o deploy sem nenhuma requisição falhar, volta a versão e confere a auditoria; ele precisa de Linux com Docker e as portas 80/443 livres, e fora do CI é pulado sem isso.

## Segurança

- O serviço roda como **root**, pois vai administrar o servidor. Restrinja o acesso à porta 8000 (firewall, VPN ou proxy reverso com HTTPS).
- 2FA (TOTP) obrigatório para o administrador; senhas com argon2id; sessões em cookie `HttpOnly` com proteção CSRF.
- O setup inicial exige o token gravado em `/etc/korego/setup-token`, legível apenas por root, para que ninguém na rede reivindique o servidor antes de você.
- Bloqueio de login após 5 falhas por minuto por IP.
- A autoatualização valida o checksum da release e restaura o binário anterior se algo falhar.

## Licença

Apache License 2.0. Veja [LICENSE](LICENSE).
