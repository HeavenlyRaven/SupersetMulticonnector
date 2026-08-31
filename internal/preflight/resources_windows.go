//go:build windows

package preflight

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const minDiskBytes = 5 * 1024 * 1024 * 1024   // 5 GiB
const minMemoryBytes = 2 * 1024 * 1024 * 1024 // 2 GiB

func diskAndMemoryChecks() []Result {
	return []Result{checkDisk(), checkMemory()}
}

func checkDisk() Result {
	dir, err := windows.UTF16PtrFromString(".")
	if err != nil {
		return Result{Name: "disk-space", OK: false, Message: "cannot resolve current directory: " + err.Error()}
	}
	var freeAvail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(dir, &freeAvail, &total, &totalFree); err != nil {
		return Result{Name: "disk-space", OK: false, Message: "cannot stat filesystem: " + err.Error()}
	}
	if freeAvail < minDiskBytes {
		return Result{Name: "disk-space", OK: false,
			Message: fmt.Sprintf("only %.1f GiB free, need at least %.0f GiB", gib(freeAvail), gib(minDiskBytes)),
			Hint:    "free up disk space before running `fedctl up` — remember this runs inside WSL2/Docker Desktop's disk image on Windows"}
	}
	return Result{Name: "disk-space", OK: true, Message: fmt.Sprintf("%.1f GiB free", gib(freeAvail))}
}

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct. golang.org/x/sys
// does not wrap GlobalMemoryStatusEx, so this calls kernel32 directly —
// the same LazyDLL/LazyProc mechanism x/sys/windows itself is built on.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

func checkMemory() Result {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	ret, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if ret == 0 {
		return Result{Name: "memory", OK: false, Message: "cannot read system memory info: " + callErr.Error()}
	}
	total := m.TotalPhys
	if total < minMemoryBytes {
		return Result{Name: "memory", OK: false,
			Message: fmt.Sprintf("only %.1f GiB total RAM, need at least %.0f GiB", gib(total), gib(minMemoryBytes)),
			Hint:    "ClickHouse and Superset both need headroom; increase the RAM allocated to Docker Desktop / WSL2"}
	}
	return Result{Name: "memory", OK: true, Message: fmt.Sprintf("%.1f GiB total RAM", gib(total))}
}

func gib(b uint64) float64 { return float64(b) / (1024 * 1024 * 1024) }

// cgroups and SELinux are Linux kernel concepts; on Windows, containers
// run inside Docker Desktop's Linux VM (WSL2), which this host process
// cannot introspect, so these checks are not applicable.
func checkCgroupVersion() Result {
	return Result{Name: "cgroup-version", OK: true, Message: "not applicable on Windows (Docker Desktop manages the WSL2 VM)"}
}

func checkSELinux() Result {
	return Result{Name: "selinux", OK: true, Message: "not applicable on Windows"}
}
