//go:build linux && (amd64 || arm64)

package main

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// installABI2Filter closes the truncation gap in Landlock ABI 2. The caller
// must stay on its locked OS thread from installation until exec; all threads
// and tool processes created by the executed CLI then inherit this filter.
// Landlock still decides which files may be opened for reading or writing.
func installABI2Filter() error {
	filter := abi2TruncateFilter()
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("seccomp no-new-privileges: %w", err)
	}
	_, _, errno := unix.Syscall6(unix.SYS_PRCTL, unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0, 0)
	runtime.KeepAlive(filter)
	runtime.KeepAlive(program)
	if errno != 0 {
		return fmt.Errorf("install ABI 2 truncation filter: %w", errno)
	}
	return nil
}

func abi2TruncateFilter() []unix.SockFilter {
	const (
		loadWord         = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
		jumpEqual        = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
		jumpBits         = unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K
		returnConstant   = unix.BPF_RET | unix.BPF_K
		permissionDenied = uint32(unix.SECCOMP_RET_ERRNO) | uint32(unix.EPERM)
		unavailable      = uint32(unix.SECCOMP_RET_ERRNO) | uint32(unix.ENOSYS)
	)
	filter := []unix.SockFilter{
		{Code: loadWord, K: 4}, // struct seccomp_data.arch
		{Code: jumpEqual, K: abi2SeccompArchitecture, Jt: 1},
		{Code: returnConstant, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: loadWord, K: 0}, // struct seccomp_data.nr
		// x32 shares AUDIT_ARCH_X86_64 but uses different syscall numbers.
		// Reject its marker, and unknown marked numbers on arm64, fail closed.
		{Code: jumpBits, K: 0x40000000, Jf: 1},
		{Code: returnConstant, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: jumpEqual, K: unix.SYS_TRUNCATE, Jf: 1},
		{Code: returnConstant, K: permissionDenied},
		// openat2 keeps flags behind a pointer that classic seccomp cannot
		// inspect. ENOSYS permits standard-library fallbacks to openat.
		{Code: jumpEqual, K: unix.SYS_OPENAT2, Jf: 1},
		{Code: returnConstant, K: unavailable},
		// No inherited ring descriptor is permitted by the launcher. Prevent
		// creating a ring that could issue opens without this syscall filter.
		{Code: jumpEqual, K: unix.SYS_IO_URING_SETUP, Jf: 1},
		{Code: returnConstant, K: unavailable},
	}
	if abi2SeccompHasOpen {
		filter = appendABI2OpenFilter(filter, abi2SeccompOpenSyscall, 1)
	}
	filter = appendABI2OpenFilter(filter, unix.SYS_OPENAT, 2)
	return append(filter, unix.SockFilter{Code: returnConstant, K: unix.SECCOMP_RET_ALLOW})
}

func appendABI2OpenFilter(filter []unix.SockFilter, syscallNumber uint32, flagsArgument uint32) []unix.SockFilter {
	// Both supported audit architectures are little endian. Open flags are
	// interpreted from the low 32 bits of seccomp_data.args[flagsArgument].
	// Access mode 3 creates an ioctl-only descriptor with neither FMODE_READ
	// nor FMODE_WRITE; ABI 2 cannot deny its O_TRUNC through open permissions.
	// Both mode 0 and mode 3 therefore need a separate truncation prohibition.
	return append(filter,
		unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: syscallNumber, Jf: 6},
		unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16 + flagsArgument*8},
		unix.SockFilter{Code: unix.BPF_ALU | unix.BPF_AND | unix.BPF_K, K: unix.O_ACCMODE | unix.O_TRUNC},
		unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.O_RDONLY | unix.O_TRUNC, Jt: 1},
		unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.O_ACCMODE | unix.O_TRUNC, Jf: 1},
		unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: uint32(unix.SECCOMP_RET_ERRNO) | uint32(unix.EPERM)},
		unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	)
}
