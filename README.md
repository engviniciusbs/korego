# KoreGO

KoreGO é um gerenciador de VPS auto-hospedado: um único binário Go que serve a API e o painel web (Next.js exportado e embutido). Ele roda na própria máquina que você quer gerenciar, sem dependências externas além do sistema operacional.

## Estado

**Etapa 3 (Proxy avançado e migração do nginx)** concluída, sobre a **Etapa 2 (Publicar)**, a **Etapa 1 (Observar)** e a **Etapa 0 (Fundação)**: binário único com painel embutido, primeiro acesso com criação do administrador (senha + 2FA obrigatório), sessões, auditoria, visão geral do servidor e autoatualização com verificação de checksum e rollback. A Etapa 1 acrescentou métricas ao vivo, containers Docker e PM2; a Etapa 2, publicação de aplicações, domínios com HTTPS automático e deploy pelo GitHub (veja "Publicar"); a Etapa 3 dá aos domínios rotas por caminho, pastas estáticas, certificados próprios, modo "Só Cloudflare", cabeçalhos e compressão, e troca o nginx pelo Caddy do KoreGO com ensaio, volta automática e botão de voltar (veja "Domínios avançados" e "Migrar do nginx").

Roadmap:

- **Etapa 1 (Observar)**: métricas, containers e PM2 (pronta).
- **Etapa 2 (Publicar)**: aplicações, deploy, domínios e HTTPS (pronta).
- **Etapa 3 (Proxy avançado)**: rotas, pastas estáticas, certificados próprios, Só Cloudflare e migração do nginx (pronta).

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
- **Domínios**: cada domínio aponta para uma aplicação ou para um endereço manual (`127.0.0.1:3000`), e pode ter rotas por caminho e pastas estáticas (veja "Domínios avançados"). O KoreGO gerencia um Caddy em `80/443` e emite os certificados por HTTP-01, então o DNS já precisa apontar para o servidor (ou use um certificado próprio). Se um nginx já ocupa as portas, veja "Migrar do nginx".
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

## Domínios avançados

A Etapa 3 amplia o editor de domínio (**Domínios → Adicionar domínio** ou **Editar**) com três abas: **Rotas**, **Certificado** e **Segurança**. Um domínio com só a rota `/` continua igual ao da Etapa 2, e os domínios que já existiam seguem funcionando sem nenhum passo manual.

- **Rotas por caminho**: até 20 por domínio. O caminho é `/` ou um prefixo que começa e termina com `/` (`/api/`). Vence o prefixo mais longo, e `/` (obrigatória) atende o resto. Um pedido para `/api` sem a barra final recebe `308` para `/api/`. Cada rota aponta para:
  - **Aplicação** do KoreGO;
  - **Endereço** `host:porta`, com **Limite do corpo** (acima dele a resposta é `413`; 0 = sem limite) e **Sem buffer** (para streaming e eventos);
  - **Pasta** estática (veja abaixo).
- **Pastas estáticas**: a pasta precisa estar dentro de `/var/www`, que o Caddy enxerga só para leitura. Symlinks valem, então um deploy pode trocar `/var/www/loja/current` de `releases/1` para `releases/2` sem recarregar nada: a requisição seguinte já sai da pasta nova.
  - **Não crie symlinks em `/var/www` que apontem para fora dela.** O Caddy segue symlinks, e só `/var/www` está montada no container: um link para fora ou não abre, ou, se o alvo também estiver montado por outro caminho, expõe o que não devia. Mantenha releases e links dentro de `/var/www`.
  - **SPA**: um caminho desconhecido recebe o `/index.html`. O `index.html`, inclusive no fallback, sai com `Cache-Control: no-cache`.
  - **Cache imutável** (opcional, como `/assets/`): o que estiver sob esse prefixo sai com `Cache-Control: public, max-age=31536000, immutable`; arquivo ausente é `404`.
  - Fora de `/`, a rota funciona como alias: em `/app/`, o pedido `/app/x` sai de `<pasta>/x`.
  - `.git` e arquivos `.env*` nunca são servidos.
