package procs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStat_CommWithSpacesAndParens(t *testing.T) {
	pid, ppid, comm, ut, st, start, err := parseStat([]byte("123 (my (weird) proc) S 1 123 123 0 -1 4194560 0 0 0 0 77 88 0 0 20 0 1 0 4242 1000 1000\n"))
	require.NoError(t, err)
	assert.Equal(t, 123, pid)
	assert.Equal(t, 1, ppid)
	assert.Equal(t, "my (weird) proc", comm)
	assert.EqualValues(t, 77, ut)
	assert.EqualValues(t, 88, st)
	assert.EqualValues(t, 4242, start)
}

func TestParseStat_Invalid(t *testing.T) {
	for _, in := range []string{"", "garbage", "1 (x) S 1 2"} {
		_, _, _, _, _, _, err := parseStat([]byte(in))
		assert.Error(t, err, in)
	}
}

func TestParseCgroup_V2V1Empty(t *testing.T) {
	id := strings.Repeat("b", 64)
	assert.Equal(t, "/system.slice/docker-"+id+".scope",
		parseCgroup([]byte("0::/system.slice/docker-"+id+".scope\n")))
	v1 := "12:cpu,cpuacct:/user.slice\n11:memory:/docker/" + id + "\n"
	assert.Equal(t, "/docker/"+id, parseCgroup([]byte(v1)))
	assert.Equal(t, "/user.slice", parseCgroup([]byte("2:cpu:/user.slice\n1:name=systemd:/x\n")))
	assert.Equal(t, "/v2", parseCgroup([]byte("1:cpu:/docker/"+id+"\n0::/v2\n")))
	assert.Equal(t, "", parseCgroup(nil))
}

func TestParseCmdline(t *testing.T) {
	assert.Equal(t, []string{"nginx", "-g", "daemon off;"}, parseCmdline([]byte("nginx\x00-g\x00daemon off;\x00")))
	assert.Nil(t, parseCmdline([]byte("")))
}

func TestParseStatusRSSUID(t *testing.T) {
	rss, uid := parseStatusRSSUID([]byte("Name:\tx\nUid:\t33\t34\t35\t36\nVmRSS:\t  1234 kB\n"))
	assert.EqualValues(t, 1234*1024, rss)
	assert.Equal(t, 33, uid)
	rss, uid = parseStatusRSSUID([]byte("Name:\tkthreadd\n"))
	assert.EqualValues(t, 0, rss)
	assert.Equal(t, -1, uid)
}

func TestParseProcStatTotal(t *testing.T) {
	// guest e guest_nice ficam de fora: já estão em user/nice.
	assert.EqualValues(t, 36, parseProcStatTotal([]byte("cpu  1 2 3 4 5 6 7 8 9 10\ncpu0 100 100\n")))
	assert.EqualValues(t, 0, parseProcStatTotal([]byte("intr 1\n")))
}
