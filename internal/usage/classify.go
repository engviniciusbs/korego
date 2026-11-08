// Package usage atribui cada processo a um dono (projeto, app PM2, serviço...)
// e soma o consumo por dono.
package usage

import (
	"regexp"
	"strings"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/procs"
)

// Kind é o tipo de dono.
type Kind string

const (
	KindContainer Kind = "container"
	KindPM2       Kind = "pm2"
	KindService   Kind = "service"
	KindSession   Kind = "session"
	KindKernel    Kind = "kernel"
	KindOther     Kind = "other"
)

// Ref identifica o dono de um processo.
type Ref struct {
	Key, Label string
	Kind       Kind
	Managed    *bool // só para Kind == container
}

// Lookup reúne o que é preciso para atribuir donos.
type Lookup struct {
	Containers map[string]docker.Container // id completo (64 hex) → container
	PM2        map[int]string              // pid do app → nome do app
	Parent     map[int]int                 // pid → ppid desta leitura
}

var (
	reScope = regexp.MustCompile(`docker-([0-9a-f]{64})\.scope`)
	reV1    = regexp.MustCompile(`/docker/([0-9a-f]{64})`)
)

const maxDepth = 32

var other = Ref{Key: "other", Label: "Outros", Kind: KindOther}

// Classify devolve o dono de um processo, pela ordem da spec §2.
func Classify(p procs.Proc, l Lookup) Ref {
	// 1. container
	if id := containerID(p.Cgroup); id != "" {
		c, ok := l.Containers[id]
		if !ok {
			return other
		}
		if c.Project != "" {
			managed := false
			for _, o := range l.Containers {
				if o.Project == c.Project && o.Managed {
					managed = true
					break
				}
			}
			return Ref{Key: "project:" + c.Project, Label: c.Project, Kind: KindContainer, Managed: &managed}
		}
		managed := c.Managed
		return Ref{Key: "container:" + c.Name, Label: "Avulso: " + c.Name, Kind: KindContainer, Managed: &managed}
	}
	// 2. pm2: o PID ou um ancestral é um app
	if name, ok := pm2Owner(p, l); ok {
		return Ref{Key: "pm2:" + name, Label: "PM2: " + name, Kind: KindPM2}
	}
	// 3. serviço
	last := p.Cgroup
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	if unit, ok := strings.CutSuffix(last, ".service"); ok && unit != "" {
		if strings.HasPrefix(unit, "pm2-") {
			return Ref{Key: "service:pm2-daemon", Label: "PM2 (daemon)", Kind: KindService}
		}
		return Ref{Key: "service:" + unit, Label: unit, Kind: KindService}
	}
	// 4. sessão de usuário
	if strings.Contains(p.Cgroup, "/user.slice/") {
		return Ref{Key: "session", Label: "Sessões de usuário", Kind: KindSession}
	}
	// 5. kernel
	if p.PID == 2 || (p.PPID == 2 && len(p.Cmdline) == 0) {
		return Ref{Key: "kernel", Label: "Kernel", Kind: KindKernel}
	}
	return other
}

func containerID(cg string) string {
	if m := reScope.FindStringSubmatch(cg); m != nil {
		return m[1]
	}
	if m := reV1.FindStringSubmatch(cg); m != nil {
		return m[1]
	}
	return ""
}

func pm2Owner(p procs.Proc, l Lookup) (string, bool) {
	if len(l.PM2) == 0 {
		return "", false
	}
	pid := p.PID
	for depth := 0; depth < maxDepth && pid > 0; depth++ {
		if name, ok := l.PM2[pid]; ok {
			return name, true
		}
		next, ok := l.Parent[pid]
		if !ok && depth == 0 {
			next = p.PPID
		}
		if next == pid {
			break
		}
		pid = next
	}
	return "", false
}
