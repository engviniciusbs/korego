import Link from "next/link";
import { AuthFrame } from "@/components/shell/auth-frame";
import { focusRing } from "@/components/ui/focus";

export default function NotFound() {
  return (
    <AuthFrame eyebrow="Erro 404" title="Página não encontrada">
      <p className="text-[13px] text-fg-muted">O endereço acessado não existe neste painel.</p>
      <Link
        href="/"
        className={`mt-5 inline-flex h-8 items-center border border-accent bg-accent px-3 text-[13px] font-medium text-accent-fg hover:bg-accent-hover ${focusRing}`}
      >
        Voltar para o painel
      </Link>
    </AuthFrame>
  );
}
