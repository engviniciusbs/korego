import type { Metadata } from "next";
import type { ReactNode } from "react";
import {
  Alert,
  Badge,
  Button,
  Input,
  Label,
  Panel,
  Spinner,
  type BadgeVariant,
  type ButtonVariant,
} from "@/components/ui";

export const metadata: Metadata = {
  title: "Guia de estilo · KoreGO",
  robots: { index: false },
};

const swatches: Array<{ token: string; hex: string }> = [
  { token: "bg", hex: "#08080a" },
  { token: "surface", hex: "#0f0f12" },
  { token: "surface-2", hex: "#15151a" },
  { token: "surface-3", hex: "#1c1c22" },
  { token: "line", hex: "#23232a" },
  { token: "line-strong", hex: "#34343d" },
  { token: "fg", hex: "#e6e6ea" },
  { token: "fg-muted", hex: "#a0a0ab" },
  { token: "fg-subtle", hex: "#7c7c88" },
  { token: "accent", hex: "#6d3df5" },
  { token: "accent-text", hex: "#a990ff" },
  { token: "success", hex: "#2fbf71" },
  { token: "warning", hex: "#e5b53a" },
  { token: "danger", hex: "#f0524f" },
  { token: "info", hex: "#4aa8e8" },
];

const buttonVariants: ButtonVariant[] = ["primary", "secondary", "ghost", "danger"];
const badgeVariants: BadgeVariant[] = ["neutral", "accent", "success", "warning", "danger", "info"];

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="space-y-3">
      <h2
        id={id}
        className="border-b border-line pb-2 font-mono text-[11px] font-medium uppercase tracking-[0.12em] text-fg-subtle"
      >
        {title}
      </h2>
      {children}
    </section>
  );
}

