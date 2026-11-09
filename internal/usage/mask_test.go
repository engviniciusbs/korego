package usage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskCmd(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"app", "--password=s3cr3t"}, "app --password=••••"},
		{[]string{"app", "--db-password", "s3cr3t"}, "app --db-password ••••"},
		{[]string{"env", "API_TOKEN=abc"}, "env API_TOKEN=••••"},
		{[]string{"x", "--secret-key=v"}, "x --secret-key=••••"},
		{[]string{"x", "--apikey=v"}, "x --apikey=••••"},
		{[]string{"x", "--Passwd", "v", "ok"}, "x --Passwd •••• ok"},
		{[]string{"x", "--monkey=v"}, "x --monkey=v"},
		{[]string{"x", "--verbose", "file"}, "x --verbose file"},
		{nil, ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, MaskCmd(c.argv))
	}
	long := MaskCmd([]string{strings.Repeat("a", 400)})
	assert.LessOrEqual(t, len([]rune(long)), 160)
	assert.True(t, strings.HasSuffix(long, "…"))
}

func TestMaskCmd_ShC(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"sh", "-c", "mysql --host=db --password=hunter2"}, "sh -c mysql --host=db --password=••••"},
		{[]string{"sh", "-c", "FOO=1 API_TOKEN=abc run"}, "sh -c FOO=1 API_TOKEN=•••• run"},
		{[]string{"sh", "-c", "mysql --password hunter2"}, "sh -c mysql --password ••••"},
		{[]string{"sh", "-c", "mysql --password=hunter2 -h db"}, "sh -c mysql --password=•••• -h db"},
		{[]string{"sh", "-c", "x --password 'a b' y"}, "sh -c x --password '••••' y"},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		assert.NotContains(t, got, "hunter2")
	}
}

func TestMaskCmd_NomesExtrasEUserinfo(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"x", "--jwt.key=abc"}, "x --jwt.key=••••"},
		{[]string{"x", "--db:pass=abc"}, "x --db:pass=••••"},
		{[]string{"x", "--pwd=a", "--passphrase=b", "--auth=c", "--authorization=d", "--bearer=e"}, "x --pwd=•••• --passphrase=•••• --auth=•••• --authorization=•••• --bearer=••••"},
		{[]string{"x", "--credentials=a", "--credential=a", "--jwt=a", "--cookie=a", "--dsn=a"}, "x --credentials=•••• --credential=•••• --jwt=•••• --cookie=•••• --dsn=••••"},
		{[]string{"x", "--monkey=v"}, "x --monkey=v"},
		{[]string{"x", "postgres://u:p@db/x"}, "x postgres://u:••••@db/x"},
		{[]string{"x", "DATABASE_URL=postgres://u:p@db/x"}, "x DATABASE_URL=postgres://u:••••@db/x"},
		{[]string{"sh", "-c", "curl https://u:p@h/a"}, "sh -c curl https://u:••••@h/a"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, MaskCmd(c.argv))
	}
}

func TestMaskCmd_SoOValor(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"bash", "-c", `"mysql --password=hunter2 -h db" && echo ok`}, `bash -c "mysql --password=•••• -h db" && echo ok`},
		{[]string{"x", `"--password=hunter2"`, "-h", "db"}, `x "--password=••••" -h db`},
		{[]string{"x", `--password=a;--token=b`}, `x --password=••••`},
		{[]string{"x", `--password='a b' -h db`}, `x --password='••••' -h db`},
		{[]string{"x", `--cmd="run --token=zz" ok`}, `x --cmd="run --token=••••" ok`},
		{[]string{"sh", "-c", `a --password=p1 && b --token=p2 | c`}, `sh -c a --password=•••• && b --token=•••• | c`},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		for _, s := range []string{"hunter2", "p1", "p2", "zz"} {
			assert.NotContains(t, got, s)
		}
	}
}

