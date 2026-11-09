"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

/** Certificado próprio guardado (GET /api/certificates). Nunca traz a chave. */
export interface Certificate {
  id: number;
  name: string;
  issuer: string;
  names: string[];
  notAfter: string;
  /** SHA-256 do certificado folha. */
  fingerprint: string;
  /** Hostnames dos domínios que usam o certificado. */
  usedBy: string[];
  /** Vence em até 30 dias. */
  expiresSoon: boolean;
}

/** Corpo de POST /api/certificates: o PEM colado (cadeia + chave) ou um caminho no servidor. */
export type CertificateInput = { name: string; pem: string } | { name: string; path: string };

export function useCertificates() {
  return useQuery({
    queryKey: ["certificates"],
    queryFn: () => api<{ certificates: Certificate[] }>("/api/certificates").then((r) => r.certificates),
  });
}

export function useAddCertificate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CertificateInput) =>
      api<{ certificate: Certificate }>("/api/certificates", { method: "POST", body: input }).then(
        (r) => r.certificate,
      ),
    onSuccess: (c) => {
      qc.setQueryData<Certificate[]>(["certificates"], (old) => (old ? [...old.filter((o) => o.id !== c.id), c] : [c]));
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: ["certificates"] }),
  });
}

export function useDeleteCertificate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<void>(`/api/certificates/${id}`, { method: "DELETE" }),
    onSettled: () => void qc.invalidateQueries({ queryKey: ["certificates"] }),
  });
}
