"use client";

import { useApps, type AppView } from "./apps";
import { useContainers, type Container } from "./containers";
import { useDomains, type Domain, type StaticRoot } from "./domains";

/**
 * Sites que rodam na VPS sem serem projetos do KoreGO: stacks do docker
 * compose, containers avulsos, pastas estáticas e endereços soltos. Montados
 * na hora a partir dos domínios e dos containers; nada disso é gravado.
 */
export type SiteKind = "compose" | "avulso" | "estatico" | "endereco";

export interface DetectedSite {
  kind: SiteKind;
  /** Projeto do compose, nome do container, hostname ou endereço. */
  name: string;
  domains: Domain[];
  containers: Container[];
  /** Pastas servidas (só nos estáticos). */
  roots: StaticRoot[];
}

export const SITE_KIND_LABEL: Record<SiteKind, string> = {
  compose: "Docker compose",
  avulso: "Container avulso",
  estatico: "Estático",
  endereco: "Endereço",
};

const KINDS: readonly SiteKind[] = ["compose", "avulso", "estatico", "endereco"];

export function parseSiteKind(v: string | null): SiteKind | null {
  return v && (KINDS as readonly string[]).includes(v) ? (v as SiteKind) : null;
}

export const siteHref = (s: Pick<DetectedSite, "kind" | "name">) =>
  `/projetos/site/?tipo=${s.kind}&nome=${encodeURIComponent(s.name)}`;

/** Porta de um endereço "host:porta" (ou ":porta"); null se não houver. */
function portOf(address: string | undefined): number | null {
  const m = /:(\d+)$/.exec(address ?? "");
  return m ? Number(m[1]) : null;
}

/** Portas para onde o domínio manda tráfego (rotas address). */
function domainPorts(d: Domain): number[] {
  const ports = new Set<number>();
  for (const r of d.routes ?? []) {
    if (r.targetType !== "address") continue;
    const p = portOf(r.address);
    if (p !== null) ports.add(p);
  }
  return [...ports];
}

/** Portas publicadas no host que o proxy alcança (loopback ou todas as interfaces). */
function containerPorts(c: Container): number[] {
  return (c.ports ?? [])
    .filter((p) => p.publicPort > 0 && ["", "0.0.0.0", "127.0.0.1", "::", "::1"].includes(p.ip))
    .map((p) => p.publicPort);
}

const groupKey = (c: Container) => (c.project ? `compose:${c.project}` : `avulso:${c.name}`);

/** Um domínio que aponta para uma app do KoreGO não entra na lista. */
const pointsToApp = (d: Domain) => (d.routes ?? []).some((r) => r.targetType === "app");

/** Grupo dos containers de stacks já adotadas: seus domínios também saem da lista. */
const ADOPTED = "adotado";

/**
 * Monta os sites da VPS. `apps` tira da lista as stacks adotadas (apps
 * compose-local) e os domínios que chegam nelas pela porta.
 */
export function detectSites(domains: Domain[], containers: Container[], apps: AppView[] = []): DetectedSite[] {
  const groups = new Map<string, DetectedSite>();
  const byPort = new Map<number, string>();
  const adopted = new Set(
    apps.filter((a) => a.buildType === "compose-local" && a.composeProject).map((a) => a.composeProject),
  );

  for (const c of containers) {
    if (c.managed) continue;
    if (c.project && adopted.has(c.project)) {
      for (const p of containerPorts(c)) if (!byPort.has(p)) byPort.set(p, ADOPTED);
      continue;
    }
    const key = groupKey(c);
    let g = groups.get(key);
    if (!g) {
      g = c.project
        ? { kind: "compose", name: c.project, domains: [], containers: [], roots: [] }
        : { kind: "avulso", name: c.name, domains: [], containers: [], roots: [] };
      groups.set(key, g);
    }
    g.containers.push(c);
    for (const p of containerPorts(c)) if (!byPort.has(p)) byPort.set(p, key);
  }

  const loose: DetectedSite[] = [];
  for (const d of domains) {
    if (d.isPanel || pointsToApp(d)) continue;
    const key = domainPorts(d)
      .map((p) => byPort.get(p))
      .find((k) => k !== undefined);
    if (key === ADOPTED) continue;
    if (key) {
      groups.get(key)!.domains.push(d);
      continue;
    }
    const roots = d.staticRoots ?? [];
    const rootRoute = (d.routes ?? []).find((r) => r.path === "/");
    if (rootRoute?.targetType === "static" || (roots.length > 0 && domainPorts(d).length === 0)) {
      loose.push({ kind: "estatico", name: d.hostname, domains: [d], containers: [], roots });
    } else if (domainPorts(d).length > 0) {
      loose.push({ kind: "endereco", name: d.hostname, domains: [d], containers: [], roots });
    }
  }

  return [...groups.values(), ...loose].sort((a, b) => a.name.localeCompare(b.name, "pt-BR"));
}

/** Versão atual de uma pasta: o último trecho do caminho real (ex.: 20261115133041-9faa1c9). */
export function rootVersion(r: StaticRoot): string {
  if (!r.exists || !r.resolved) return "";
  const parts = r.resolved.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] ?? "";
}

export function findSite(sites: DetectedSite[], kind: SiteKind, name: string): DetectedSite | undefined {
  return sites.find((s) => s.kind === kind && s.name === name);
}

export function useDetectedSites() {
  const domains = useDomains();
  const containers = useContainers();
  const apps = useApps();
  // Sem Docker, os estáticos ainda aparecem; o erro dos containers vira aviso.
  const containersReady = containers.data !== undefined || containers.isError;
  // Sem a lista de apps, nenhuma stack é tida como adotada.
  const appsReady = apps.data !== undefined || apps.isError;
  const isPending = domains.isPending || !containersReady || !appsReady;
  const data =
    domains.data && containersReady && appsReady
      ? detectSites(domains.data, containers.data ?? [], apps.data ?? [])
      : undefined;
  return {
    data,
    isPending: isPending && !domains.isError,
    isError: domains.isError,
    error: domains.error,
    containersError: containers.error,
    isFetching: domains.isFetching || containers.isFetching,
    refetch: () => {
      void domains.refetch();
      void containers.refetch();
      void apps.refetch();
    },
  };
}
