//go:build linux

// codex-test-sandbox confines the official Codex process and all of its
// descendants to one private intelligence-test directory. Network access is
// deliberately unchanged so that Codex can contact the selected upstream.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const minimumLandlockABI = 2

const readAccess = uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)

// REFER prevents link/rename escapes. ABI 3 also handles TRUNCATE; ABI 2 uses
// an additional seccomp policy for the truncate operations it cannot handle.
// Creating device nodes is never granted.
const baseHandledAccess = readAccess | uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE|
	unix.LANDLOCK_ACCESS_FS_REMOVE_DIR|unix.LANDLOCK_ACCESS_FS_REMOVE_FILE|
	unix.LANDLOCK_ACCESS_FS_MAKE_CHAR|unix.LANDLOCK_ACCESS_FS_MAKE_DIR|
	unix.LANDLOCK_ACCESS_FS_MAKE_REG|unix.LANDLOCK_ACCESS_FS_MAKE_SOCK|
	unix.LANDLOCK_ACCESS_FS_MAKE_FIFO|unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK|
	unix.LANDLOCK_ACCESS_FS_MAKE_SYM|unix.LANDLOCK_ACCESS_FS_REFER)

var readRuntimePaths = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib64", "/opt/codex",
	"/etc/ssl", "/etc/pki", "/etc/ca-certificates",
	"/etc/os-release", "/etc/debian_version", "/etc/alpine-release",
	"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/gai.conf",
	"/etc/localtime", "/etc/passwd", "/etc/group", "/etc/shells",
	"/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d",
	"/etc/profile", "/etc/profile.d", "/etc/bash.bashrc",
	"/dev/random", "/dev/urandom",
}

var writeDevicePaths = []string{"/dev/null", "/dev/zero", "/dev/tty", "/dev/ptmx", "/dev/pts"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Codex filesystem sandbox:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "--check" {
		return checkSupport()
	}
	if len(args) < 3 || args[2] != "--" {
		return errors.New("usage: codex-test-sandbox EXECUTABLE TASK_DIRECTORY -- [ARGS...]")
	}
	if !filepath.IsAbs(args[0]) || !filepath.IsAbs(args[1]) {
		return errors.New("executable and task directory must be absolute paths")
	}
	executable, err := filepath.EvalSymlinks(args[0])
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	taskDirectory, err := filepath.EvalSymlinks(args[1])
	if err != nil {
		return fmt.Errorf("resolve task directory: %w", err)
	}
	if taskDirectory == "/" {
		return errors.New("the filesystem root cannot be a task directory")
	}
	if err := validateTaskLocation(taskDirectory, executable); err != nil {
		return err
	}
	// #nosec G703 -- This intentional directory argument is canonical and
	// absolute; the server supplies a private task directory, not prompt text.
	info, err := os.Stat(taskDirectory)
	if err != nil || !info.IsDir() {
		return errors.New("task directory must be an existing directory")
	}
	abi, err := landlockABI()
	if err != nil {
		return err
	}

	// Landlock restricts the calling OS thread. Keep installation and exec on
	// this same thread; a goroutine migration would otherwise evade the rules.
	runtime.LockOSThread()
	if err := restrictFilesystem(executable, taskDirectory, abi); err != nil {
		return err
	}
	// #nosec G702 -- Execute the configured canonical CLI directly, with
	// separate argv elements and no shell. User prompts are supplied on stdin.
	return syscall.Exec(executable, append([]string{args[0]}, args[3:]...), os.Environ())
}

func validateTaskLocation(taskDirectory, executable string) error {
	paths := append([]string{executable}, readRuntimePaths...)
	paths = append(paths, writeDevicePaths...)
	for _, path := range paths {
		resolved, err := filepath.EvalSymlinks(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve permitted runtime path: %w", err)
		}
		if pathContains(taskDirectory, resolved) || pathContains(resolved, taskDirectory) {
			return errors.New("task directory must not overlap a permitted runtime path")
		}
	}
	return nil
}

func pathContains(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func checkSupport() (resultErr error) {
	abi, err := landlockABI()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	ruleset, _, err := createRuleset(abi)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(ruleset)) }()
	// An empty ruleset denies all handled filesystem operations. Installing it
	// in this short-lived preflight process checks actual enforcement support,
	// without reading credentials or modifying the filesystem.
	return applyRuleset(ruleset, abi)
}

func landlockABI() (int, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, fmt.Errorf("landlock is unavailable: %w", errno)
	}
	if abi < minimumLandlockABI {
		return 0, fmt.Errorf("landlock ABI %d is too old; ABI %d or newer is required", abi, minimumLandlockABI)
	}
	return int(abi), nil
}

func restrictFilesystem(executable, taskDirectory string, abi int) (resultErr error) {
	ruleset, access, err := createRuleset(abi)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(ruleset)) }()
	taskAccess := access &^ uint64(unix.LANDLOCK_ACCESS_FS_MAKE_CHAR|unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK)
	if err := addPathRule(ruleset, taskDirectory, taskAccess, false); err != nil {
		return err
	}
	for _, path := range readRuntimePaths {
		if err := addPathRule(ruleset, path, readAccess, true); err != nil {
			return err
		}
	}
	if err := addPathRule(ruleset, executable, readAccess, false); err != nil {
		return err
	}
	for _, path := range writeDevicePaths {
		if err := addPathRule(ruleset, path, readAccess|unix.LANDLOCK_ACCESS_FS_WRITE_FILE, true); err != nil {
			return err
		}
	}
	return applyRuleset(ruleset, abi)
}

func createRuleset(abi int) (int, uint64, error) {
	// Passing only the original 8-byte handled_access_fs field also works on
	// newer kernels, without opting into unrelated network or scope policies.
	access := baseHandledAccess
	if abi >= 3 {
		access |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	result, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&access)), unsafe.Sizeof(access), 0)
	if errno != 0 {
		return -1, 0, fmt.Errorf("create ruleset: %w", errno)
	}
	ruleset := int(result)
	if _, err := unix.FcntlInt(uintptr(ruleset), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		return -1, 0, errors.Join(fmt.Errorf("mark ruleset close-on-exec: %w", err), unix.Close(ruleset))
	}
	return ruleset, access, nil
}

func applyRuleset(ruleset, abi int) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no-new-privileges: %w", err)
	}
	if abi == 2 {
		if err := installABI2Filter(); err != nil {
			return err
		}
	}
	// The launcher accepts only stdin/stdout/stderr from the service. Mark all
	// other descriptors close-on-exec so no outside writable FD can bypass an
	// ABI 2 path check through ftruncate after exec.
	if err := unix.CloseRange(3, ^uint(0), unix.CLOSE_RANGE_CLOEXEC); err != nil {
		return fmt.Errorf("prevent inherited file descriptors: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("apply ruleset: %w", errno)
	}
	return nil
}

func addPathRule(ruleset int, path string, access uint64, optional bool) (resultErr error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if optional && errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open permitted runtime path %s: %w", path, err)
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(fd)) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect permitted runtime path %s: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		access &= unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("allow runtime path %s: %w", path, errno)
	}
	return nil
}
