//go:build linux && (amd64 || arm64)

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestABI2FilterRejectsTruncateAndPreservesArtifactWrites(t *testing.T) {
	if os.Getenv("SUB2API_ABI2_FILTER_TEST") == "syscalls" {
		checkABI2FilterSyscalls(t)
		return
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "service-fixture")
	artifact := filepath.Join(directory, "result.html")
	const original = "fixture service configuration"
	if err := os.WriteFile(source, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	// #nosec G702 -- Execute the current test binary with a fixed test selector;
	// fixture paths are separate environment values, never shell commands.
	command := exec.Command(os.Args[0], "-test.run=^TestABI2FilterRejectsTruncateAndPreservesArtifactWrites$")
	command.Env = append(os.Environ(), "SUB2API_ABI2_FILTER_TEST=syscalls", "SUB2API_ABI2_FILTER_SOURCE="+source, "SUB2API_ABI2_FILTER_ARTIFACT="+artifact)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("filtered child failed: %v\n%s", err, output)
	}
	actual, err := os.ReadFile(source)
	if err != nil || string(actual) != original {
		t.Fatalf("truncate changed the fixture: %q, %v", actual, err)
	}
	actual, err = os.ReadFile(artifact)
	if err != nil || string(actual) != "<html>inherited filter</html>" {
		t.Fatalf("artifact was not written after exec: %q, %v", actual, err)
	}
}

func checkABI2FilterSyscalls(t *testing.T) {
	t.Helper()
	source := os.Getenv("SUB2API_ABI2_FILTER_SOURCE")
	artifact := os.Getenv("SUB2API_ABI2_FILTER_ARTIFACT")
	runtime.LockOSThread()
	if err := installABI2Filter(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Truncate(source, 0); !errors.Is(err, unix.EPERM) {
		t.Fatalf("truncate was not denied: %v", err)
	}
	if fd, err := unix.Open(source, unix.O_RDONLY|unix.O_TRUNC, 0); !errors.Is(err, unix.EPERM) {
		if err == nil {
			_ = unix.Close(fd)
		}
		t.Fatalf("read-only truncating open was not denied: %v", err)
	}
	if fd, err := unix.Openat(unix.AT_FDCWD, source, unix.O_RDONLY|unix.O_TRUNC|unix.O_CLOEXEC, 0); !errors.Is(err, unix.EPERM) {
		if err == nil {
			_ = unix.Close(fd)
		}
		t.Fatalf("read-only truncating openat was not denied: %v", err)
	}
	if fd, err := unix.Open(source, unix.O_ACCMODE|unix.O_TRUNC, 0); !errors.Is(err, unix.EPERM) {
		if err == nil {
			_ = unix.Close(fd)
		}
		t.Fatalf("ioctl-only truncating open was not denied: %v", err)
	}
	if fd, err := unix.Openat(unix.AT_FDCWD, source, unix.O_ACCMODE|unix.O_TRUNC|unix.O_CLOEXEC, 0); !errors.Is(err, unix.EPERM) {
		if err == nil {
			_ = unix.Close(fd)
		}
		t.Fatalf("ioctl-only truncating openat was not denied: %v", err)
	}
	if abi2SeccompHasOpen {
		path, err := unix.BytePtrFromString(source)
		if err != nil {
			t.Fatal(err)
		}
		_, _, errno := unix.Syscall(uintptr(abi2SeccompOpenSyscall), uintptr(unsafe.Pointer(path)), unix.O_RDONLY|unix.O_TRUNC, 0)
		runtime.KeepAlive(path)
		if errno != unix.EPERM {
			t.Fatalf("legacy read-only truncating open was not denied: %v", errno)
		}
		_, _, errno = unix.Syscall(uintptr(abi2SeccompOpenSyscall), uintptr(unsafe.Pointer(path)), unix.O_ACCMODE|unix.O_TRUNC, 0)
		runtime.KeepAlive(path)
		if errno != unix.EPERM {
			t.Fatalf("legacy ioctl-only truncating open was not denied: %v", errno)
		}
	}
	if _, err := os.ReadFile(source); err != nil {
		t.Fatalf("ordinary reads must remain available for Landlock to authorize: %v", err)
	}
	file, err := os.OpenFile(artifact, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("temporary artifact"); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(4); err != nil {
		t.Fatalf("ftruncate on an authorized writable file must remain usable: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, errno := unix.Syscall6(unix.SYS_OPENAT2, 0, 0, 0, 0, 0, 0); errno != unix.ENOSYS {
		t.Fatalf("openat2 must return ENOSYS: %v", errno)
	}
	if _, _, errno := unix.Syscall(unix.SYS_IO_URING_SETUP, 0, 0, 0); errno != unix.ENOSYS {
		t.Fatalf("io_uring_setup must return ENOSYS: %v", errno)
	}
	// The replacement process exercises the actual truncate syscall; command
	// line truncate utilities may instead open a writable FD and ftruncate it,
	// which this filter intentionally leaves to Landlock to authorize.
	if err := os.Setenv("SUB2API_ABI2_FILTER_TEST", "inherited"); err != nil {
		t.Fatal(err)
	}
	// #nosec G702 -- Replace this process with the same test binary and static
	// argv; no shell evaluates fixture paths supplied separately in the env.
	err = syscall.Exec(os.Args[0], []string{os.Args[0], "-test.run=^TestABI2FilterInheritedInChild$"}, os.Environ())
	t.Fatalf("exec failed: %v", err)
}

func TestABI2FilterInheritedInChild(t *testing.T) {
	if os.Getenv("SUB2API_ABI2_FILTER_TEST") != "inherited" {
		return
	}
	runtime.LockOSThread()
	if err := unix.Truncate(os.Getenv("SUB2API_ABI2_FILTER_SOURCE"), 0); !errors.Is(err, unix.EPERM) {
		t.Fatalf("truncate was not denied after exec: %v", err)
	}
	if err := os.WriteFile(os.Getenv("SUB2API_ABI2_FILTER_ARTIFACT"), []byte("<html>inherited filter</html>"), 0600); err != nil {
		t.Fatalf("artifact write failed after exec: %v", err)
	}
}

func TestABI2FilterRejectsMarkedSyscallABI(t *testing.T) {
	if os.Getenv("SUB2API_ABI2_FILTER_TEST") == "marked-abi" {
		runtime.LockOSThread()
		if err := installABI2Filter(); err != nil {
			t.Fatal(err)
		}
		_, _, _ = unix.RawSyscall(uintptr(unix.SYS_GETPID)|0x40000000, 0, 0, 0)
		t.Fatal("a syscall with the x32 ABI marker survived")
	}
	// #nosec G702 -- Execute only the current test binary with static argv.
	command := exec.Command(os.Args[0], "-test.run=^TestABI2FilterRejectsMarkedSyscallABI$")
	command.Env = append(os.Environ(), "SUB2API_ABI2_FILTER_TEST=marked-abi")
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("marked syscall must kill the child: %v", err)
	}
	status, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGSYS {
		t.Fatalf("marked syscall exited without SIGSYS: %v", err)
	}
}
