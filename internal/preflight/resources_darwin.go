//go:build darwin

package preflight

import (
	"fmt"

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
	avail := uint64(stat.Bavail) * uint64(stat.Bsize)
	if avail < minDiskBytes {
		return Result{Name: "disk-space", OK: false,
			Message: fmt.Sprintf("only %.1f GiB free, need at least %.0f GiB", gib(avail), gib(minDiskBytes)),
			Hint:    "free up disk space before running `fedctl up`"}
	}
	return Result{Name: "disk-space", OK: true, Message: fmt.Sprintf("%.1f GiB free", gib(avail))}
}

func checkMemory() Result {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Result{Name: "memory", OK: false, Message: "cannot read system memory info: " + err.Error()}
	}
	if total < minMemoryBytes {
		return Result{Name: "memory", OK: false,
			Message: fmt.Sprintf("only %.1f GiB total RAM, need at least %.0f GiB", gib(total), gib(minMemoryBytes)),
			Hint:    "ClickHouse and Superset both need headroom; add memory or lower their limits in compose.yaml"}
	}
	return Result{Name: "memory", OK: true, Message: fmt.Sprintf("%.1f GiB total RAM", gib(total))}
}

func gib(b uint64) float64 { return float64(b) / (1024 * 1024 * 1024) }

// cgroups are a Linux kernel concept; on macOS, Docker Desktop runs
// containers inside a managed Linux VM whose cgroup configuration this
// host process cannot see or influence, so this check is not applicable.
func checkCgroupVersion() Result {
	return Result{Name: "cgroup-version", OK: true, Message: "not applicable on macOS (Docker Desktop manages the Linux VM)"}
}

// SELinux is Linux-specific; not applicable on macOS.
func checkSELinux() Result {
	return Result{Name: "selinux", OK: true, Message: "not applicable on macOS"}
}
