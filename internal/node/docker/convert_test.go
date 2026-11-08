package docker

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToContainer_LabelsAndName(t *testing.T) {
	s := container.Summary{
		ID:      "abc123",
		Names:   []string{"/web-1"},
		Image:   "nginx:1.27",
		Created: 1700000000,
		State:   container.StateRunning,
		Status:  "Up 3 hours (healthy)",
		Labels: map[string]string{
			"com.docker.compose.project": "loja",
			"com.docker.compose.service": "web",
			"korego.managed":             "true",
		},
		Ports: []container.PortSummary{{IP: netip.MustParseAddr("0.0.0.0"), PrivatePort: 80, PublicPort: 8080, Type: "tcp"}},
	}
	c := toContainer(s)
	assert.Equal(t, "web-1", c.Name)
	assert.Equal(t, "abc123", c.ID)
	assert.Equal(t, "nginx:1.27", c.Image)
	assert.Equal(t, "running", c.State)
	assert.Equal(t, "Up 3 hours (healthy)", c.Status)
	assert.Equal(t, "healthy", c.Health)
	assert.Equal(t, "loja", c.Project)
	assert.Equal(t, "web", c.Service)
	assert.True(t, c.Managed)
	assert.Equal(t, int64(1700000000), c.CreatedAt.Unix())
	assert.Equal(t, []Port{{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Proto: "tcp"}}, c.Ports)

	sem := toContainer(container.Summary{Names: []string{"/avulso"}, Labels: map[string]string{"korego.managed": "false"}})
	assert.Equal(t, "", sem.Project)
	assert.False(t, sem.Managed)
	assert.Equal(t, "", sem.Health)
	assert.NotNil(t, sem.Ports)
}

func TestToContainer_Health(t *testing.T) {
	assert.Equal(t, "unhealthy", toContainer(container.Summary{Health: &container.HealthSummary{Status: container.Unhealthy}}).Health)
	assert.Equal(t, "", toContainer(container.Summary{Health: &container.HealthSummary{Status: container.NoHealthcheck}}).Health)
	assert.Equal(t, "starting", toContainer(container.Summary{Status: "Up 2 seconds (health: starting)"}).Health)
	assert.Equal(t, "unhealthy", toContainer(container.Summary{Status: "Up 1 minute (unhealthy)"}).Health)
}

func TestToContainer_JSONCamelCase(t *testing.T) {
	b, err := json.Marshal(toContainer(container.Summary{Names: []string{"/x"}}))
	require.NoError(t, err)
	for _, k := range []string{"id", "name", "image", "state", "status", "health", "createdAt", "project", "service", "managed", "ports"} {
		assert.Contains(t, string(b), `"`+k+`":`)
	}
}

func TestToDetails_EnvNamesOnly(t *testing.T) {
	r := container.InspectResponse{
		ID:           "abc123",
		Name:         "/db",
		Created:      "2024-01-02T03:04:05.123456789Z",
		Path:         "docker-entrypoint.sh",
		Args:         []string{"postgres", "-c", "x=1"},
		RestartCount: 2,
		State: &container.State{
			Status:    container.StateRunning,
			StartedAt: "2024-01-02T03:05:00Z",
			Health:    &container.Health{Status: container.Healthy},
		},
		Config: &container.Config{
			Image:  "postgres:16",
			Env:    []string{"B=2", "A=segredo", "SEM_VALOR"},
			Labels: map[string]string{"com.docker.compose.project": "loja"},
		},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{"loja_default": {}, "bridge": {}},
			Ports: network.PortMap{
				network.MustParsePort("5432/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "15432"}},
			},
		},
		Mounts: []container.MountPoint{{Type: "volume", Source: "/var/lib/docker/volumes/x", Destination: "/data", RW: true}},
	}
	d := toDetails(r)
	assert.Equal(t, []string{"A", "B", "SEM_VALOR"}, d.EnvNames)
	assert.Equal(t, "db", d.Name)
	assert.Equal(t, "postgres:16", d.Image)
	assert.Equal(t, "running", d.State)
	assert.Equal(t, "healthy", d.Health)
	assert.Equal(t, "loja", d.Project)
	assert.Equal(t, "docker-entrypoint.sh postgres -c x=1", d.Command)
	assert.Equal(t, 2, d.RestartCount)
	assert.Equal(t, []string{"bridge", "loja_default"}, d.Networks)
	assert.Equal(t, []Mount{{Type: "volume", Source: "/var/lib/docker/volumes/x", Destination: "/data", ReadWrite: true}}, d.Mounts)
	assert.Equal(t, []Port{{IP: "127.0.0.1", PrivatePort: 5432, PublicPort: 15432, Proto: "tcp"}}, d.Ports)
	assert.Equal(t, int64(1704164645123456789), d.CreatedAt.UnixNano())
	assert.Equal(t, int64(1704164700), d.StartedAt.Unix())

	b, err := json.Marshal(d)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "segredo")
	for _, k := range []string{"id", "name", "command", "startedAt", "restartCount", "envNames", "networks", "mounts", "readWrite"} {
		assert.Contains(t, string(b), `"`+k+`":`)
	}
}