- **Certificados próprios** (aba **Certificado** → **Próprio**): cole o PEM (cadeia + chave) ou informe o caminho do arquivo no servidor. O KoreGO recusa cadeia inválida, chave que não bate com o certificado, certificado vencido ou que não cobre o hostname (e o `www`, quando há redirecionamento). O PEM fica cifrado no banco, a chave nunca volta pela API nem aparece no painel, e um certificado (um curinga `*.exemplo.com`, por exemplo) pode ser usado por vários domínios. A seção **Certificados** lista onde cada um é usado e quando vence; o painel avisa 30 dias antes. É o caminho para certificados de origem da Cloudflare ou para domínios cujo DNS ainda não aponta para o servidor.
- **Só Cloudflare** (aba **Segurança**): conexões que não vêm das faixas de IP da Cloudflare recebem `403`, em HTTP e HTTPS, e o IP do cliente passa a ser lido do `CF-Connecting-IP`. O upstream recebe esse IP em `X-Real-IP` e em `X-Forwarded-For` (que o Caddy reescreve com só o visitante), então o painel e as aplicações veem o visitante, não o IP da Cloudflare. As faixas vêm embutidas no binário e são atualizadas uma vez por dia (uma lista truncada ou com erro é ignorada e a anterior continua valendo). Duas ressalvas:
  - O filtro confere o IP, não quem é o cliente: as faixas da Cloudflare são compartilhadas por todas as contas, e qualquer pessoa pode apontar uma zona dela para o seu IP e passar pelo filtro. Para garantir que só a **sua** zona chega à origem, ative também o **Authenticated Origin Pulls** (mTLS) na Cloudflare; o KoreGO não configura isso.
  - A confiança nas faixas (`trusted_proxies`) vale por servidor do Caddy, não por domínio: com qualquer domínio em Só Cloudflare, os servidores HTTP e HTTPS inteiros passam a aceitar o `CF-Connecting-IP` vindo das faixas da Cloudflare, inclusive para os outros domínios que estiverem atrás dela.
- **Cabeçalhos de resposta**: até 30 por domínio, enviados em todas as rotas. **Modelo segurança** acrescenta `Strict-Transport-Security`, `X-Content-Type-Options`, `Referrer-Policy` e `Permissions-Policy`. Sempre, em todo domínio, o cabeçalho `Server` é removido e o upstream recebe `X-Real-IP`.
- **Compressão**: zstd e gzip, conforme o navegador aceitar. Ligada por padrão nos domínios novos e desligada nos que vieram da Etapa 2.

## Migrar do nginx

Quando outro servidor (nginx ou apache2) segura as portas 80/443, o KoreGO não briga por elas: o Caddy roda em **modo ensaio**, em `127.0.0.1:18080` e `127.0.0.1:18443`, e o painel mostra "Ensaio: o Caddy roda em portas de teste enquanto nginx (pid …) ocupa 80/443". Você cadastra e confere tudo com o nginx ainda no ar e só então troca. O KoreGO nunca para o nginx por conta própria, só quando você pede, e não lê os arquivos do nginx: os sites são cadastrados no painel. O ensaio também não termina sozinho: se o nginx sair do ar por um instante (um reload, ou o KoreGO subindo antes dele no boot), o Caddy continua no ensaio e o painel avisa que as portas estão livres; só **Assumir as portas** põe o Caddy em 80/443.

Os exemplos usam nomes fictícios: `loja.exemplo.net` (uma SPA em `/var/www/loja/current` com a API em `127.0.0.1:4000`), `exemplo.com` (uma aplicação em `127.0.0.1:3000`) e o servidor `203.0.113.10`.

1. **Backup.** Guarde a configuração e os certificados atuais, e leve uma cópia para fora do servidor:

   ```sh
   sudo tar czf /root/backup-nginx-$(date +%F).tar.gz /etc/nginx /etc/letsencrypt /etc/ssl
   sudo grep -R -n -e server_name -e proxy_pass -e root /etc/nginx/sites-enabled
   ```

   O `grep` mostra o que cadastrar: para cada `server_name`, os `location` com `proxy_pass` (rotas de endereço) e com `root` (pastas).
2. **Cadastrar.** Em **Domínios**, adicione cada site com as mesmas rotas: `loja.exemplo.net` com `/api/` → endereço `127.0.0.1:4000` e `/` → pasta `/var/www/loja/current` com SPA e cache imutável em `/assets/`; `exemplo.com` → endereço `127.0.0.1:3000`. Para sites atrás da Cloudflare em SSL Full (strict), guarde um certificado de origem da Cloudflare (ou o curinga atual) em **Certificados** e escolha **Próprio** na aba Certificado. Acrescente na aba Segurança os cabeçalhos que o nginx enviava (CSP, HSTS).
3. **Comparar no ensaio.** O banner do proxy mostra o ensaio por domínio: handshake TLS, status de cada rota e, nas pastas, se o `index.html` saiu. Domínios com certificado automático aparecem como "certificado pendente até assumir as portas", porque o certificado só é emitido depois da troca. Compare também a resposta pública (nginx) com a do ensaio para a página inicial, um caminho da API, um arquivo de `/assets/` e um caminho desconhecido da SPA:

   ```sh
   for p in / /api/saude /assets/app.js /rota/qualquer; do
     curl -s -o /tmp/pub -D - "https://loja.exemplo.net$p" | grep -v -i -e '^date:' -e '^server:' -e '^cf-' > /tmp/pub.h
     curl -sk -o /tmp/ens -D - --resolve loja.exemplo.net:18443:127.0.0.1 "https://loja.exemplo.net:18443$p" | grep -v -i -e '^date:' -e '^server:' > /tmp/ens.h
     echo "== $p"; diff /tmp/pub.h /tmp/ens.h; sha256sum /tmp/pub /tmp/ens
   done
   ```

   Status, cabeçalhos (sem `Date`, `Server` e os que a Cloudflare acrescenta) e o hash do corpo devem bater. Teste também o login e a API da aplicação.
