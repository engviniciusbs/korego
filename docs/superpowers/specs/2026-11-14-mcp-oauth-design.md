# Conexão de agentes por OAuth (MCP) — design

## Intenção

Hoje, para ligar um agente ao `/mcp`, o usuário cria um token no painel, copia e cola numa configuração. O objetivo é que ele só informe a URL do painel ao agente. O agente abre o navegador numa tela do KoreGO, o usuário entra, confirma com o 2FA e o agente fica conectado. Isso usa o fluxo de autorização previsto pela especificação do MCP (OAuth 2.1 com PKCE, Protected Resource Metadata e registro dinâmico de cliente), que Claude Code, Cursor, VS Code, Codex e Gemini CLI já suportam.

## Objetivos

- `claude mcp add --transport http korego https://<painel>/mcp`, sem token, abre o navegador e conecta.
- Cursor e VS Code conectam com o link de instalação contendo só a URL.
- Cada conexão aparece na lista de Tokens de API, com o nome do cliente, e é revogável ali como os tokens manuais.
- O 2FA é exigido em toda autorização.

## Não objetivos

- Clientes hospedados na web (claude.ai, ChatGPT, outros sites): o retorno da autorização só vai para a máquina do usuário ou para editores conhecidos. Decidido pelo usuário; pode ser ampliado depois.
- Escopos finos (só leitura etc.): o acesso continua total, como o token manual.
- Client ID Metadata Documents (CIMD): exigiriam que o servidor buscasse URLs arbitrárias (SSRF). Os clientes-alvo caem no registro dinâmico quando o servidor não anuncia CIMD.
- Endpoint de revogação RFC 7009 e introspecção: a revogação é pelo painel.
- OpenID Connect (id_token, userinfo).

## Anti-objetivos (falharia mesmo funcionando)

- Um link de phishing fazer o acesso sair da máquina do usuário.
- A sondagem sem token, que todo cliente OAuth faz antes de autenticar, bloquear o IP pelo limitador.
- O painel servir metadados com uma origem tirada do cabeçalho `Host`, que é manipulável.
- A tela de autorização ser enquadrada (clickjacking) ou aprovar sem 2FA.
- Quebrar os tokens manuais, que continuam valendo para scripts, CI e clientes sem OAuth.

## Abordagens consideradas

1. **Servidor de autorização embutido no KoreGO (escolhida).** O próprio binário serve os metadados, o registro, a autorização e o token. A tela de consentimento usa a sessão e o 2FA que já existem. Não há dependência nova nem serviço externo.
2. **Provedor externo (Auth0, Keycloak, Zitadel).** Delegaria a parte de OAuth, mas o 2FA e os usuários do KoreGO ficariam duplicados, e o painel passaria a depender de outro serviço. Não combina com um painel de binário único.
3. **Só melhorar o copiar e colar** (o que existe hoje mais as abas por cliente). Não atende ao pedido de "link de autenticação".

## Decisões

### Origem e pré-requisito

- A origem canônica é `https://<domínio do painel>`, lida da configuração do painel e só quando o certificado está ativo (como `githubHandlers.panelURL`). Ela é o `issuer` e a base do `resource` (`https://<domínio>/mcp`). Nunca vem do cabeçalho `Host`.
- Sem domínio com certificado ativo, os endpoints OAuth respondem 404 e o `/mcp` não anuncia `resource_metadata`. O token manual por loopback continua funcionando.
- Todos os endpoints OAuth exigem `requestIsHTTPS`. Não usam `tokenTransportOK`, que também aceita HTTP por loopback, e com a origem obrigatoriamente HTTPS o loopback não tem uso aqui.
- O cliente precisa usar exatamente a URL canônica. Com `https://IP:porta/mcp` ou outro nome, o `resource` não bate e o SDK do cliente aborta. O painel "Conectar um agente" mostra só a URL canônica e avisa disso.
- Se o domínio do painel mudar, as conexões OAuth existentes deixam de renovar, porque `resource` e `issuer` mudam, e precisam ser refeitas. O README registra isso.

### Endpoints

