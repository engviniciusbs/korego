export interface AuditItem {
  id: number;
  userId: number | null;
  action: string;
  target: string;
  ip: string;
  metadata: Record<string, unknown>;
  createdAt: string;
}

/** Texto de detalhe de uma linha da auditoria. */
export function auditDetail(a: AuditItem): string {
  if (a.metadata?.via === "token" && typeof a.metadata.token === "string" && a.metadata.token) {
    return `token ${a.metadata.token}`;
  }
  const email = typeof a.metadata?.email === "string" ? a.metadata.email : "";
  if (email) return email;
  if (a.target) return `sessão ${a.target.slice(0, 8)}…`;
  return "—";
}
