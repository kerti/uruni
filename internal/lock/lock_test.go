package lock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPathForSitsBesideTheDatabaseFile(t *testing.T) {
	if got, want := PathFor("./uruni.db"), "./uruni.db.lock"; got != want {
		t.Errorf("PathFor(%q) = %q, want %q", "./uruni.db", got, want)
	}
}

func TestAcquireCreatesTheLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() = %v, want nil", err)
	}
	defer func() { _ = l.Release() }()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("Acquire() did not create %s: %v", path, err)
	}
}

func TestAcquireRefusesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire() = %v, want nil", err)
	}
	defer func() { _ = first.Release() }()

	_, err = Acquire(path)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire() = %v, want ErrLocked", err)
	}
	// The operator-facing message names the lock file and says plainly that
	// another instance is running — that is the whole point of the error.
	if !strings.Contains(err.Error(), path) {
		t.Errorf("second Acquire() error = %q, want it to name %q", err, path)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Acquire() error = %q, want it to say another instance is running", err)
	}
}

func TestAcquireReportsAnUnopenableLockPath(t *testing.T) {
	// A lock file the operator's URUNI_DB implies but that cannot be created —
	// here because its parent directory does not exist, which is what a typo'd
	// or unmounted data path looks like at boot.
	path := filepath.Join(t.TempDir(), "no-such-directory", "uruni.db.lock")

	_, err := Acquire(path)
	if err == nil {
		t.Fatal("Acquire() with an unopenable path = nil, want an error")
	}
	if errors.Is(err, ErrLocked) {
		t.Errorf("Acquire() = %v, want a file error rather than ErrLocked — the lock is not held, it could not be opened", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Acquire() error = %q, want it to name %q so the operator can see which path failed", err, path)
	}
}

func TestAcquireReportsAFlockFailureThatIsNotContention(t *testing.T) {
	// An exclusive flock on a valid descriptor has no real-world failure other
	// than contention, so the syscall is stubbed. The branch still has to be
	// right: a kernel refusal is not "someone else holds it", and reporting it
	// as ErrLocked would send an operator hunting for a second process that
	// does not exist.
	stubFlock(t, func(int, int) error { return unix.EIO })

	_, err := Acquire(filepath.Join(t.TempDir(), "uruni.db.lock"))
	if err == nil {
		t.Fatal("Acquire() with a failing flock = nil, want an error")
	}
	if errors.Is(err, ErrLocked) {
		t.Errorf("Acquire() = %v, want the underlying error rather than ErrLocked", err)
	}
	if !errors.Is(err, unix.EIO) {
		t.Errorf("Acquire() = %v, want it to wrap the syscall error", err)
	}
}

func TestReleaseReportsAFailedUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() = %v, want nil", err)
	}

	// Stubbed only after the lock is held, so the failure lands on the unlock.
	stubFlock(t, func(int, int) error { return unix.EBADF })

	if err := l.Release(); err == nil {
		t.Error("Release() with a failing unlock = nil, want an error")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("Release() error = %q, want it to name %q", err, path)
	}
}

func TestReleaseTwiceIsANoOpAndNeverTouchesAReusedDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() = %v, want nil", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release() = %v, want nil", err)
	}

	// serve releases through a defer, so an explicit release followed by the
	// deferred one is an ordinary shape, not an abuse. The second call must not
	// reach the syscall at all: the descriptor is closed, and the kernel is free
	// to have given that number to any file opened since — unlocking which would
	// release a lock this process never took.
	stubFlock(t, func(int, int) error {
		t.Error("second Release() called flock on a closed descriptor")
		return nil
	})

	if err := l.Release(); err != nil {
		t.Errorf("second Release() = %v, want nil", err)
	}
}

// stubFlock replaces the flock syscall for one test and restores it afterwards.
func stubFlock(t *testing.T, fn func(fd, how int) error) {
	t.Helper()
	original := flock
	flock = fn
	t.Cleanup(func() { flock = original })
}

func TestReleaseAllowsAnOrdinaryRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire() = %v, want nil", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release() = %v, want nil", err)
	}

	// A clean stop-then-start is the ordinary restart path (`make
	// server-restart`): the second Acquire must succeed with no manual
	// cleanup of the lock file.
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() after Release() = %v, want nil", err)
	}
	defer func() { _ = second.Release() }()
}

// TestStaleLockFromAKilledProcessDoesNotWedge is the acceptance criterion
// that a lock left by a killed process must not permanently wedge the
// instance. It spawns this same test binary as a subprocess that acquires
// the lock and blocks forever, kills that subprocess the same way
// `make server-stop` escalates to once its grace period elapses, waits for
// the kernel to actually reap it, and then asserts a fresh Acquire succeeds
// immediately — no polling, no sleep, because cmd.Wait returning is itself
// the guarantee that the file descriptor holding the lock is gone.
func TestStaleLockFromAKilledProcessDoesNotWedge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	//nolint:gosec // os.Args[0] is this same test binary, re-executed with a
	// flag that selects the helper test below — the standard Go idiom for
	// running production code in a real subprocess, not user input.
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperAcquireAndBlock$")
	cmd.Env = append(os.Environ(), "URUNI_LOCK_TEST_HELPER=1", "URUNI_LOCK_TEST_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() = %v, want nil", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the helper process = %v, want nil", err)
	}

	// Blocks on the child's own stdout until it writes its ready line — a
	// real synchronization point, not a sleep guessing how long Acquire takes.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper process reported %q, %v, want \"locked\"", line, err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the helper process = %v, want nil", err)
	}
	// Wait, not a poll loop: once it returns, the kernel has already reclaimed
	// every file descriptor the killed process held, flock included.
	_ = cmd.Wait()

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() after the holder was killed = %v, want nil (a stale lock must not wedge)", err)
	}
	defer func() { _ = l.Release() }()
}

// TestHelperAcquireAndBlock is not a real test: it is `go test`'s standard
// way to get a subprocess running production code, gated so it is a no-op
// under a normal `go test` run. See TestStaleLockFromAKilledProcessDoesNotWedge.
func TestHelperAcquireAndBlock(t *testing.T) {
	if os.Getenv("URUNI_LOCK_TEST_HELPER") != "1" {
		t.Skip("not invoked as a helper process")
	}

	l, err := Acquire(os.Getenv("URUNI_LOCK_TEST_PATH"))
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	_ = l // held until this process is killed by the parent test

	_, _ = os.Stdout.WriteString("locked\n")
	select {} // block until killed; this process is never let exit cleanly
}