| Método e caminho | Função |
|---|---|
| `GET /.well-known/oauth-protected-resource` e `.../oauth-protected-resource/mcp` | RFC 9728: `resource`, `authorization_servers: [origem]`, `bearer_methods_supported: ["header"]`. Serve com `sdkauth.ProtectedResourceMetadataHandler` do go-sdk. |
| `GET /.well-known/oauth-authorization-server` | RFC 8414: `issuer`, `authorization_endpoint` (`/oauth/authorize`), `token_endpoint` (`/oauth/token`), `registration_endpoint` (`/oauth/register`), `response_types_supported: ["code"]`, `grant_types_supported: ["authorization_code","refresh_token"]`, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`, `authorization_response_iss_parameter_supported: true`. Usa os tipos de `oauthex`. |
| `POST /oauth/register` | RFC 7591, público. Ver "Registro". |
| `GET /oauth/authorize` | Valida o pedido e redireciona (302) para a página do painel `/autorizar/?req=<id>`. |
| `GET /api/oauth/requests/{id}` | Sessão. Devolve o que a tela mostra: nome do cliente, destino do retorno e data do registro. |
| `POST /api/oauth/requests/{id}/approve` | Sessão, CSRF e código 2FA no corpo. Devolve `{redirect}` com `code`, `state` e `iss`. |
| `POST /api/oauth/requests/{id}/deny` | Sessão e CSRF. Devolve `{redirect}` com `error=access_denied`, `state` e `iss`. |
| `POST /oauth/token` | Concessões `authorization_code` e `refresh_token`. Ver "Tokens". |

- `/oauth/*` e `/.well-known/*` não usam cookie nem CSRF e não respondem CORS: não há cliente de navegador.
- `approve` e `deny` respondem JSON e a página navega com `window.location.assign`. Um 302 vindo de um formulário seria bloqueado pelo `form-action 'self'` da CSP para esquemas como `cursor://`.

### Registro (DCR)

- O registro é tolerante (RFC 7591 §3.2.1):
  - ignora campos desconhecidos (`scope`, `client_uri`, `logo_uri`, `software_*`...);
  - de `grant_types` mantém só `authorization_code` e `refresh_token`; se nenhum sobrar, registra os dois;
  - força `token_endpoint_auth_method: none` e `response_types: ["code"]`;
  - sem `client_name`, usa "Agente sem nome"; o nome tem caracteres de controle removidos e até 64 caracteres;
  - responde com os metadados efetivamente registrados.
- O único motivo de recusa é `redirect_uris`: vazio, mais de 5, ou algum fora da lista. Nesses casos responde 400 `invalid_redirect_uri`, e o URI recusado vai para o log do servidor (sem dados do usuário), para revelar clientes reais que a lista não cobre.
- **Lista de retornos permitidos**:
  - loopback: `http://127.0.0.1`, `http://localhost` e `http://[::1]`, com qualquer porta e qualquer caminho, sem query nem fragmento;
  - `https://vscode.dev/redirect` e `https://insiders.vscode.dev/redirect`, exatos;
  - o retorno OAuth do Cursor, como **um único padrão exato** (esquema, autoridade e caminho; só o segmento com o nome do servidor pode variar). O valor é capturado de um registro real do Cursor durante a implementação. Até lá o Cursor usa token manual.
  - **Nunca** se aceitam `vscode://`, `vscode-insiders://` ou esquemas genéricos de editor. Neles o código iria para o handler de qualquer extensão instalada, e uma extensão que registre ou repasse a query entregaria o acesso.
  - Todo o resto é recusado. A lista fica em código, num só lugar e testada.
- O `client_id` é aleatório, com 32 bytes. Não há segredo de cliente: o cliente é público e o PKCE é obrigatório.
- Limites: 10 registros por hora por IP (IPv6 agrupado por /64). No máximo 200 clientes que nunca foram autorizados: ao atingir o teto, o mais antigo deles é apagado, sem recusar o novo. Os clientes nunca autorizados expiram em 30 dias. Clientes com conexão ativa não expiram.
- O registro não é auditado, para não virar ruído. A autorização é.

### Autorização

- `GET /oauth/authorize` exige `response_type=code`, um `client_id` conhecido, `redirect_uri` registrado, `code_challenge` com `code_challenge_method=S256` e `state`. O `resource`, quando vier, tem de ser igual à origem mais `/mcp`.
- No loopback, a porta do `redirect_uri` pode variar em relação à registrada (RFC 8252 §7.3). Esquema, host e caminho precisam bater exatamente. Nos demais retornos, a comparação é exata.
- Se `client_id` ou `redirect_uri` forem inválidos, o painel mostra uma página de erro e **nunca redireciona**. Erros nos demais parâmetros voltam ao `redirect_uri` com `error`, `state` e `iss`.
- O pedido válido fica guardado em memória por 10 min, com id de 32 bytes, e o navegador vai para `/autorizar/?req=<id>`. Os parâmetros não passam pela página. A memória guarda no máximo 256 pedidos (o mais antigo sai) e o `/oauth/authorize` tem limite por IP (IPv6 por /64).
- `scope` é ignorado: não há escopos, e o acesso é total.
- `resource` é comparado depois de normalizado: esquema e host em minúsculas, sem a porta padrão e sem a barra final. O código e o token ficam presos ao valor canônico.
- Ao reiniciar, os pedidos pendentes se perdem e o usuário recomeça. Isso é aceitável.

### Tela de consentimento (`/autorizar/`)

- Sem sessão, vai para `/login/?next=/autorizar/?req=...` e volta depois do login. O suporte a `next` no login é novo e aceita **só** o padrão `^/autorizar/\?req=[A-Za-z0-9_-]{43}$`. Qualquer outro valor é ignorado e o login segue para `/`. Isso fecha os desvios do tipo `/\evil.com`, tabulação e quebra de linha.
- Mostra:
  - "**<client_name>** quer acesso total a este painel", com a nota "nome informado pelo próprio aplicativo";
  - o **destino do retorno**, em destaque e em linguagem simples: "este computador (localhost:PORTA)", "Cursor", "VS Code" ou "VS Code (vscode.dev)";
  - o aviso de que o acesso equivale ao administrador;
  - a validade (90 dias por padrão, ou 30 dias; sem "Nunca");
  - o campo do código 2FA e os botões Autorizar e Recusar.
- Nada é aprovado automaticamente, nem para um cliente já autorizado antes.
- O 2FA usa `svc` e o limitador do login, como as demais confirmações por sessão.
- A tela herda `frame-ancestors 'none'` e `X-Frame-Options: DENY`, que já se aplicam a todas as respostas.
- As rotas `/api/oauth/requests/*` são montadas em `mountSessionOnly`, nunca em `mountCore`. Do contrário, um token aprovaria novas conexões pelo despacho do MCP. Um teste confirma que um pedido por token recebe recusa.
- O 2FA logo depois do login: o código tem proteção contra reuso, então quem acabou de entrar precisa do **próximo** código. Decisão do usuário: manter o 2FA em toda autorização, sem exceção. Quando a sessão foi criada há menos de 2 min, a tela avisa "Você acabou de usar um código no login; aguarde o próximo". O erro `INVALID_CODE` nesse caso repete o aviso.

### Tokens

- **Código de autorização:** 32 bytes, guardado só como hash, vale 60 s e uma única vez. Fica preso a `client_id`, `redirect_uri`, `code_challenge`, `resource` e usuário. Se o mesmo código for usado de novo, a conexão que ele gerou é revogada (RFC 6749 §4.1.2).
- **Conexão:** cada aprovação cria uma linha em `api_tokens`, com nome único gerado a partir de `client_name` (com sufixo se o nome já existir). A linha guarda o hash do token de acesso atual, o hash do refresh atual e o do anterior, o `client_id`, a validade do acesso e a validade absoluta (`expires_at`, 30 ou 90 dias). Isso exige uma migração com colunas novas em `api_tokens` e uma tabela `oauth_clients`. Assim `RequireToken`, a auditoria (`via token`), a lista e a revogação continuam valendo sem caminho paralelo.
- **Token de acesso:** formato `kg_` como hoje, válido por 1 h. `Validate` passa a recusar o acesso vencido mesmo antes de `expires_at`.
- **Refresh:** prefixo `kgr_`, aceito só em `/oauth/token`, rotativo; o `client_id` do pedido tem de ser o da conexão. Cada uso emite um par novo. O refresh anterior continua aceito por **60 s** depois da rotação, vindo do mesmo cliente, e devolve um par novo: isso cobre resposta perdida e processos concorrentes. Fora dessa janela, a reapresentação do anterior revoga a conexão e é auditada (`token.refresh_reuse`). Nada é emitido além de `expires_at`.
- **Validade:** escolhida na tela de consentimento, 30 ou 90 dias (padrão 90), e enviada como `ttlDays` no corpo do `approve`.
- **`/oauth/token`:** form-urlencoded; erros no formato RFC 6749 (`invalid_grant`, `invalid_request`, `invalid_client`, `unsupported_grant_type`); `Cache-Control: no-store`; limitador por IP próprio (`oauth:`+ip). Confere o PKCE S256 em tempo constante.
- **Auditoria:** `token.oauth_authorized` (cliente, destino, validade), `token.oauth_denied` e `token.refresh_reuse`. A revogação usa o evento atual.

### Mudanças no `/mcp`

- Quando a origem está configurada, a resposta 401 leva `WWW-Authenticate: Bearer resource_metadata="https://<domínio>/.well-known/oauth-protected-resource/mcp"`. Dá para usar `RequireBearerTokenOptions.ResourceMetadataURL` do SDK ou escrever no próprio `RequireToken`, que responde antes.
- O limitador `token:` só conta falha quando o hash é **desconhecido**, ou seja, uma tentativa de adivinhar. Não contam como falha: pedido sem `Authorization` (a sondagem de todo cliente OAuth), nem token cujo hash corresponde a uma linha vencida ou revogada, inclusive o token de acesso anterior à rotação. Esses recebem 401 com `WWW-Authenticate`. Sem isso, a expiração a cada hora, várias janelas atrás do mesmo IP ou um cliente revogado insistindo bloqueariam o IP por 15 min, inclusive para os tokens manuais e o 2FA. Guardar o hash do acesso anterior serve para isso.

### Web

- Nova página `/autorizar/`, fora do layout com menu lateral.
- Configurações → Tokens de API ganha um painel **Conectar um agente** no topo, que não precisa de token, com as mesmas abas por cliente:
  - Claude Code: `claude mcp add --transport http korego <url>`;
  - Cursor: link e JSON só com `url`;
  - VS Code: link e `code --add-mcp` só com `url`;
  - Codex: `url` e depois `codex mcp login korego`;
  - Gemini: `httpUrl`;
  - Windsurf: `serverUrl`.
  - O texto diz que o navegador vai abrir para autorizar.
- O diálogo "Token criado" mantém as abas com o cabeçalho, para quem prefere o token manual. Já está implementado e sem commit, e entra nesta branch.
- Na lista, as conexões OAuth aparecem com o rótulo "OAuth" e o cliente. O "Último uso" e a revogação funcionam igual.
- O README ganha uma seção de conexão por OAuth.

## Modelo de ameaças (STRIDE resumido)

| Ameaça | Controle |
|---|---|
| Phishing: um cliente registrado com nome falso envia o código para o atacante | Retorno restrito a loopback e editores conhecidos; destino em destaque; 2FA a cada aprovação. Resíduo: malware na máquina do usuário receberia o código, mas nesse caso a máquina já está comprometida. |
| Roubo do código no retorno | PKCE S256 obrigatório; código de 60 s e uso único; preso ao `redirect_uri` e ao cliente; `iss` na resposta (RFC 9207, contra mix-up). |
| Redirecionamento aberto pelo `/oauth/authorize` | `client_id` e `redirect_uri` inválidos nunca redirecionam; `next` do login só aceita caminho relativo. |
| Clickjacking na tela de consentimento | `frame-ancestors 'none'` e `X-Frame-Options: DENY` (já globais). |
| CSRF na aprovação | Aprovação por POST com sessão, CSRF e 2FA; id do pedido de 32 bytes. |
| Envenenamento de metadados pelo `Host` | Origem tirada da configuração do painel. |
| Token vazado | Acesso de 1 h; refresh rotativo com detecção de reuso; validade absoluta de no máximo 90 dias; revogação no painel. Resíduo: uma conexão pode criar, pelo MCP e com 2FA, um token manual sem validade, e esse token sobrevive à revogação da conexão. |
| Abuso do registro público e do `/oauth/authorize` | Limite por IP (IPv6 por /64); teto de clientes com despejo do mais antigo; teto de 256 pedidos pendentes. |
| Esquema de editor entregando o código a uma extensão | Nada de `vscode://` ou esquemas genéricos; Cursor com um único padrão exato. |
| Força bruta no 2FA ou no token | Limitadores atuais (login e `token:`), mais o `oauth:` no `/oauth/token`. |
| Negação de serviço pelo limitador nas sondagens | Pedido sem token não conta. |
| Repúdio | Auditoria da autorização, da recusa e do reuso de refresh, com o nome do cliente. |

Risco aceito: `https://vscode.dev/redirect` é uma página da Microsoft que repassa o retorno ao VS Code local. Confiar nela é confiar na Microsoft.

## O que fica para o construtor

- Nomes de tipos, arquivos e da migração; como a memória de pedidos pendentes e a limpeza de clientes são agendadas.
- Se o 401 usa a opção do SDK ou é escrito em `RequireToken`.
- O texto exato da tela, seguindo o tom do painel.
- Testes: a lista de retornos (aceitos e recusados, incluindo `http://localhost.evil.com`, `http://127.0.0.1.nip.io`, `https://vscode.dev/redirect/../x`, userinfo `http://a@127.0.0.1`, `vscode://qualquer.extensao/x` e esquemas desconhecidos); registro com campos extras e `grant_types` com device_code aceito; token vencido ou revogado que não conta no limitador; refresh anterior aceito dentro de 60 s e revogando depois; `next` com `/\evil.com` e tabulação; rotas de aprovação recusadas por token; PKCE errado; código reusado; refresh reusado; `resource` diferente; origem não configurada; sondagem sem token que não bloqueia; `next` malicioso.
- Capturar os pedidos de registro reais de Claude Code, VS Code, Cursor, Codex e Gemini CLI (o retorno do Cursor é o mais importante) e conferir a lista de retornos contra eles.
- Um teste ponta a ponta com o cliente OAuth do go-sdk (`auth.AuthorizationCodeHandler`) contra o servidor de teste, simulando a aprovação. Se possível, também com `claude mcp add` real, como no e2e anterior.
