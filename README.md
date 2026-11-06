# KoreGO

KoreGO é um gerenciador de VPS auto-hospedado: um único binário Go que serve a API e o painel web (Next.js exportado e embutido). Ele roda na própria máquina que você quer gerenciar, sem dependências externas além do sistema operacional.

## Estado

**Etapa 0 (Fundação)**: binário único com painel embutido, primeiro acesso com criação do administrador (senha + 2FA obrigatório), sessões, auditoria, visão geral do servidor e autoatualização com verificação de checksum e rollback.

Roadmap:

- **Etapa 1 (Observar)**: métricas e monitoramento do servidor.
- **Etapa 2 (Publicar)**: publicação de aplicações e domínios.

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

Variáveis de ambiente: `KOREGO_LISTEN` (padrão `:8000`), `KOREGO_DATA_DIR` e `KOREGO_CONFIG_DIR`.

## Testes

```sh
# Go
go test ./... && go vet ./...

# Web (unitários e lint)
cd web && pnpm vitest run && pnpm lint

# Ponta a ponta (Playwright), com o binário já compilado em bin/
make web && make build
cd web && pnpm exec playwright install chromium && pnpm test:e2e
```

O teste ponta a ponta sobe o binário em `:18000` com dados temporários e percorre: setup, 2FA, visão geral, logout, login com código de recuperação e auditoria.

## Segurança

- O serviço roda como **root**, pois vai administrar o servidor. Restrinja o acesso à porta 8000 (firewall, VPN ou proxy reverso com HTTPS).
- 2FA (TOTP) obrigatório para o administrador; senhas com argon2id; sessões em cookie `HttpOnly` com proteção CSRF.
- O setup inicial exige o token gravado em `/etc/korego/setup-token`, legível apenas por root, para que ninguém na rede reivindique o servidor antes de você.
- Bloqueio de login após 5 falhas por minuto por IP.
- A autoatualização valida o checksum da release e restaura o binário anterior se algo falhar.

## Licença

Apache License 2.0. Veja [LICENSE](LICENSE).
