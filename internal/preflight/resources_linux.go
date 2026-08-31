//go:build linux

package preflight

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const minDiskBytes = 5 * 1024 * 1024 * 1024   // 5 GiB
const minMemoryBytes = 2 * 1024 * 1024 * 1024 // 2 GiB

func diskAndMemoryChecks() []Result {
	return []Result{checkDisk(), checkMemory()}
}

func checkDisk() Result {
	var stat unix.Statfs_t
	if err := unix.Statfs(".", &stat); err != nil {
		return Result{Name: "disk-space", OK: false, Message: "cannot stat filesystem: " + err.Error()}
	}
	avail := stat.Bavail * uint64(stat.Bsize)
	if avail < minDiskBytes {
		return Result{Name: "disk-space", OK: false,
			Message: fmt.Sprintf("only %.1f GiB free, need at least %.0f GiB", gib(avail), gib(minDiskBytes)),
			Hint:    "free up disk space before running `fedctl up`"}
	}
	return Result{Name: "disk-space", OK: true, Message: fmt.Sprintf("%.1f GiB free", gib(avail))}
}

func checkMemory() Result {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return Result{Name: "memory", OK: false, Message: "cannot read system memory info: " + err.Error()}
	}
	total := uint64(info.Totalram) * uint64(info.Unit)
	if total < minMemoryBytes {
		return Result{Name: "memory", OK: false,
			Message: fmt.Sprintf("only %.1f GiB total RAM, need at least %.0f GiB", gib(total), gib(minMemoryBytes)),
			Hint:    "ClickHouse and Superset both need headroom; add memory or lower their limits in compose.yaml"}
	}
	return Result{Name: "memory", OK: true, Message: fmt.Sprintf("%.1f GiB total RAM", gib(total))}
}

func gib(b uint64) float64 { return float64(b) / (1024 * 1024 * 1024) }

func checkCgroupVersion() Result {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return Result{Name: "cgroup-version", OK: true, Message: "cgroup v2"}
	}
	if _, err := os.Stat("/sys/fs/cgroup/memory"); err == nil {
		return Result{Name: "cgroup-version", OK: false,
			Message: "host is on cgroup v1",
			Hint:    "cgroup v2 is strongly recommended for accurate container memory limits; check your Docker/host configuration"}
	}
	return Result{Name: "cgroup-version", OK: true, Message: "could not determine cgroup version (not necessarily a problem)"}
}

func checkSELinux() Result {
	if _, err := os.Stat("/sys/fs/selinux"); err == nil {
		return Result{Name: "selinux", OK: true,
			Message: "SELinux is present — bind-mounted config directories use the `:z` suffix, which is required here"}
	}
	return Result{Name: "selinux", OK: true, Message: "SELinux not present"}
}