func TestToDetails_CamposNulos(t *testing.T) {
	d := toDetails(container.InspectResponse{ID: "x", Name: "/y"})
	assert.Equal(t, "y", d.Name)
	assert.Empty(t, d.EnvNames)
}

func TestLogLine_InvalidUTF8AndLong(t *testing.T) {
	assert.Equal(t, "a�b", sanitizeLine([]byte{'a', 0xff, 0xfe, 'b'}))
	assert.Equal(t, "olá", sanitizeLine([]byte("olá")))

	longa := strings.Repeat("x", 20*1024)
	assert.Equal(t, strings.Repeat("x", 16*1024)+"…", sanitizeLine([]byte(longa)))

	// O corte respeita o limite de rune: "é" tem 2 bytes e 16 KiB cai no meio de um.
	got := sanitizeLine([]byte("x" + strings.Repeat("é", 10*1024)))
	assert.True(t, utf8.ValidString(got))
	assert.True(t, strings.HasSuffix(got, "…"))
	assert.Equal(t, 16*1024-1, len(strings.TrimSuffix(got, "…")))
}

func TestParseLogLine(t *testing.T) {
	l := parseLogLine([]byte("2024-01-02T03:04:05.000000123Z olá mundo\r"), "stderr", "")
	assert.Equal(t, "1704164645000000123", l.TS)
	assert.Equal(t, "stderr", l.Stream)
	assert.Equal(t, "olá mundo", l.Text)

	// Sem timestamp válido, mantém o texto e usa o último TS conhecido.
	l = parseLogLine([]byte("sem-ts"), "stdout", "0000000000000000001")
	assert.Equal(t, "0000000000000000001", l.TS)
	assert.Equal(t, "sem-ts", l.Text)
}

func TestSinceParam(t *testing.T) {
	s, err := sinceParam("1704164645000000123")
	require.NoError(t, err)
	assert.Equal(t, "1704164645.000000124", s) // exclui a linha já vista
	_, err = sinceParam("abc")
	assert.Error(t, err)
}

func TestLineWriter_SplitsAndFlushes(t *testing.T) {
	var got []LogLine
	w := newLineWriter("stdout", func(l LogLine) { got = append(got, l) })
	_, _ = w.Write([]byte("2024-01-02T03:04:05Z um\n2024-01-02T03:04:06Z do"))
	_, _ = w.Write([]byte("is\n2024-01-02T03:04:07Z tres"))
	w.flush()
	require.Len(t, got, 3)
	assert.Equal(t, []string{"um", "dois", "tres"}, []string{got[0].Text, got[1].Text, got[2].Text})
	assert.Less(t, got[0].TS, got[1].TS)
}

func TestLineWriter_LinhaEnormeLimitada(t *testing.T) {
	var got []LogLine
	w := newLineWriter("stdout", func(l LogLine) { got = append(got, l) })
	_, _ = w.Write([]byte("2024-01-02T03:04:05Z "))
	for range 100 {
		_, _ = w.Write([]byte(strings.Repeat("y", 1024)))
	}
	_, _ = w.Write([]byte("\n2024-01-02T03:04:06Z fim\n"))
	require.Len(t, got, 2)
	assert.Equal(t, strings.Repeat("y", 16*1024)+"…", got[0].Text)
	assert.Equal(t, "fim", got[1].Text)
	assert.LessOrEqual(t, cap(w.buf), 2*maxBuffered)
}

func TestToEvent(t *testing.T) {
	e := toEvent(events.Message{Action: "health_status: healthy", Actor: events.Actor{ID: "abc", Attributes: map[string]string{"name": "web"}}})
	assert.Equal(t, Event{Action: "health_status", ID: "abc", Name: "web"}, e)
	assert.Equal(t, "start", toEvent(events.Message{Action: events.ActionStart}).Action)
}

