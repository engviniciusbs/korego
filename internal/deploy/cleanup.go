package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/store"
)

// Cleanup remove as imagens korego/<slug> que não estão em keep. Imagem em uso
// (ErrConflict) é ignorada com uma linha em out; outros erros são juntados e
// devolvidos depois de tentar todas.
func Cleanup(ctx context.Context, d docker.Client, app store.App, keep []string, out io.Writer) error {
	imgs, err := d.Images(ctx, "korego/"+app.Slug)
	if err != nil {
		return fmt.Errorf("listar imagens: %w", err)
	}
	var errs []error
	for _, img := range imgs {
		if slices.Contains(keep, img.Ref) {
			continue
		}
		switch err := d.RemoveImage(ctx, img.Ref); {
		case err == nil:
			fmt.Fprintf(out, "imagem antiga removida: %s\n", img.Ref)
		case errors.Is(err, docker.ErrConflict):
			fmt.Fprintf(out, "imagem em uso, mantida: %s\n", img.Ref)
		default:
			errs = append(errs, fmt.Errorf("remover %s: %w", img.Ref, err))
		}
	}
	return errors.Join(errs...)
}
