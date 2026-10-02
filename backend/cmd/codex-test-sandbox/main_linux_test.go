//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSandboxHelperEntrypoint(t *testing.T) {
	if os.Getenv("CODEX_SANDBOX_TEST_ENTRYPOINT") != "1" {
		return
	}
	for i, argument := range os.Args {
		if argument == "--sandbox-helper" {
			if err := run(os.Args[i+1:]); err != nil {
				if _, writeErr := os.Stderr.WriteString(err.Error()); writeErr != nil {
					os.Exit(2)
				}
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(1)
}

func TestSandboxKernelAttackEntrypoint(t *testing.T) {
	if os.Getenv("CODEX_SANDBOX_TEST_ATTACK") != "1" {
		return
	}
	target := os.Args[len(os.Args)-1]
	if err := os.Truncate(target, 0); err == nil {
		t.Fatal("direct truncate escaped the filesystem sandbox")
	}
	fd, err := unix.Open(target, unix.O_RDONLY|unix.O_TRUNC, 0)
	if err == nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("read-only truncate escaped the filesystem sandbox")
	}
	fd, err = unix.Open(target, unix.O_ACCMODE|unix.O_TRUNC, 0)
	if err == nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("ioctl-only truncate escaped the filesystem sandbox")
	}
	if _, err := os.Stdout.WriteString("ATTACKS_BLOCKED\n"); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxRequiresExplicitPrivateTaskDirectory(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"/bin/sh", "/tmp"},
		{"sh", "/tmp", "--"},
		{"/bin/sh", "relative", "--"},
		{"/bin/sh", "/", "--"},
		{"/bin/sh", "/usr", "--"},
		{"/bin/sh", "/usr/bin", "--"},
	} {
		if err := run(args); err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
}

func TestSandboxTaskLocationCannotExposeRuntimeTrees(t *testing.T) {
	for _, task := range []string{"/usr", "/usr/private-job", "/etc", "/dev/pts/private-job"} {
		if err := validateTaskLocation(task, "/bin/sh"); err == nil {
			t.Fatalf("task overlaps runtime permission: %s", task)
		}
	}
	if err := validateTaskLocation(filepath.Join(t.TempDir(), "private-job"), "/bin/sh"); err != nil {
		t.Fatalf("private temporary task directory rejected: %v", err)
	}
}

func TestSandboxPreflightChecksRequiredABI(t *testing.T) {
	_, probeErr := landlockABI()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(testExecutable, "-test.run=^TestSandboxHelperEntrypoint$", "--", "--sandbox-helper", "--check")
	command.Env = []string{"CODEX_SANDBOX_TEST_ENTRYPOINT=1", "PATH=/usr/bin:/bin"}
	output, checkErr := command.CombinedOutput()
	if probeErr != nil && checkErr == nil {
		t.Fatalf("preflight accepted a kernel missing required support: %v", probeErr)
	}
	if probeErr == nil && checkErr != nil {
		t.Fatalf("preflight could not enforce sandbox: %v\n%s", checkErr, output)
	}
}

func TestSandboxFilesystemBoundaries(t *testing.T) {
	if _, err := landlockABI(); err != nil {
		t.Skipf("host cannot enforce required Landlock policy: %v", err)
	}
	root := t.TempDir()
	task := filepath.Join(root, "task")
	if err := os.Mkdir(task, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "server-secret")
	if err := os.WriteFile(secret, []byte("OUTSIDE_PRIVATE_DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(task, "escape")); err != nil {
		t.Fatal(err)
	}
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(task, "attack-probe")
	testBinary, err := os.ReadFile(testExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attacker, testBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pass paths through argv, never through a shell-interpolated prompt. These
	// checks run in a descendant shell, exercising inheritance of the policy.
	command := exec.Command(testExecutable, "-test.run=^TestSandboxHelperEntrypoint$", "--", "--sandbox-helper", "/bin/sh", task, "--", "-c", `
set -eu
printf 'ARTIFACT' > result.html
test "$(cat result.html)" = ARTIFACT
if cat "$1" >/dev/null 2>&1; then echo outside-readable; exit 11; fi
if cat escape >/dev/null 2>&1; then echo symlink-readable; exit 12; fi
if printf overwrite > "$1" 2>/dev/null; then echo outside-writable; exit 13; fi
if /bin/sh -c 'cat "$1"' sh "$1" >/dev/null 2>&1; then echo child-readable; exit 14; fi
if cat /proc/self/environ >/dev/null 2>&1; then echo proc-readable; exit 15; fi
if ln "$1" linked 2>/dev/null; then echo outside-linked; exit 16; fi
./attack-probe -test.run=^TestSandboxKernelAttackEntrypoint$ "$1"
printf 'BOUNDARIES_OK'
`, "sh", secret)
	command.Dir = task
	command.Env = []string{"CODEX_SANDBOX_TEST_ENTRYPOINT=1", "CODEX_SANDBOX_TEST_ATTACK=1", "PATH=/usr/bin:/bin"}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("sandbox boundary probe failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "BOUNDARIES_OK") || !strings.Contains(output.String(), "ATTACKS_BLOCKED") {
		t.Fatalf("sandbox did not execute the complete probe: %s", output.String())
	}
	artifact, err := os.ReadFile(filepath.Join(task, "result.html"))
	if err != nil || string(artifact) != "ARTIFACT" {
		t.Fatalf("task write failed: artifact=%q, error=%v", artifact, err)
	}
	unchanged, err := os.ReadFile(secret)
	if err != nil || string(unchanged) != "OUTSIDE_PRIVATE_DATA" {
		t.Fatalf("outside file changed: content=%q, error=%v", unchanged, err)
	}
}
