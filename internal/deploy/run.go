package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/store"
)

// Erros da verificação de saúde (use errors.Is).
var (
	ErrContainerExited = errors.New("o container encerrou")
	ErrUnhealthy       = errors.New("o container ficou unhealthy")
	ErrHealthTimeout   = errors.New("tempo esgotado esperando o container ficar saudável")
)

// healthInterval é o intervalo entre verificações; variável só para os testes.
var healthInterval = time.Second

// containerName monta korego-<slug>-<sha7>-d<id>. O sha7 é o sufixo da tag da
// imagem depois do último ':' (ImageForApp já o produz com 7 caracteres),
// cortado em 7; sem ':', usa "nosha".
func containerName(slug string, deployID int64, image string) string {
	short := "nosha"
	if i := strings.LastIndex(image, ":"); i >= 0 && i < len(image)-1 {
		short = image[i+1:]
		if len(short) > 7 {
			short = short[:7]
		}
	}
	return fmt.Sprintf("korego-%s-%s-d%d", slug, short, deployID)
}

// StartContainer cria e inicia o container da aplicação, publicando a porta
// interna só em 127.0.0.1:<hostPort>. env é repassado sem alteração.
func StartContainer(ctx context.Context, d docker.Client, app store.App, deployID int64, image string, env []string, hostPort int) (string, error) {
	if !ValidSlug(app.Slug) {
		return "", fmt.Errorf("slug inválido: %q", app.Slug)
	}
	if !ValidImageRef(image) {
		return "", fmt.Errorf("imagem inválida: %q", image)
	}
	if !ValidPort(app.InternalPort) || !ValidPort(hostPort) {
		return "", fmt.Errorf("porta inválida (interna %d, host %d)", app.InternalPort, hostPort)
	}
	id, err := d.Run(ctx, docker.RunSpec{
		Name:  containerName(app.Slug, deployID, image),
		Image: image,
		Labels: map[string]string{
			"korego.managed": "true",
			"korego.app":     app.Slug,
			"korego.deploy":  strconv.FormatInt(deployID, 10),
		},
		Env: env,
		Ports: []docker.PortMapping{{
			HostIP: "127.0.0.1", HostPort: uint16(hostPort), ContainerPort: uint16(app.InternalPort), Proto: "tcp",
		}},
		RestartPolicy: "unless-stopped",
	})
	if err != nil {
		return "", fmt.Errorf("iniciar container: %w", err)
	}
	return id, nil
}

// WaitHealthy espera o container ficar saudável. A cada verificação: exited ou
// dead → ErrContainerExited (com o código de saída); Health unhealthy →
// ErrUnhealthy; healthy → ok; sem healthcheck, ok quando dial("127.0.0.1:<porta>")
// funciona. Health starting segue esperando. Passado timeout → ErrHealthTimeout;
// ctx cancelado → ctx.Err().
//
// Limitação (v1): sem healthcheck, o Docker-in-Docker e o Docker Desktop
// aceitam TCP na porta publicada antes de o app ouvir; o dial pode dar ok cedo.
func WaitHealthy(ctx context.Context, d docker.Client, id string, hostPort int, timeout time.Duration, dial func(addr string) error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(healthInterval)
	defer tick.Stop()
	addr := "127.0.0.1:" + strconv.Itoa(hostPort)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		det, err := d.Inspect(ctx, id)
		if err != nil {
			return fmt.Errorf("inspecionar container: %w", err)
		}
		switch {
		case det.State == "exited" || det.State == "dead":
			return fmt.Errorf("%w (código de saída %d)", ErrContainerExited, det.ExitCode)
		case det.Health == "unhealthy":
			return fmt.Errorf("%w (status do docker: %s)", ErrUnhealthy, det.Health)
		case det.Health == "healthy":
			return nil
		case det.Health == "" && dial != nil && dial(addr) == nil:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrHealthTimeout
		case <-tick.C:
		}
	}
}
