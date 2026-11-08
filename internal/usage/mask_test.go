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
