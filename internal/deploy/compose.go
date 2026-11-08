package deploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/engviniciusbs/korego/internal/store"
	"go.yaml.in/yaml/v3"
)

type composeService struct {
	Ports  []string          `yaml:"ports"`
	Labels map[string]string `yaml:"labels"`
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// ComposeOverride gera o arquivo override que publica o serviço só em
// 127.0.0.1:<hostPort> e o marca com os labels do KoreGO.
func ComposeOverride(app store.App, hostPort int) ([]byte, error) {
	if !ValidServiceName(app.ComposeService) {
		return nil, fmt.Errorf("serviço compose inválido: %q", app.ComposeService)
	}
	if !ValidSlug(app.Slug) {
		return nil, fmt.Errorf("slug inválido: %q", app.Slug)
	}
	if !ValidPort(app.InternalPort) || !ValidPort(hostPort) {
		return nil, fmt.Errorf("porta inválida (interna %d, host %d)", app.InternalPort, hostPort)
	}
	f := composeFile{Services: map[string]composeService{
		app.ComposeService: {
			Ports:  []string{"127.0.0.1:" + strconv.Itoa(hostPort) + ":" + strconv.Itoa(app.InternalPort)},
			Labels: map[string]string{"korego.managed": "true", "korego.app": app.Slug},
		},
	}}
	return yaml.Marshal(f)
}

// ComposeUp sobe o projeto korego-<slug> com o compose do repositório (em dir)
// mais o override.
func ComposeUp(ctx context.Context, r Runner, app store.App, dir, overridePath string, out io.Writer) error {
	if !ValidSlug(app.Slug) {
		return fmt.Errorf("slug inválido: %q", app.Slug)
	}
	if !ValidRelPath(app.ComposePath) {
		return fmt.Errorf("caminho do compose inválido: %q", app.ComposePath)
	}
	err := r.Run(ctx, Cmd{Name: "docker", Args: []string{
		"compose", "-p", "korego-" + app.Slug,
		"-f", filepath.Join(dir, filepath.FromSlash(app.ComposePath)),
		"-f", overridePath,
		"up", "-d", "--remove-orphans",
	}}, out)
	if err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	return nil
}
