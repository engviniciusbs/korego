/**
 * Anel de foco compartilhado: visível só para navegação por teclado.
 * `outline-hidden` mantém um contorno no modo de alto contraste (forced-colors);
 * `outline-solid` reativa o estilo, pois `outline-2` sozinho herda o `none` da base.
 */
export const focusRing =
  "outline-hidden focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";
