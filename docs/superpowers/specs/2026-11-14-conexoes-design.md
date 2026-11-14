# Tela "Conexões" (antiga Tokens de API) — design curto

## Intenção
A tela atual de conexão de agentes usa abas, não mostra logos e tem espaçamento ruim. O usuário quer clicar num botão e já ter o cliente (Cursor, VS Code, Claude Code etc.) integrado ao MCP. Quando é o agente que inicia a conexão, a autenticação OAuth já resolve. "Tokens de API" deixa de ser o nome: o que a pessoa faz ali é conectar agentes.

## Objetivos
- Sidebar e título da página passam a se chamar **Conexões**, no endereço `/configuracoes/conexoes/`. O endereço antigo `/configuracoes/tokens/` redireciona para o novo.
- A seção **Conectar um agente** tem uma grade de cartões, um por cliente, cada um com logo oficial, tipo (Editor ou Terminal), **uma ação principal** e uma linha dizendo o que acontece depois:
  - Cursor e VS Code: botão "Adicionar ao …", que abre o link de instalação só com a URL. O editor cuida da autorização pelo navegador.
  - Claude Code, Codex e Gemini CLI: comando de terminal com botão Copiar.
  - Windsurf: botão "Copiar configuração", com o aviso "ainda não verificado com OAuth".
  - Em cada cartão, "Configuração manual" fica recolhida.
- No topo da seção, a URL do servidor com Copiar. No rodapé, um texto para outros clientes.
- A seção **Agentes conectados** lista as conexões OAuth (com o selo OAUTH) e os tokens manuais, com o botão "Novo token".
- O diálogo "Token criado" usa a mesma grade, com o token embutido nos links e comandos, no lugar das abas.
- Layout responsivo: 3 colunas, 2 em telas médias e 1 no celular, com margens de 16px.

## Não objetivos
- Deep link para Claude Code, Codex, Gemini e Windsurf: esses clientes não oferecem um.
- Mudanças no backend de OAuth.

## Abordagens
1. **Cartões com ação principal (escolhida pelo usuário, no mockup `conexoes-v3`).**
2. Abas por cliente (o que existe hoje). Recusada: esconde as opções e foi achada feia.

## Decisões
- Logos coloridos por padrão, como no mockup.
- Fontes dos logos:
  - Simple Icons (CC0) para Claude, Cursor, OpenAI (usado no Codex), Gemini e Windsurf;
  - codicon oficial da Microsoft para o VS Code.
- Os SVGs ficam embutidos no código, sem CDN, por causa da CSP `'self'`.
- Comandos:
  - Claude Code: `claude mcp add --transport http -s user korego <url>`;
  - Codex: `codex mcp add korego --url <url>` seguido de `codex mcp login korego`;
  - Gemini: `gemini mcp add --transport http korego <url>`.
  - A sintaxe do Codex e do Gemini é conferida na documentação durante a implementação.
- A auditoria e a API (`/api/tokens`) não mudam de nome. A mudança é só de interface.
- Textos em pt-BR como no mockup.

## Fica para o construtor
Estrutura de componentes; as cores exatas de hover seguem os tokens do tema. Atualizar os testes atuais da página e o README, que hoje cita "Configurações → Tokens de API".
