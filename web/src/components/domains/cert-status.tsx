import { Badge, type BadgeVariant } from "@/components/ui";
import { certNotAfter, type CertInfo } from "@/lib/domains";

const dateFmt = new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit", year: "numeric" });

/** "Pendente", "Ativo até dd/mm/aaaa" (ou só "Ativo" sem data) ou "Erro: <mensagem>". */
export function certLabel(cert: CertInfo): string {
  if (cert.state === "active") {
    const d = certNotAfter(cert);
    return d ? `Ativo até ${dateFmt.format(d)}` : "Ativo";
  }
  if (cert.state === "error") return `Erro: ${cert.message || "falha ao emitir o certificado"}`;
  return "Pendente";
}

const variant: Record<CertInfo["state"], BadgeVariant> = {
  active: "success",
  pending: "warning",
  error: "danger",
};

/** Estado do certificado: LED + texto. O erro vem em texto normal, pois pode ser longo. */
export function CertStatus({ cert }: { cert: CertInfo }) {
  if (cert.state === "error") {
    return (
      <span className="inline-flex items-start gap-2 text-xs text-danger [overflow-wrap:anywhere]">
        <span aria-hidden="true" className="mt-1 size-1.5 shrink-0 bg-current" />
        <span>{certLabel(cert)}</span>
      </span>
    );
  }
  return (
    <Badge size="sm" dot variant={variant[cert.state]} className="normal-case tracking-normal">
      {certLabel(cert)}
    </Badge>
  );
}
