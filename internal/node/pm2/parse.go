package pm2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var jsonStart = regexp.MustCompile(`\[\s*[\{\]]`)

// jlistItem declara apenas os campos usados; o resto do pm2_env (incluindo
// variáveis de ambiente) é descartado pelo decoder.
type jlistItem struct {
	Name  string `json:"name"`
	PMID  int    `json:"pm_id"`
	PID   int    `json:"pid"`
	Monit struct {
		Memory uint64  `json:"memory"`
		CPU    float64 `json:"cpu"`
	} `json:"monit"`
	Env struct {
		Status      string `json:"status"`
		RestartTime int    `json:"restart_time"`
		Cwd         string `json:"pm_cwd"`
		Uptime      int64  `json:"pm_uptime"` // ms desde a época
	} `json:"pm2_env"`
}

// parseJList lê a saída de `pm2 jlist`, ignorando avisos antes do primeiro "[".
func parseJList(out []byte, now time.Time) ([]Process, error) {
	// Avisos do PM2 também começam com "[" ("[PM2] ..."); a lista JSON é o
	// primeiro "[" seguido de "{" ou "]".
	loc := jsonStart.FindIndex(out)
	if loc == nil {
		return nil, errors.New("saída do pm2 sem lista JSON")
	}
	var items []jlistItem
	if err := json.NewDecoder(bytes.NewReader(out[loc[0]:])).Decode(&items); err != nil {
		return nil, fmt.Errorf("ler saída do pm2: %w", err)
	}
	ps := make([]Process, 0, len(items))
	for _, it := range items {
		p := Process{
			Name:        it.Name,
			PMID:        it.PMID,
			PID:         it.PID,
			Status:      it.Env.Status,
			CPUPercent:  it.Monit.CPU,
			MemoryBytes: it.Monit.Memory,
			Restarts:    it.Env.RestartTime,
			Cwd:         it.Env.Cwd,
		}
		if it.Env.Status == "online" && it.Env.Uptime > 0 {
			if s := (now.UnixMilli() - it.Env.Uptime) / 1000; s > 0 {
				p.UptimeSeconds = s
			}
		}
		ps = append(ps, p)
	}
	return ps, nil
}
