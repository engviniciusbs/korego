import Link from "next/link";

// Placeholder até o grupo de rotas (painel) da Task 12.
export default function Home() {
  return (
    <main className="flex flex-1 flex-col items-start justify-center gap-4 px-8">
      <h1 translate="no" className="font-mono text-2xl font-semibold tracking-tight">
        Kore<span className="text-accent-text">GO</span>
      </h1>
      <Link
        href="/styleguide/"
        className="text-sm text-accent-text underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
      >
        Ver guia de estilo
      </Link>
    </main>
  );
}
