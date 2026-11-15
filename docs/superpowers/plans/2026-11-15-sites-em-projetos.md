# Sites da VPS em Projetos — plano

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (execução nativa).

**Goal:** Projetos mostra também as stacks compose, os containers avulsos e os sites estáticos que já rodam na VPS.

**Architecture:**
- **Backend:** o DTO de domínio ganha `staticRoots`.
- **Frontend:** monta os sites em `web/src/lib/sites.ts` a partir de domínios e containers.
- **Telas:** os cartões ficam em Projetos, com uma página só de leitura em `/projetos/site/`.

**Spec:** `docs/superpowers/specs/2026-11-15-sites-em-projetos-design.md`

## Global Constraints
- Nada novo no banco. Nada de editar ou remover nos cartões detectados.
- Ficam de fora o domínio do painel (`isPanel`) e os containers `managed`.
- Selos: "KoreGO", "Docker compose", "Container avulso", "Estático", "Endereço".
- Visual dos cartões igual ao `ProjectCard` atual.

## Review Focus
1. Uma pasta estática inexistente mostra o aviso, sem quebrar o cartão.
2. Um domínio `address` sem container na mesma porta vira o cartão "Endereço".
3. Um domínio com várias rotas (`/api/` → 8092 e `/` → 8091) aparece uma vez só, no cartão da stack.
4. Os containers do KoreGO (`managed`) e o domínio do painel não aparecem.
5. Um nome inexistente em `/projetos/site/` mostra "não encontrado".

### Task 1: `staticRoots` no domínio (Go)
- **Files:** `internal/api/domains.go` e `internal/api/domains_test.go`.
- **Código:**
  - `type staticRootDTO struct {Path, Root, Resolved string; Exists bool; ModifiedAt *time.Time}`;
  - `func statRoot(path, root string) staticRootDTO`, que usa `filepath.EvalSymlinks` e `os.Stat`;
  - `toDomainDTO` preenche `StaticRoots` (`json:"staticRoots"`, sempre uma lista) para cada rota `static`.
- **Teste:** um diretório temporário com `current` → `releases/x`. O resultado traz o `Resolved` terminando em `releases/x`, `Exists` verdadeiro e `ModifiedAt` preenchido. Uma raiz inexistente dá `Exists` falso.

### Task 2: `web/src/lib/sites.ts`
- **Código:**
  - `type SiteKind = "compose" | "avulso" | "estatico" | "endereco"`;
  - `interface DetectedSite {kind; name; domains: Domain[]; containers: Container[]; roots: StaticRoot[]; address?: string}`;
  - `detectSites(domains, containers): DetectedSite[]`, ordenado por nome;
  - `siteHref(s)`;
  - `useDetectedSites()`.
- **Regras:**
  - a porta do domínio vem de qualquer rota `address` do domínio;
  - o domínio se liga à stack quando um container dela publica a mesma porta;
  - uma rota "/" estática vira um cartão estático, salvo quando o domínio já entrou numa stack.
- **Testes:** os cinco itens do Review Focus, com dados iguais aos da VPS.

### Task 3: Telas
- **Projetos:** abaixo da grade KoreGO, a seção "Na VPS" com `SiteCard`. O estado vazio da página só aparece se não houver nem projetos nem sites.
- **Página `/projetos/site/`:** só leitura, com breadcrumb, domínios (link para o site e para `/dominios/`), containers (estado e link para o detalhe) e pastas (caminho, versão e data).
- **Testes de página e verificação:** `npx vitest run`, `tsc`, `eslint` e `npm run build`. Screenshot em 1280px e 390px.
