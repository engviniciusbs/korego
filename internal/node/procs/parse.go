package procs

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
)

var errStat = errors.New("stat inválido")

// parseStat lê /proc/<pid>/stat. O comm (campo 2) pode ter espaços e
// parênteses, por isso o corte é no último parêntese de fechamento.
func parseStat(b []byte) (pid, ppid int, comm string, utime, stime, start uint64, err error) {
	s := string(b)
	open := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		err = errStat
		return
	}
	pid, err = strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		err = errStat
		return
	}
	comm = s[open+1 : end]
	// f[0] é o campo 3 (state): o campo n está em f[n-3].
	f := strings.Fields(s[end+1:])
	if len(f) < 20 {
		err = errStat
		return
	}
	if ppid, err = strconv.Atoi(f[1]); err != nil {
		err = errStat
		return
	}
	if utime, err = strconv.ParseUint(f[11], 10, 64); err != nil {
		err = errStat
		return
	}
	if stime, err = strconv.ParseUint(f[12], 10, 64); err != nil {
		err = errStat
		return
	}
	if start, err = strconv.ParseUint(f[19], 10, 64); err != nil {
		err = errStat
		return
	}
	return
}

// parseStatusRSSUID lê VmRSS (kB, ausente em threads de kernel = 0) e o uid
// real de /proc/<pid>/status. uid é -1 quando ausente.
func parseStatusRSSUID(b []byte) (rss uint64, uid int) {
	uid = -1
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "VmRSS:"):
			if f := strings.Fields(line[len("VmRSS:"):]); len(f) > 0 {
				if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
					rss = kb * 1024
				}
			}
		case strings.HasPrefix(line, "Uid:"):
			if f := strings.Fields(line[len("Uid:"):]); len(f) > 0 {
				if v, err := strconv.Atoi(f[0]); err == nil {
					uid = v
				}
			}
		}
	}
	return
}

// parseCgroup devolve o caminho do cgroup: a linha "0::" (v2); senão a
// primeira linha cujo caminho contém "docker"; senão o primeiro caminho.
func parseCgroup(b []byte) string {
	var first, docker string
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if parts[0] == "0" && parts[1] == "" {
			return path
		}
		if first == "" {
			first = path
		}
		if docker == "" && strings.Contains(path, "docker") {
			docker = path
		}
	}
	if docker != "" {
		return docker
	}
	return first
}

// parseCmdline separa o argv de /proc/<pid>/cmdline (separado por NUL).
func parseCmdline(b []byte) []string {
	b = bytes.TrimSuffix(b, []byte{0})
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}

// parseProcStatTotal soma os 8 primeiros campos da linha "cpu " de /proc/stat.
// guest e guest_nice ficam de fora: já estão contados em user e nice.
func parseProcStatTotal(b []byte) uint64 {
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		var total uint64
		for i, f := range strings.Fields(line)[1:] {
			if i >= 8 {
				break
			}
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return 0
			}
			total += v
		}
		return total
	}
	return 0
}
