package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// Local é o Client que fala com o Docker local (DOCKER_HOST ou o socket padrão).
type Local struct {
	cli *client.Client
	err error // erro de configuração do cliente (ex.: DOCKER_HOST inválido)
}

var _ Client = (*Local)(nil)

// NewLocal cria o cliente sem conectar, para o KoreGO subir mesmo com o Docker parado.
func NewLocal() *Local {
	c, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	return &Local{cli: c, err: err}
}

// Close libera as conexões do cliente.
func (l *Local) Close() error {
	if l.cli == nil {
		return nil
	}
	return l.cli.Close()
}

func (l *Local) client() (*client.Client, error) {
	if l.err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, l.err)
	}
	return l.cli, nil
}

// mapErr traduz os erros do SDK para os erros do pacote.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case client.IsErrConnectionFailed(err):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	case cerrdefs.IsNotFound(err):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case cerrdefs.IsConflict(err):
		return fmt.Errorf("%w: %w", ErrConflict, err)
	default:
		return err
	}
}

// Ping verifica se o daemon responde.
func (l *Local) Ping(ctx context.Context) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.Ping(ctx, client.PingOptions{})
	return mapErr(err)
}

// List lista todos os containers, inclusive os parados.
func (l *Local) List(ctx context.Context) ([]Container, error) {
	cli, err := l.client()
	if err != nil {
		return nil, err
	}
	res, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		out = append(out, toContainer(s))
	}
	return out, nil
}

// Inspect devolve os detalhes de um container.
func (l *Local) Inspect(ctx context.Context, id string) (Details, error) {
	cli, err := l.client()
	if err != nil {
		return Details{}, err
	}
	res, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return Details{}, mapErr(err)
	}
	return toDetails(res.Container), nil
}

// Start inicia o container.
func (l *Local) Start(ctx context.Context, id string) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return mapErr(err)
}

// Stop para o container com o timeout padrão dele.
func (l *Local) Stop(ctx context.Context, id string) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.ContainerStop(ctx, id, client.ContainerStopOptions{})
	return mapErr(err)
}

// Restart reinicia o container com o timeout padrão dele.
func (l *Local) Restart(ctx context.Context, id string) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	return mapErr(err)
}

// Remove remove o container sem force e sem apagar volumes; rodando → ErrConflict.
func (l *Local) Remove(ctx context.Context, id string) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	_, err = cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
	return mapErr(err)
}

// Logs lê os logs do container e chama fn a cada linha. Bloqueia até o fim do
// stream (nil) ou até ctx terminar (ctx.Err()).
func (l *Local) Logs(ctx context.Context, id string, o LogOptions, fn func(LogLine)) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	insp, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return mapErr(err)
	}
	tty := insp.Container.Config != nil && insp.Container.Config.Tty

	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: o.Follow}
	if o.Tail > 0 {
		opts.Tail = strconv.Itoa(o.Tail)
	}
	if o.Since != "" {
		if opts.Since, err = sinceParam(o.Since); err != nil {
			return err
		}
	}
	rc, err := cli.ContainerLogs(ctx, id, opts)
	if err != nil {
		return mapErr(err)
	}
	defer rc.Close()

	stdout := newLineWriter("stdout", fn)
	stderr := newLineWriter("stderr", fn)
	if tty {
		_, err = io.Copy(stdout, rc)
	} else {
		_, err = stdcopy.StdCopy(stdout, stderr, rc)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	stdout.flush()
	stderr.flush()
	return mapErr(err)
}

// Stats acompanha o uso de CPU e memória do container. Bloqueia até o fim do
// stream (nil) ou até ctx terminar (ctx.Err()).
func (l *Local) Stats(ctx context.Context, id string, fn func(Stats)) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	res, err := cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return mapErr(err)
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	for {
		var s container.StatsResponse
		if err := dec.Decode(&s); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return mapErr(err)
		}
		fn(toStats(container.StatsResponse{CPUStats: s.PreCPUStats}, s))
	}
}

// Events repassa os eventos de container das ações em EventActions. Bloqueia
// até ctx terminar (ctx.Err()) ou o stream cair (ErrUnavailable).
func (l *Local) Events(ctx context.Context, fn func(Event)) error {
	cli, err := l.client()
	if err != nil {
		return err
	}
	f := make(client.Filters).Add("type", string(events.ContainerEventType)).Add("event", EventActions...)
	res := cli.Events(ctx, client.EventsListOptions{Filters: f})
	for {
		select {
		case m := <-res.Messages:
			fn(toEvent(m))
		case err := <-res.Err:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil || errors.Is(err, io.EOF) {
				return fmt.Errorf("%w: stream de eventos encerrado", ErrUnavailable)
			}
			return mapErr(err)
		}
	}
}

