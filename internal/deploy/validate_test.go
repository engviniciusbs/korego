package deploy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func check(t *testing.T, name string, f func(string) bool, ok, bad []string) {
	t.Helper()
	for _, s := range ok {
		assert.True(t, f(s), "%s deveria aceitar %q", name, s)
	}
	for _, s := range bad {
		assert.False(t, f(s), "%s deveria recusar %q", name, s)
	}
}

func TestValidators(t *testing.T) {
	check(t, "slug", ValidSlug, []string{"blog", "a", "meu-app-1", strings.Repeat("a", 63)},
		[]string{"", "-a", "Blog", "a_b", "a b", "../x", strings.Repeat("a", 64)})
	check(t, "repo", ValidRepo, []string{"owner/app", "o.w-n_1/r.e-p_o"},
		[]string{"", "owner", "a/b/c", "../x", "./x", "a/..", "a/.", "a b/c", "-"})
	check(t, "gitURL", ValidGitURL, []string{"https://github.com/o/r.git", "https://git.exemplo.com/x"},
		[]string{"", "http://github.com/o/r", "ssh://git@github.com/o/r", "git@github.com:o/r",
			"https://user:pass@github.com/o/r", "https://user@github.com/o/r", "https://github.com/o /r",
			"file:///etc", "https://", "-https://x.com", "ext::sh -c x"})
	check(t, "branch", ValidBranch, []string{"main", "feature/x-1", "v1.2.3", "rel_1"},
		[]string{"", "-branch", "/abs", "a/", "main.lock", "a..b", "a b", "a;b", "$x", strings.Repeat("a", 256)})
	check(t, "relpath", ValidRelPath, []string{"Dockerfile", "docker/Dockerfile.prod", "./compose.yml"},
		[]string{"", "../x", "a/../../x", "/abs", "-f", "a\\b", "C:/x", "a/..", "a\x00b"})
	check(t, "service", ValidServiceName, []string{"web", "web.1", "A_b-c"},
		[]string{"", "-a", "a b", ".a", strings.Repeat("a", 64)})
	check(t, "env", ValidEnvName, []string{"A", "_X", "DATABASE_URL", "a1"},
		[]string{"", "1A", "A-B", "A B", "A=B", strings.Repeat("A", 129)})
	check(t, "image", ValidImageRef, []string{
		"nginx", "nginx:1-alpine", "library/nginx", "ghcr.io/owner/app:v1.2.3",
		"registry.exemplo.com:5000/team/app@sha256:" + strings.Repeat("a", 64),
		"localhost:5000/app", "nginx:latest@sha256:" + strings.Repeat("0", 64), "a/b__c/d-e"},
		[]string{"", "Imagem:latest", "nginx:", "-nginx", "nginx latest", "a/../b", "nginx@sha256:abc",
			"nginx:" + strings.Repeat("a", 129), "ghcr.io//a", "a/", "/a", "nginx;rm", "nginx:-x"})
}
