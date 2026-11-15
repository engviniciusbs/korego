package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func wf(t *testing.T, p, c string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseName(t *testing.T) {
	ts := time.Date(2026, 11, 15, 13, 30, 41, 0, time.UTC)
	if got := ReleaseName(ts, "9faa1c9abc"); got != "20261115133041-9faa1c9" {
		t.Fatal(got)
	}
	if got := ReleaseName(ts, ""); got != "20261115133041-0000000" {
		t.Fatal(got)
	}
}

func TestDetectBuildCommand(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"package.json", "pnpm-lock.yaml"}, "corepack enable && pnpm install --frozen-lockfile && pnpm run build"},
		{[]string{"package.json", "yarn.lock"}, "corepack enable && yarn install --frozen-lockfile && yarn build"},
		{[]string{"package.json", "package-lock.json"}, "npm ci && npm run build"},
		{[]string{"index.html"}, ""},
	}
	for _, c := range cases {
		d := t.TempDir()
		for _, f := range c.files {
			wf(t, filepath.Join(d, f), "x")
		}
		if got := DetectBuildCommand(d); got != c.want {
			t.Errorf("%v: %q", c.files, got)
		}
	}
}

func TestPublishRelease(t *testing.T) {
	src, pub := t.TempDir(), t.TempDir()
	wf(t, filepath.Join(src, "index.html"), "<h1>")
	wf(t, filepath.Join(src, "assets/a.js"), "x")
	got, err := PublishRelease(src, pub, "20261115133041-9faa1c9", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(pub, "releases", "20261115133041-9faa1c9") {
		t.Fatal(got)
	}
	for p, m := range map[string]os.FileMode{"index.html": 0o644, "assets/a.js": 0o644, "assets": 0o755, ".": 0o755} {
		fi, err := os.Stat(filepath.Join(got, p))
		if err != nil || fi.Mode().Perm() != m {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
}

func TestPublishRelease_SPAExigeIndex(t *testing.T) {
	src, pub := t.TempDir(), t.TempDir()
	wf(t, filepath.Join(src, "about.html"), "x")
	name := "20261115133041-9faa1c9"
	if _, err := PublishRelease(src, pub, name, true); err == nil {
		t.Fatal("esperava erro sem index.html")
	}
	if _, err := os.Stat(filepath.Join(pub, "releases", name)); !os.IsNotExist(err) {
		t.Fatal("pasta parcial não foi apagada")
	}
	if _, err := PublishRelease(src, pub, name, false); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRelease_RecusaLinkParaFora(t *testing.T) {
	src, pub := t.TempDir(), t.TempDir()
	wf(t, filepath.Join(src, "index.html"), "x")
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "p.txt")); err != nil {
		t.Fatal(err)
	}
	name := "20261115133041-9faa1c9"
	if _, err := PublishRelease(src, pub, name, true); err == nil {
		t.Fatal("esperava recusa")
	}
	if _, err := os.Stat(filepath.Join(pub, "releases", name)); !os.IsNotExist(err) {
		t.Fatal("pasta parcial sobrou")
	}
}

func TestSwapCurrent(t *testing.T) {
	pub := t.TempDir()
	a, b := filepath.Join(pub, "releases", "20261115000000-aaaaaaa"), filepath.Join(pub, "releases", "20261115000001-bbbbbbb")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	for _, r := range []string{a, b} {
		if err := SwapCurrent(pub, r); err != nil {
			t.Fatal(err)
		}
		got, err := CurrentRelease(pub)
		if err != nil || got != r {
			t.Fatalf("%q %v", got, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(pub, "current.tmp")); !os.IsNotExist(err) {
		t.Fatal("sobrou current.tmp")
	}
}

func TestPruneReleases(t *testing.T) {
	pub := t.TempDir()
	rel := filepath.Join(pub, "releases")
	var names []string
	for i := 1; i <= 7; i++ {
		n := ReleaseName(time.Date(2026, 11, i, 0, 0, 0, 0, time.UTC), "abcdef0")
		names = append(names, n)
		os.MkdirAll(filepath.Join(rel, n), 0o755)
	}
	os.MkdirAll(filepath.Join(rel, "old-site"), 0o755)
	os.MkdirAll(filepath.Join(rel, "20261114.bak"), 0o755)
	if err := PruneReleases(pub, 5, filepath.Join(rel, names[0])); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(rel)
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	want := []string{names[0], names[2], names[3], names[4], names[5], names[6], "20261114.bak", "old-site"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPruneReleases_ProtegeCurrent(t *testing.T) {
	pub := t.TempDir()
	rel := filepath.Join(pub, "releases")
	var names []string
	for i := 1; i <= 4; i++ {
		n := ReleaseName(time.Date(2026, 11, i, 0, 0, 0, 0, time.UTC), "abcdef0")
		names = append(names, n)
		os.MkdirAll(filepath.Join(rel, n), 0o755)
	}
	SwapCurrent(pub, filepath.Join(rel, names[0]))
	if err := PruneReleases(pub, 1); err != nil {
		t.Fatal(err)
	}
	for i, n := range names {
		_, err := os.Stat(filepath.Join(rel, n))
		if (i == 0 || i == 3) != (err == nil) {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestValidateAdoptRoot(t *testing.T) {
	www, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := wwwRoot
	wwwRoot = www
	defer func() { wwwRoot = old }()

	root := filepath.Join(www, "hoprs")
	rel := filepath.Join(root, "releases", "x")
	os.MkdirAll(rel, 0o755)
	os.Symlink(rel, filepath.Join(root, "current"))
	pr, rd, err := ValidateAdoptRoot(filepath.Join(root, "current"))
	if err != nil || pr != root || rd != rel {
		t.Fatalf("%q %q %v", pr, rd, err)
	}

	// current como pasta real
	r2 := filepath.Join(www, "real")
	os.MkdirAll(filepath.Join(r2, "current"), 0o755)
	if _, _, err := ValidateAdoptRoot(filepath.Join(r2, "current")); err == nil {
		t.Error("pasta real aceita")
	}
	// link para fora de releases/
	r3 := filepath.Join(www, "out")
	os.MkdirAll(filepath.Join(r3, "other"), 0o755)
	os.Symlink(filepath.Join(r3, "other"), filepath.Join(r3, "current"))
	if _, _, err := ValidateAdoptRoot(filepath.Join(r3, "current")); err == nil {
		t.Error("link fora de releases aceito")
	}
	// raiz fora de wwwRoot
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(outside, "releases", "x"), 0o755)
	os.Symlink(filepath.Join(outside, "releases", "x"), filepath.Join(outside, "current"))
	if _, _, err := ValidateAdoptRoot(filepath.Join(outside, "current")); err == nil {
		t.Error("raiz fora de wwwRoot aceita")
	}
}

func TestInsideRoot(t *testing.T) {
	base := t.TempDir()
	stack := filepath.Join(base, "stack")
	os.MkdirAll(stack, 0o755)
	wf(t, filepath.Join(stack, "ok.sh"), "x")
	wf(t, filepath.Join(base, "x.sh"), "x")
	if _, err := InsideRoot(stack, filepath.Join(stack, "ok.sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := InsideRoot(stack, filepath.Join(stack, "../x.sh")); err == nil {
		t.Error("../x.sh aceito")
	}
	os.Symlink(filepath.Join(base, "x.sh"), filepath.Join(stack, "l.sh"))
	if _, err := InsideRoot(stack, filepath.Join(stack, "l.sh")); err == nil {
		t.Error("link para fora aceito")
	}
}