// --- conversão dos tipos do SDK ---

const (
	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelManaged = "korego.managed"
)

func toContainer(s container.Summary) Container {
	var name string
	if len(s.Names) > 0 {
		name = strings.TrimPrefix(s.Names[0], "/")
	}
	ports := make([]Port, 0, len(s.Ports))
	for _, p := range s.Ports {
		var ip string
		if p.IP.IsValid() {
			ip = p.IP.String()
		}
		ports = append(ports, Port{IP: ip, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Proto: p.Type})
	}
	health := ""
	if s.Health != nil {
		health = healthString(s.Health.Status)
	} else {
		health = healthFromStatus(s.Status)
	}
	c := Container{
		ID:        s.ID,
		Name:      name,
		Image:     s.Image,
		State:     string(s.State),
		Status:    s.Status,
		Health:    health,
		CreatedAt: time.Unix(s.Created, 0).UTC(),
		Ports:     ports,
	}
	applyLabels(&c, s.Labels)
	return c
}

func applyLabels(c *Container, labels map[string]string) {
	c.Project = labels[labelProject]
	c.Service = labels[labelService]
	c.Managed = labels[labelManaged] == "true"
}

func healthString(s container.HealthStatus) string {
	switch s {
	case container.Healthy, container.Unhealthy, container.Starting:
		return string(s)
	default:
		return ""
	}
}

// healthFromStatus lê a saúde do texto de status, para daemons cuja listagem
// não traz o campo Health.
func healthFromStatus(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	default:
		return ""
	}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return time.Time{}
	}
	return t
}

func toDetails(r container.InspectResponse) Details {
	d := Details{
		Container: Container{
			ID:        r.ID,
			Name:      strings.TrimPrefix(r.Name, "/"),
			Image:     r.Image,
			CreatedAt: parseTime(r.Created),
			Ports:     []Port{},
		},
		Command:      strings.TrimSpace(strings.Join(append([]string{r.Path}, r.Args...), " ")),
		RestartCount: r.RestartCount,
		EnvNames:     []string{},
		Networks:     []string{},
		Mounts:       make([]Mount, 0, len(r.Mounts)),
	}
	if r.State != nil {
		d.State = string(r.State.Status)
		d.StartedAt = parseTime(r.State.StartedAt)
		if r.State.Health != nil {
			d.Health = healthString(r.State.Health.Status)
		}
	}
	if r.Config != nil {
		if r.Config.Image != "" {
			d.Image = r.Config.Image
		}
		applyLabels(&d.Container, r.Config.Labels)
		for _, e := range r.Config.Env {
			name, _, _ := strings.Cut(e, "=")
			d.EnvNames = append(d.EnvNames, name)
		}
		slices.Sort(d.EnvNames)
	}
	if ns := r.NetworkSettings; ns != nil {
		for n := range ns.Networks {
			d.Networks = append(d.Networks, n)
		}
		slices.Sort(d.Networks)
		for p, binds := range ns.Ports {
			if len(binds) == 0 {
				d.Ports = append(d.Ports, Port{PrivatePort: p.Num(), Proto: string(p.Proto())})
				continue
			}
			for _, b := range binds {
				var ip string
				if b.HostIP.IsValid() {
					ip = b.HostIP.String()
				}
				pub, _ := strconv.ParseUint(b.HostPort, 10, 16)
				d.Ports = append(d.Ports, Port{IP: ip, PrivatePort: p.Num(), PublicPort: uint16(pub), Proto: string(p.Proto())})
			}
		}
		slices.SortFunc(d.Ports, func(a, b Port) int {
			if c := int(a.PrivatePort) - int(b.PrivatePort); c != 0 {
				return c
			}
			if c := strings.Compare(a.Proto, b.Proto); c != 0 {
				return c
			}
			return strings.Compare(a.IP, b.IP)
		})
	}
	for _, m := range r.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: string(m.Type), Source: m.Source, Destination: m.Destination, ReadWrite: m.RW})
	}
	return d
}

func toEvent(m events.Message) Event {
	action := string(m.Action)
	if a, _, ok := strings.Cut(action, ":"); ok {
		action = strings.TrimSpace(a)
	}
	return Event{Action: action, ID: m.Actor.ID, Name: m.Actor.Attributes["name"]}
}
