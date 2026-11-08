package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/engviniciusbs/korego/internal/store"
)

// ErrRailpackMissing indica que o binário railpack não está no PATH.
var ErrRailpackMissing = errors.New("railpack não está instalado")

// LookPath é injetável nos testes.
var LookPath = exec.LookPath

// Build constrói a imagem (ou, em compose, os serviços) a partir do clone em dir.
// A origem image não passa por aqui: use PullAndTag.
func Build(ctx context.Context, r Runner, app store.App, dir, tag string, out io.Writer) error {
	switch app.BuildType {
	case "dockerfile":
		if !ValidRelPath(app.DockerfilePath) {
			return errors.New("caminho do Dockerfile inválido")
		}
		return r.Run(ctx, Cmd{Name: "docker", Args: []string{"build", "-t", tag,
			"-f", filepath.Join(dir, app.DockerfilePath), dir}}, out)
	case "railpack":
		if _, err := LookPath("railpack"); err != nil {
			return ErrRailpackMissing
		}
		// Plano fora do clone: um symlink commitado no repo não pode redirecionar a escrita.
		plan := filepath.Clean(dir) + ".railpack-plan.json"
		if err := r.Run(ctx, Cmd{Name: "railpack", Args: []string{"prepare", dir, "--plan-out", plan}}, out); err != nil {
			return fmt.Errorf("railpack prepare: %w", err)
		}
		return r.Run(ctx, Cmd{Name: "docker", Args: []string{"buildx", "build",
			"--build-arg", "BUILDKIT_SYNTAX=ghcr.io/railwayapp/railpack-frontend",
			"-f", plan, "-t", tag, "--load", dir}}, out)
	case "compose":
		if !ValidRelPath(app.ComposePath) {
			return errors.New("caminho do Compose inválido")
		}
		if !ValidSlug(app.Slug) {
			return errors.New("slug inválido")
		}
		return r.Run(ctx, Cmd{Name: "docker", Args: []string{"compose", "-p", "korego-" + app.Slug,
			"-f", filepath.Join(dir, app.ComposePath), "build"}}, out)
	default:
		return fmt.Errorf("tipo de build %q não é construído aqui", app.BuildType)
	}
}
