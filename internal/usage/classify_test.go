package usage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/procs"
)

func TestClassify_Precedence(t *testing.T) {
	idA := strings.Repeat("a", 64)
	idB := strings.Repeat("b", 64)
	idC := strings.Repeat("c", 64)
	idX := strings.Repeat("d", 64)
	idN := strings.Repeat("f", 64)
	idM := strings.Repeat("e", 64)
	l := Lookup{
		Containers: map[string]docker.Container{
			idA: {ID: idA, Name: "blog-web-1", Project: "blog"},
			idM: {ID: idM, Name: "shop-db-1", Project: "shop", Managed: true},
			idN: {ID: idN, Name: "shop-web-1", Project: "shop"},
			idB: {ID: idB, Name: "web-1"},
			idC: {ID: idC, Name: "app-1", Project: "loja", Managed: true},
		},
		PM2:    map[int]string{500: "bot-discord"},
		Parent: map[int]int{501: 500, 502: 501, 500: 1},
	}
	tru := true
	fls := false
	cases := []struct {
		nome string
		p    procs.Proc
		want Ref
	}{
		{"v2 com projeto", procs.Proc{PID: 10, PPID: 1, Cmdline: []string{"x"}, Cgroup: "/system.slice/docker-" + idA + ".scope"},
			Ref{Key: "project:blog", Label: "blog", Kind: KindContainer, Managed: &fls}},
		{"projeto gerenciado se algum for", procs.Proc{PID: 10, Cgroup: "/system.slice/docker-" + idN + ".scope"},
			Ref{Key: "project:shop", Label: "shop", Kind: KindContainer, Managed: &tru}},
		{"v1 sem projeto", procs.Proc{PID: 11, Cgroup: "/docker/" + idB},
			Ref{Key: "container:web-1", Label: "Avulso: web-1", Kind: KindContainer, Managed: &fls}},
		{"projeto gerenciado", procs.Proc{PID: 12, Cgroup: "/docker/" + idC},
			Ref{Key: "project:loja", Label: "loja", Kind: KindContainer, Managed: &tru}},
		{"id desconhecido", procs.Proc{PID: 13, Cgroup: "/docker/" + idX},
			Ref{Key: "other", Label: "Outros", Kind: KindOther}},
		{"filho de app pm2", procs.Proc{PID: 502, PPID: 501, Cgroup: "/system.slice/pm2-root.service"},
			Ref{Key: "pm2:bot-discord", Label: "PM2: bot-discord", Kind: KindPM2}},
		{"o proprio app pm2", procs.Proc{PID: 500, PPID: 1, Cgroup: "/system.slice/pm2-root.service"},
			Ref{Key: "pm2:bot-discord", Label: "PM2: bot-discord", Kind: KindPM2}},
		{"daemon pm2", procs.Proc{PID: 400, PPID: 1, Cgroup: "/system.slice/pm2-root.service"},
			Ref{Key: "service:pm2-daemon", Label: "PM2 (daemon)", Kind: KindService}},
		{"nginx", procs.Proc{PID: 20, PPID: 1, Cgroup: "/system.slice/nginx.service"},
			Ref{Key: "service:nginx", Label: "nginx", Kind: KindService}},
		{"sessao", procs.Proc{PID: 30, PPID: 1, Cgroup: "/user.slice/user-1000.slice/session-3.scope"},
			Ref{Key: "session", Label: "Sessões de usuário", Kind: KindSession}},
		{"kthread no cgroup raiz", procs.Proc{PID: 40, PPID: 2, Cgroup: "/"},
			Ref{Key: "kernel", Label: "Kernel", Kind: KindKernel}},
		{"kthreadd", procs.Proc{PID: 2, PPID: 0, Cgroup: "/"},
			Ref{Key: "kernel", Label: "Kernel", Kind: KindKernel}},
		{"ppid 2 com cmdline nao e kernel", procs.Proc{PID: 41, PPID: 2, Cmdline: []string{"x"}, Cgroup: "/"},
			Ref{Key: "other", Label: "Outros", Kind: KindOther}},
		{"cgroup vazio", procs.Proc{PID: 50, PPID: 1},
			Ref{Key: "other", Label: "Outros", Kind: KindOther}},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			assert.Equal(t, c.want, Classify(c.p, l))
		})
	}
}

func TestClassify_PM2CicloNaoTrava(t *testing.T) {
	l := Lookup{PM2: map[int]string{999: "x"}, Parent: map[int]int{1: 2, 2: 1}}
	got := Classify(procs.Proc{PID: 1, PPID: 2, Cmdline: []string{"x"}}, l)
	assert.Equal(t, KindOther, got.Kind)
}
