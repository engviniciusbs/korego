package pm2

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return b
}

// 1760000000000 ms + 90 s.
var parseNow = time.UnixMilli(1760000090000)

func TestParseJList(t *testing.T) {
	ps, err := parseJList(readTestdata(t, "jlist.json"), parseNow)
	require.NoError(t, err)
	require.Len(t, ps, 2)

	assert.Equal(t, Process{
		Name: "api-exemplo", PMID: 0, PID: 4242, Status: "online", CPUPercent: 1.5,
		MemoryBytes: 73400320, Restarts: 3, Cwd: "/srv/exemplo", UptimeSeconds: 90,
	}, ps[0])
	assert.Equal(t, Process{
		Name: "worker-exemplo", PMID: 1, Status: "stopped",
		Cwd: "/srv/exemplo-worker",
	}, ps[1])
}

func TestParseJList_WarningPrefix(t *testing.T) {
	want, err := parseJList(readTestdata(t, "jlist.json"), parseNow)
	require.NoError(t, err)
	got, err := parseJList(readTestdata(t, "jlist-with-warning.txt"), parseNow)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestParseJList_Invalid(t *testing.T) {
	_, err := parseJList([]byte("sem json"), parseNow)
	assert.Error(t, err)
	_, err = parseJList([]byte("[{"), parseNow)
	assert.Error(t, err)
}

func TestParseJList_Empty(t *testing.T) {
	ps, err := parseJList([]byte("[]"), parseNow)
	require.NoError(t, err)
	assert.NotNil(t, ps)
	assert.Empty(t, ps)
}

func TestParseJList_DropsEnv(t *testing.T) {
	raw := readTestdata(t, "jlist.json")
	require.Contains(t, string(raw), "SEGREDO_TESTE")
	ps, err := parseJList(raw, parseNow)
	require.NoError(t, err)
	out, err := json.Marshal(ps)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "SEGREDO_TESTE")
	assert.NotContains(t, string(out), "valor-ficticio")
}

func TestParseJList_PID(t *testing.T) {
	b, err := os.ReadFile("testdata/jlist.json")
	require.NoError(t, err)
	ps, err := parseJList(b, time.Now())
	require.NoError(t, err)
	require.Len(t, ps, 2)
	assert.Equal(t, 4242, ps[0].PID)
	assert.Equal(t, 0, ps[1].PID)
}