func TestMaskCmd_SeparadoresNaSenhaDaURL(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"x", "--db=postgres://u:X1&Y2;Z3|W4@db/x"}, "x --db=postgres://u:••••@db/x"},
		{[]string{"x", "postgres://u:X1&Y2;Z3|W4@db/x"}, "x postgres://u:••••@db/x"},
		{[]string{"x", "DATABASE_URL=mysql://u:X1;Y2&Z3|W4@db/x"}, "x DATABASE_URL=mysql://u:••••@db/x"},
		{[]string{"sh", "-c", "psql postgres://u:X1&Y2;Z3|W4@db/x && echo ok"}, "sh -c psql postgres://u:••••@db/x && echo ok"},
		{[]string{"sh", "-c", "curl https://u:X1|Y2;Z3&W4@h/a | jq ."}, "sh -c curl https://u:••••@h/a | jq ."},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		for _, s := range []string{"X1", "Y2", "Z3", "W4"} {
			assert.NotContains(t, got, s)
		}
	}
}

func TestMaskCmd_ValorInteiroDoElemento(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"x", "--password=X1 Y2"}, "x --password=••••"},
		{[]string{"x", "API_TOKEN=X1 Y2 Z3", "run"}, "x API_TOKEN=•••• run"},
		{[]string{"x", "--password=X1&Y2;Z3|W4"}, "x --password=••••"},
		{[]string{"x", "--secret=X1 && Y2", "ok"}, "x --secret=•••• ok"},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		for _, s := range []string{"X1", "Y2", "Z3", "W4"} {
			assert.NotContains(t, got, s)
		}
	}
}

// Elemento nome=valor sensível não passa por shell: nada depois do = aparece.
func TestMaskCmd_ElementoSensivelSemCorte(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"x", "--password=Xk;tail=SECRETTAIL"}, "x --password=••••"},
		{[]string{"x", "--password=abc&def=ghi"}, "x --password=••••"},
		{[]string{"x", "API_TOKEN=abc|--x=ghi", "run"}, "x API_TOKEN=•••• run"},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		for _, s := range []string{"SECRETTAIL", "tail", "def", "ghi", "Xk", "abc"} {
			assert.NotContains(t, got, s)
		}
	}
}

// A senha da URL vai até o último @ antes de / ou espaço.
func TestMaskCmd_ArrobaNaSenhaDaURL(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"x", "postgres://u:p@Q9@db/x"}, "x postgres://u:••••@db/x"},
		{[]string{"x", "--db=postgres://u:p@Q9@db/x"}, "x --db=postgres://u:••••@db/x"},
		{[]string{"sh", "-c", "psql postgres://u:p@Q9@db/x && echo ok"}, "sh -c psql postgres://u:••••@db/x && echo ok"},
		// duas URLs sem espaço entre elas: cada senha é trocada, os hosts ficam
		{[]string{"sh", "-c", "curl http://a:K1@h1/x;curl http://c:K2@h2/y"}, "sh -c curl http://a:••••@h1/x;curl http://c:••••@h2/y"},
		{[]string{"sh", "-c", "curl http://a:K1@h1;curl http://c:K2@h2/y"}, "sh -c curl http://a:••••@h1;curl http://c:••••@h2/y"},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		for _, s := range []string{"Q9", "K1", "K2"} {
			assert.NotContains(t, got, s)
		}
	}
}

func TestMaskCmd_Bearer(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"curl", "-H", "Authorization: Bearer T0K3N", "https://api"}, "curl -H Authorization: Bearer •••• https://api"},
		{[]string{"curl", "-H", "authorization: bearer T0K3N"}, "curl -H authorization: bearer ••••"},
		{[]string{"curl", "-H", "Authorization:Bearer T0K3N"}, "curl -H Authorization:Bearer ••••"},
		{[]string{"sh", "-c", `curl -H "Authorization: Bearer T0K3N" https://api`}, `sh -c curl -H "Authorization: Bearer ••••" https://api`},
		{[]string{"sh", "-c", `curl -H 'Authorization: BEARER T0K3N'`}, `sh -c curl -H 'Authorization: BEARER ••••'`},
	}
	for _, c := range cases {
		got := MaskCmd(c.argv)
		assert.Equal(t, c.want, got)
		assert.NotContains(t, got, "T0K3N")
	}
}
