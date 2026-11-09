// Package pm2 lê os processos do PM2 sem nunca alterá-los.
package pm2

import "context"

// Process é um processo gerenciado pelo PM2.
type Process struct {
	Name          string  `json:"name"`
	PMID          int     `json:"pmId"`
	PID           int     `json:"pid"`    // PID do app; 0 quando parado
	Status        string  `json:"status"` // online, stopped, errored, ...
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryBytes   uint64  `json:"memoryBytes"`
	Restarts      int     `json:"restarts"`
	Cwd           string  `json:"cwd"`
	UptimeSeconds int64   `json:"uptimeSeconds"` // 0 quando não está online
}

// Snapshot é o retrato dos processos do PM2.
type Snapshot struct {
	Available     bool      `json:"available"` // binário pm2 encontrado
	DaemonRunning bool      `json:"daemonRunning"`
	Processes     []Process `json:"processes"`
}

// Reader lê o estado do PM2.
type Reader interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}
