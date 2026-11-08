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
		{[]string{"sh", "-c", "x --password 'a b' y"}, "sh -c x --password •••• y"},
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
