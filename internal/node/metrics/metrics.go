// Package metrics define a fonte de métricas do host.
package metrics

import (
	"context"
	"time"
)

// Disk descreve o uso de um ponto de montagem.
type Disk struct {
	Mount      string `json:"mount"`
	FSType     string `json:"fstype"`
	UsedBytes  uint64 `json:"usedBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

// Sample é uma amostra instantânea das métricas do host.
type Sample struct {
	Time             time.Time `json:"time"`
	CPUPercent       float64   `json:"cpuPercent"`
	MemUsedBytes     uint64    `json:"memUsedBytes"` // Total - Available
	MemTotalBytes    uint64    `json:"memTotalBytes"`
	Load1            float64   `json:"load1"`
	Load5            float64   `json:"load5"`
	Load15           float64   `json:"load15"`
	NetRxBytesPerSec float64   `json:"netRxBytesPerSec"`
	NetTxBytesPerSec float64   `json:"netTxBytesPerSec"`
	Disks            []Disk    `json:"disks"`
}

// Source produz amostras de métricas.
type Source interface {
	Sample(ctx context.Context) (Sample, error)
}
