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
	// The operator-facing message names the lock file and says another instance is
	// running: the point of the error.
	if !strings.Contains(err.Error(), path) {
		t.Errorf("second Acquire() error = %q, want it to name %q", err, path)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Acquire() error = %q, want it to say another instance is running", err)
	}
}

func TestAcquireReportsAnUnopenableLockPath(t *testing.T) {
	// A lock file URUNI_DB implies but that cannot be created: its parent
	// directory does not exist, as a typo'd or unmounted data path looks at boot.
	path := filepath.Join(t.TempDir(), "no-such-directory", "uruni.db.lock")

	_, err := Acquire(path)
	if err == nil {
		t.Fatal("Acquire() with an unopenable path = nil, want an error")
	}
	if errors.Is(err, ErrLocked) {
		t.Errorf("Acquire() = %v, want a file error rather than ErrLocked - the lock is not held, it could not be opened", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Acquire() error = %q, want it to name %q so the operator can see which path failed", err, path)
	}
}

func TestAcquireReportsAFlockFailureThatIsNotContention(t *testing.T) {
	// An exclusive flock on a valid descriptor fails in the real world only on
	// contention, so the syscall is stubbed. The branch must still be right: a
	// kernel refusal is not "someone else holds it", and ErrLocked would send an
	// operator hunting a second process that does not exist.
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

	// serve releases through a defer, so an explicit release then the deferred one
	// is ordinary. The second call must not reach the syscall: the descriptor is
	// closed and the kernel may have reused that number, so unlocking would
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

	// A clean stop-then-start is the ordinary restart (`make server-restart`):
	// the second Acquire must succeed with no manual cleanup.
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() after Release() = %v, want nil", err)
	}
	defer func() { _ = second.Release() }()
}

// TestStaleLockFromAKilledProcessDoesNotWedge: a lock left by a killed
// process must not wedge the instance. It spawns this test binary as a
// subprocess that acquires the lock and blocks, kills it as `make
// server-stop` escalates after its grace period, waits for the kernel to
// reap it, and asserts a fresh Acquire succeeds immediately: cmd.Wait
// returning guarantees the descriptor holding the lock is gone, so no
// polling or sleep.
func TestStaleLockFromAKilledProcessDoesNotWedge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db.lock")

	//nolint:gosec // os.Args[0] is this same test binary, re-executed with a
	// flag that selects the helper test below - the standard Go idiom for
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

	// Blocks on the child's stdout until its ready line: a real synchronization
	// point, not a sleep guessing how long Acquire takes.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper process reported %q, %v, want \"locked\"", line, err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the helper process = %v, want nil", err)
	}
	// Wait, not a poll loop: once it returns, the kernel has reclaimed every
	// descriptor the killed process held, flock included.
	_ = cmd.Wait()

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() after the holder was killed = %v, want nil (a stale lock must not wedge)", err)
	}
	defer func() { _ = l.Release() }()
}

// TestHelperAcquireAndBlock is not a real test: `go test`'s standard way to
// get a subprocess running production code, a no-op under a normal run. See
// TestStaleLockFromAKilledProcessDoesNotWedge.
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