func TestToCreateConfig_PortsLabelsRestart(t *testing.T) {
	cfg, host, err := toCreateConfig(RunSpec{
		Name:    "blog",
		Image:   "korego/blog:abc",
		Labels:  map[string]string{"korego.managed": "true", "korego.app": "blog"},
		Ports:   []PortMapping{{HostIP: "127.0.0.1", HostPort: 20001, ContainerPort: 8080}},
		Volumes: []VolumeMount{{Volume: "blog-data", Target: "/data"}},
		Cmd:     []string{"serve"},
	})
	require.NoError(t, err)
	assert.Equal(t, "korego/blog:abc", cfg.Image)
	assert.Equal(t, []string{"serve"}, cfg.Cmd)
	assert.Equal(t, map[string]string{"korego.managed": "true", "korego.app": "blog"}, cfg.Labels)

	p := network.MustParsePort("8080/tcp")
	require.Contains(t, host.PortBindings, p)
	require.Len(t, host.PortBindings[p], 1)
	assert.Equal(t, "127.0.0.1", host.PortBindings[p][0].HostIP.String())
	assert.Equal(t, "20001", host.PortBindings[p][0].HostPort)
	assert.Contains(t, cfg.ExposedPorts, p)

	assert.Equal(t, container.RestartPolicyUnlessStopped, host.RestartPolicy.Name)
	require.Len(t, host.Mounts, 1)
	assert.Equal(t, mount.TypeVolume, host.Mounts[0].Type)
	assert.Equal(t, "blog-data", host.Mounts[0].Source)
	assert.Equal(t, "/data", host.Mounts[0].Target)

	// Política explícita é respeitada.
	_, host, err = toCreateConfig(RunSpec{Image: "x", RestartPolicy: "always"})
	require.NoError(t, err)
	assert.Equal(t, container.RestartPolicyAlways, host.RestartPolicy.Name)

	// Rede host: sem port bindings.
	cfg, host, err = toCreateConfig(RunSpec{
		Image:       "caddy",
		NetworkMode: "host",
		Ports:       []PortMapping{{HostPort: 80, ContainerPort: 80}},
	})
	require.NoError(t, err)
	assert.Equal(t, container.NetworkMode("host"), host.NetworkMode)
	assert.Empty(t, host.PortBindings)
	assert.Empty(t, cfg.ExposedPorts)
}

func TestToCreateConfig_EnvNotInError(t *testing.T) {
	const secret = "s3gredo-super-secreto"
	cases := []RunSpec{
		{Image: "", Env: []string{"TOKEN=" + secret}},
		{Image: "x", Env: []string{"TOKEN=" + secret, "SEM_IGUAL" + secret}},
		{Image: "x", Env: []string{"=" + secret}},
		{Image: "x", Env: []string{"A=" + secret}, Ports: []PortMapping{{ContainerPort: 0}}},
		{Image: "x", Env: []string{"A=" + secret}, Ports: []PortMapping{{HostIP: "nao-ip", ContainerPort: 80}}},
		{Image: "x", Env: []string{"A=" + secret}, Volumes: []VolumeMount{{Volume: "", Target: "/d"}}},
	}
	for i, s := range cases {
		_, _, err := toCreateConfig(s)
		require.Error(t, err, "caso %d", i)
		assert.NotContains(t, err.Error(), secret, "caso %d", i)
	}
}

func TestPullLine(t *testing.T) {
	assert.Equal(t, "Pulling from library/busybox", pullLine(jsonstream.Message{Status: "Pulling from library/busybox"}))
	assert.Equal(t, "abc123: Pull complete", pullLine(jsonstream.Message{ID: "abc123", Status: "Pull complete"}))
	assert.Equal(t, "abc123: Downloading 1.0 KiB/2.0 KiB",
		pullLine(jsonstream.Message{ID: "abc123", Status: "Downloading", Progress: &jsonstream.Progress{Current: 1024, Total: 2048}}))
	assert.Equal(t, "abc123: Extracting 512 B",
		pullLine(jsonstream.Message{ID: "abc123", Status: "Extracting", Progress: &jsonstream.Progress{Current: 512}}))
	assert.Equal(t, "", pullLine(jsonstream.Message{}))
}

func TestRepoMatch(t *testing.T) {
	assert.Equal(t, "korego/blog:v2", firstRepoTag([]string{"outra:1", "korego/blog:v2", "korego/blog:v1"}, "korego/blog"))
	assert.Equal(t, "", firstRepoTag([]string{"korego/blog-x:1", "<none>:<none>"}, "korego/blog"))
}