4. **Assumir as portas.** Num horário de pouco tráfego, clique em **Assumir as portas** no banner e confirme com o código do 2FA (ou um código de recuperação). Só `nginx.service` e `apache2.service` do systemd são aceitos. Domínios com certificado automático precisam ter o DNS apontando para o servidor (ou para a Cloudflare) antes. O KoreGO então:
   - para o nginx (`systemctl stop nginx`; se as portas já estiverem livres, pula este passo) e sobe o Caddy em 80/443, tentando por até 5 s enquanto o nginx solta as portas;
   - confere cada domínio em `127.0.0.1:443` (certificados automáticos têm até 60 s para sair);
   - se algo falhar, volta sozinho: Caddy de novo no ensaio, `systemctl start nginx` e a mensagem "voltou para o nginx: <motivo>";
   - com tudo certo, desativa o nginx no boot (`systemctl disable nginx`) e mostra "Migrado do nginx em <data>".

   Depois, repita a comparação do passo 3 contra a resposta pública.
5. **Voltar.** Enquanto o arquivo da unit existir, o botão **Voltar para o nginx** (também com o código do 2FA) põe o Caddy de volta no ensaio e roda `systemctl enable --now nginx`. A troca não altera os arquivos em `/etc/nginx` nem os certificados do certbot, então o nginx volta como estava. Apagar o nginx e o certbot depois fica a seu critério.

O guia completo, com o painel em HTTPS e o desligamento da porta 8000, está em [docs/migracao-nginx.md](docs/migracao-nginx.md).

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
# Consumo por dono (procs reais + Docker; só em Linux). Fora de container roda direto;
# dentro de um, o container precisa de --pid host --cgroupns host e do socket do
# Docker (-v /var/run/docker.sock:/var/run/docker.sock), além do CLI docker no PATH
go test -tags integration ./internal/node/docker/... ./internal/deploy/... ./internal/usage/...

# Proxy e troca nginx -> Caddy (Etapa 3; só em Linux, como root e com 80/443 livres).
# Sobe o korego-caddy e um nginx:alpine (korego-it-nginx) em rede host e usa
# /var/www/korego-it; -p 1 porque os dois pacotes usam o mesmo korego-caddy.
# Sem root os testes são pulados; KOREGO_IT_STRICT=1 faz falhar em vez de pular.
go test -tags integration -p 1 -exec 'sudo -E' ./internal/node/proxy/... ./internal/migrate/...

# Instalador (container ubuntu:24.04 com docker, apt-get e curl simulados)
sh scripts/test-install.sh && shellcheck scripts/*.sh

# Ponta a ponta (Playwright), com o binário já compilado em bin/
make web && make build
cd web && pnpm exec playwright install chromium && pnpm test:e2e
```

O teste ponta a ponta sobe o binário em `:18000` com dados temporários e percorre: setup, 2FA, visão geral, logout, login com código de recuperação e auditoria. Um segundo spec cria um container `busybox` de teste e percorre consumo (só em Linux: o projeto `e2e` aparece como dono, com o processo do container), lista, logs, parar, remover e auditoria; ele é pulado se o Docker não estiver disponível. O terceiro (`publicar`) cria um projeto e uma aplicação pela imagem `nginx:1-alpine`, faz o deploy, aponta `site-e2e.korego.localhost` pelo Caddy, refaz o deploy sem nenhuma requisição falhar, volta a versão e confere a auditoria; ele precisa de Linux com Docker e as portas 80/443 livres, e fora do CI é pulado sem isso. O quarto (`dominios`) cria pelo editor um domínio com as rotas `/api/` e `/` para endereços e o cabeçalho `X-Teste: 1`, confere a lista, reabre o editor com as duas rotas e o remove; ele roda em qualquer sistema.

## Segurança

- O serviço roda como **root**, pois vai administrar o servidor. Restrinja o acesso à porta 8000 (firewall, VPN ou proxy reverso com HTTPS).
- 2FA (TOTP) obrigatório para o administrador; senhas com argon2id; sessões em cookie `HttpOnly` com proteção CSRF.
- O setup inicial exige o token gravado em `/etc/korego/setup-token`, legível apenas por root, para que ninguém na rede reivindique o servidor antes de você.
- Bloqueio de login após 5 falhas por minuto por IP.
- A autoatualização valida o checksum da release e restaura o binário anterior se algo falhar.
- A config do Caddy leva as chaves dos certificados próprios e é carregada com `persist` desligado, então o Caddy não a grava em `/config/caddy/autosave.json`. Se uma versão anterior deixou esse arquivo no volume `korego-caddy-config`, o KoreGO o apaga na subida (via `docker exec` no `korego-caddy`).

## Licença

Apache License 2.0. Veja [LICENSE](LICENSE).