export default function StyleguidePage() {
  return (
    <div className="flex min-h-full flex-1 flex-col">
      <header className="flex h-12 items-center justify-between border-b border-line bg-surface px-4">
        <span className="font-mono text-sm font-semibold tracking-tight">
          <span translate="no">
            Kore<span className="text-accent-text">GO</span>
          </span>
          <span className="ml-3 text-fg-subtle">/ guia de estilo</span>
        </span>
        <Badge variant="accent" size="sm" dot>
          Somente escuro
        </Badge>
      </header>

      <main className="mx-auto w-full max-w-5xl flex-1 space-y-10 px-4 py-8">
        <div className="space-y-1">
          <h1 className="text-xl font-semibold tracking-tight text-balance">
            Sistema visual do painel
          </h1>
          <p className="max-w-prose text-sm text-fg-muted">
            Superfícies quase pretas em camadas, bordas finas, acento violeta e cantos 100% retos.
            Rótulos e dados técnicos em fonte monoespaçada.
          </p>
        </div>

        <Section id="sg-cores" title="Cores">
          <ul className="grid grid-cols-2 gap-px border border-line bg-line sm:grid-cols-3 lg:grid-cols-5">
            {swatches.map((s) => (
              <li key={s.token} className="flex items-center gap-3 bg-surface p-3">
                <span
                  aria-hidden="true"
                  className="size-8 shrink-0 border border-line-strong"
                  style={{ backgroundColor: s.hex }}
                />
                <span className="min-w-0" translate="no">
                  <span className="block truncate font-mono text-xs text-fg">{s.token}</span>
                  <span className="block font-mono text-[11px] text-fg-subtle">{s.hex}</span>
                </span>
              </li>
            ))}
          </ul>
        </Section>

        <Section id="sg-botoes" title="Botões">
          <div className="space-y-3">
            {(["sm", "md", "lg"] as const).map((size) => (
              <div key={size} className="flex flex-wrap items-center gap-2">
                <span className="w-8 font-mono text-[11px] text-fg-subtle">{size}</span>
                {buttonVariants.map((variant) => (
                  <Button key={variant} variant={variant} size={size}>
                    {variant === "danger" ? "Remover" : "Implantar"}
                  </Button>
                ))}
              </div>
            ))}
            <div className="flex flex-wrap items-center gap-2">
              <span className="w-8 font-mono text-[11px] text-fg-subtle">est.</span>
              <Button variant="primary" loading>
                Implantando…
              </Button>
              <Button variant="secondary" disabled>
                Desabilitado
              </Button>
            </div>
          </div>
        </Section>

        <Section id="sg-campos" title="Campos">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="sg-host" required>
                Domínio
              </Label>
              <Input
                id="sg-host"
                name="dominio"
                type="text"
                autoComplete="off"
                spellCheck={false}
                placeholder="app.exemplo.com…"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="sg-ip">Endereço IP</Label>
              <Input
                id="sg-ip"
                name="ip"
                type="text"
                inputMode="decimal"
                autoComplete="off"
                spellCheck={false}
                mono
                defaultValue="203.0.113.10"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="sg-porta">Porta</Label>
              <Input
                id="sg-porta"
                name="porta"
                type="number"
                inputMode="numeric"
                mono
                invalid
                defaultValue="99999"
                aria-describedby="sg-porta-erro"
              />
              <p id="sg-porta-erro" className="text-xs text-danger">
                A porta deve estar entre 1 e 65535.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="sg-filtro">Filtro (compacto)</Label>
              <Input
                id="sg-filtro"
                name="filtro"
                type="search"
                size="sm"
                autoComplete="off"
                placeholder="Filtrar serviços…"
              />
            </div>
          </div>
        </Section>

        <Section id="sg-badges" title="Badges">
          <div className="flex flex-wrap items-center gap-2">
            {badgeVariants.map((v) => (
              <Badge key={v} variant={v} dot>
                {v}
              </Badge>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="success" size="sm" dot>
              Rodando
            </Badge>
            <Badge variant="warning" size="sm" dot>
              Reiniciando
            </Badge>
            <Badge variant="danger" size="sm" dot>
              Parado
            </Badge>
            <Badge size="sm">v0.1.0</Badge>
          </div>
        </Section>

        <Section id="sg-alertas" title="Alertas">
          <div className="space-y-2">
            <Alert variant="info" title="Atualização disponível">
              Uma nova versão do KoreGO pode ser instalada.
            </Alert>
            <Alert variant="success" title="Implantação concluída">
              O serviço está respondendo em app.exemplo.com.
            </Alert>
            <Alert variant="warning" title="Disco em 85%">
              Libere espaço em /var/lib/korego antes da próxima implantação.
            </Alert>
            <Alert variant="danger" title="Falha ao iniciar o contêiner">
              O processo terminou com código 137.
            </Alert>
          </div>
        </Section>

        <Section id="sg-paineis" title="Painéis e spinner">
          <div className="grid gap-4 md:grid-cols-3">
            <Panel
              headingLevel={3}
              title="Servidor"
              description="203.0.113.10"
              actions={
                <Badge variant="success" size="sm" dot>
                  Online
                </Badge>
              }
            >
              <dl translate="no" className="grid grid-cols-2 gap-y-2 font-mono text-xs">
                <dt className="text-fg-subtle">CPU</dt>
                <dd className="text-right tabular-nums">12%</dd>
                <dt className="text-fg-subtle">Memória</dt>
                <dd className="text-right tabular-nums">1,4 / 4 GB</dd>
              </dl>
            </Panel>
            <Panel
              headingLevel={3}
              variant="accent"
              title="Implantação"
              actions={
                <Button size="sm" variant="primary">
                  Implantar
                </Button>
              }
            >
              <div className="flex items-center gap-2 text-sm text-fg-muted">
                <Spinner size="sm" label="Implantação em andamento" />
                <span>Construindo imagem…</span>
              </div>
            </Panel>
            <Panel headingLevel={3} variant="danger" size="sm" title="Zona de perigo">
              <p className="mb-3 text-xs text-fg-muted">Remove o serviço e seus volumes.</p>
              <Button size="sm" variant="danger">
                Remover serviço
              </Button>
            </Panel>
          </div>
          <div className="flex items-center gap-4 text-fg-muted">
            <Spinner size="sm" />
            <Spinner size="md" />
            <Spinner size="lg" className="text-accent-text" />
          </div>
        </Section>
      </main>
    </div>
  );
}
