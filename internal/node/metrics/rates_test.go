package metrics

import (
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/stretchr/testify/assert"
)

func TestCPUPercent(t *testing.T) {
	prev := cpu.TimesStat{User: 10, Idle: 90}
	cur := cpu.TimesStat{User: 60, Idle: 140}
	assert.InDelta(t, 50.0, cpuPercent(prev, cur), 0.001)

	assert.Equal(t, 0.0, cpuPercent(prev, prev))

	// contador que recuou não pode gerar valor negativo nem acima de 100
	assert.Equal(t, 0.0, cpuPercent(cur, prev))
	over := cpu.TimesStat{User: 1000, Idle: 90}
	got := cpuPercent(prev, over)
	assert.LessOrEqual(t, got, 100.0)
	assert.GreaterOrEqual(t, got, 0.0)
}

func TestRate_FirstAndReset(t *testing.T) {
	assert.Equal(t, 500.0, rate(0, 1000, 2*time.Second))
	assert.Equal(t, 0.0, rate(5000, 100, 2*time.Second))
	assert.Equal(t, 0.0, rate(1, 2, 0))
}

func TestIncludeInterface(t *testing.T) {
	for _, n := range []string{"eth0", "ens3"} {
		assert.True(t, includeInterface(n), n)
	}
	for _, n := range []string{"lo", "docker0", "br-1a2b", "veth9f"} {
		assert.False(t, includeInterface(n), n)
	}
}

func TestFilterDisks(t *testing.T) {
	parts := []disk.PartitionStat{
		{Device: "/dev/sda1", Mountpoint: "/", Fstype: "ext4"},
		{Device: "/dev/sda2", Mountpoint: "/boot", Fstype: "ext4"},
		{Device: "/dev/loop0", Mountpoint: "/snap/core/1", Fstype: "squashfs"},
		{Device: "overlay", Mountpoint: "/var/lib/docker/overlay2/x/merged", Fstype: "overlay"},
		{Device: "/dev/sdb1", Mountpoint: "/var/lib/docker/volumes/v", Fstype: "ext4"},
		{Device: "tmpfs", Mountpoint: "/run", Fstype: "tmpfs"},
		{Device: "/dev/sda1", Mountpoint: "/mnt/x", Fstype: "ext4"},
	}
	var mounts []string
	for _, p := range filterDisks(parts) {
		mounts = append(mounts, p.Mountpoint)
	}
	assert.ElementsMatch(t, []string{"/", "/boot"}, mounts)
}
