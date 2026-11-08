package docker

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// toCreateConfig converte o RunSpec na configuração de criação do container.
// Os erros de validação nunca incluem o valor de variáveis de ambiente.
func toCreateConfig(s RunSpec) (*container.Config, *container.HostConfig, error) {
	if s.Image == "" {
		return nil, nil, errors.New("imagem não informada")
	}
	for i, e := range s.Env {
		if name, _, ok := strings.Cut(e, "="); !ok || name == "" {
			return nil, nil, fmt.Errorf("variável de ambiente %d inválida: use NOME=valor", i+1)
		}
	}

	cfg := &container.Config{Image: s.Image, Labels: s.Labels, Env: s.Env, Cmd: s.Cmd}
	host := &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}}
	if s.RestartPolicy != "" {
		host.RestartPolicy.Name = container.RestartPolicyMode(s.RestartPolicy)
	}

	if s.NetworkMode != "" {
		host.NetworkMode = container.NetworkMode(s.NetworkMode)
	}
	if s.NetworkMode != "host" {
		for _, p := range s.Ports {
			proto := p.Proto
			if proto == "" {
				proto = "tcp"
			}
			port, ok := network.PortFrom(p.ContainerPort, network.IPProtocol(proto))
			if !ok || p.ContainerPort == 0 {
				return nil, nil, fmt.Errorf("porta do container inválida: %d/%s", p.ContainerPort, proto)
			}
			b := network.PortBinding{}
			if p.HostIP != "" {
				ip, err := netip.ParseAddr(p.HostIP)
				if err != nil {
					return nil, nil, fmt.Errorf("ip do host inválido: %q", p.HostIP)
				}
				b.HostIP = ip
			}
			if p.HostPort != 0 {
				b.HostPort = strconv.Itoa(int(p.HostPort))
			}
			if cfg.ExposedPorts == nil {
				cfg.ExposedPorts = network.PortSet{}
				host.PortBindings = network.PortMap{}
			}
			cfg.ExposedPorts[port] = struct{}{}
			host.PortBindings[port] = append(host.PortBindings[port], b)
		}
	}

	for _, v := range s.Volumes {
		if v.Volume == "" || v.Target == "" {
			return nil, nil, errors.New("volume precisa de nome e destino")
		}
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: v.Volume, Target: v.Target})
	}
	return cfg, host, nil
}

// pullLine formata uma mensagem do stream de pull como "<id>: <status> <progresso>".
// Devolve "" se a mensagem não tem texto.
func pullLine(m jsonstream.Message) string {
	text := m.Status
	if text == "" {
		text = strings.TrimSpace(m.Stream)
	}
	if text == "" {
		return ""
	}
	if m.Progress != nil && m.Progress.Current > 0 {
		if m.Progress.Total > 0 {
			text += " " + humanBytes(m.Progress.Current) + "/" + humanBytes(m.Progress.Total)
		} else {
			text += " " + humanBytes(m.Progress.Current)
		}
	}
	if m.ID != "" {
		return m.ID + ": " + text
	}
	return text
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// firstRepoTag devolve a primeira tag de tags cujo repositório é repo.
func firstRepoTag(tags []string, repo string) string {
	for _, t := range tags {
		if r, _, ok := strings.Cut(t, ":"); ok && r == repo {
			return t
		}
	}
	return ""
}

// Pull baixa a imagem e repassa o progresso, já em texto, a fn. Uma mensagem
// de erro dentro do stream vira o erro devolvido.
func (l *Local) Pull(ctx context.Context, ref string, fn func(line string)) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	res, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return mapErr(err)
	}
	defer res.Close()
	for m, err := range res.JSONMessages(ctx) {
		if err != nil {
			return mapErr(err)
		}
		if m.Error != nil {
			return fmt.Errorf("pull de %s: %s", ref, m.Error.Message)
		}
		if line := pullLine(m); line != "" && fn != nil {
			fn(line)
		}
	}
	return nil
}

// Run cria e inicia o container; nome já existente → ErrConflict.
func (l *Local) Run(ctx context.Context, s RunSpec) (string, error) {
	cli, err := l.client()
	if err != nil {
		return "", err
	}
	cfg, host, err := toCreateConfig(s)
	if err != nil {
		return "", err
	}
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: cfg, HostConfig: host, Name: s.Name})
	if err != nil {
		return "", mapErr(err)
	}
	if _, err := cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		// Não deixa o container criado para trás ocupando o nome.
		_, _ = cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
		return "", mapErr(err)
	}
	return created.ID, nil
}

// RemoveForce remove o container mesmo rodando, sem apagar volumes. Container
// inexistente não é erro.
func (l *Local) RemoveForce(ctx context.Context, id string) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	if err = mapErr(err); errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Images lista as imagens com tag no repositório repo, da mais nova à mais antiga.
func (l *Local) Images(ctx context.Context, repo string) ([]Image, error) {
	cli, err := l.client()
	if err != nil {
		return nil, err
	}
	res, err := cli.ImageList(ctx, client.ImageListOptions{Filters: make(client.Filters).Add("reference", repo)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Image, 0, len(res.Items))
	for _, s := range res.Items {
		ref := firstRepoTag(s.RepoTags, repo)
		if ref == "" {
			continue
		}
		out = append(out, Image{ID: s.ID, Ref: ref, CreatedAt: time.Unix(s.Created, 0).UTC()})
	}
	slices.SortStableFunc(out, func(a, b Image) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// RemoveImage remove a imagem sem force; em uso → ErrConflict.
func (l *Local) RemoveImage(ctx context.Context, ref string) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{})
	return mapErr(err)
}

// ListByLabel lista os containers, rodando ou não, com o label key=value.
func (l *Local) ListByLabel(ctx context.Context, key, value string) ([]Container, error) {
	cli, err := l.client()
	if err != nil {
		return nil, err
	}
	f := make(client.Filters).Add("label", key+"="+value)
	res, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		out = append(out, toContainer(s))
	}
	return out, nil
}
