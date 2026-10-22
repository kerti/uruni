package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/lock"
)

// bootEnv points serve at dir, on a free port, and returns the base URL.
func bootEnv(t *testing.T, dir string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() = %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	t.Setenv("URUNI_DB", filepath.Join(dir, "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_UPLOADS_DIR", filepath.Join(dir, "uploads"))
	t.Setenv("URUNI_BACKUP_DIR", filepath.Join(dir, "backups"))
	t.Setenv("PORT", fmt.Sprint(port))
	for _, d := range []string{"uploads", "backups"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatalf("MkdirAll() = %v", err)
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// startServe runs serveContext in the background and returns its result channel
// and a cancel.
func startServe(t *testing.T) (<-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveContext(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
	})
	return done, cancel
}

// waitHealthy polls /healthz until 200, or fails if serve returns first or the
// deadline passes.
func waitHealthy(t *testing.T, base string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("serve returned before it was ready: %v", err)
		default:
		}
		resp, err := http.Get(base + "/healthz") //nolint:noctx // local test poll with a deadline
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("serve did not report healthy within 10s")
}

func TestServeBootsMigratesAndShutsDownCleanly(t *testing.T) {
	dir := t.TempDir()
	base := bootEnv(t, dir)
	done, cancel := startServe(t)
	waitHealthy(t, base, done)

	if _, err := os.Stat(filepath.Join(dir, "uruni.db")); err != nil {
		t.Errorf("database not created: %v", err)
	}

	// A second serve on the same directory is refused while the first holds the lock.
	if err := serveContext(context.Background()); !errors.Is(err, lock.ErrLocked) {
		t.Errorf("second serve = %v, want lock.ErrLocked", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve after cancel = %v, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down within 15s")
	}
}

func TestServeRefusesBadDirectories(t *testing.T) {
	file := func(t *testing.T) string {
		p := filepath.Join(t.TempDir(), "afile")
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatalf("WriteFile() = %v", err)
		}
		return p
	}
	readOnly := func(t *testing.T) string {
		d := t.TempDir()
		if err := os.Chmod(d, 0o500); err != nil { //nolint:gosec // a read-only dir is the point
			t.Fatalf("Chmod() = %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(d, 0o700) }) //nolint:gosec // let TempDir clean up
		if f, err := os.CreateTemp(d, "probe"); err == nil {
			_ = f.Close() // running as root: permissions do not bind
			t.Skip("directory stays writable here")
		}
		return d
	}
	tests := []struct {
		name string
		env  string
		path func(t *testing.T) string
		want string
	}{
		{"uploads missing", "URUNI_UPLOADS_DIR", func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, "does not exist"},
		{"uploads not a directory", "URUNI_UPLOADS_DIR", file, "is not a directory"},
		{"uploads unwritable", "URUNI_UPLOADS_DIR", readOnly, "is not writable"},
		{"backups missing", "URUNI_BACKUP_DIR", func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, "does not exist"},
		{"backups not a directory", "URUNI_BACKUP_DIR", file, "is not a directory"},
		{"backups unwritable", "URUNI_BACKUP_DIR", readOnly, "is not writable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bootEnv(t, t.TempDir())
			t.Setenv(tt.env, tt.path(t))
			// A deadline, so a serve that wrongly boots fails the test instead of hanging it.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := serveContext(ctx)
			if err == nil || !strings.Contains(err.Error(), tt.env) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("serve = %v, want an error naming %s that %s", err, tt.env, tt.want)
			}
		})
	}
}
