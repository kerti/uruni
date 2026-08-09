// Command uruni is the whole product: one binary that serves the API, the
// public report and the embedded SPA (ADR-001). Its subcommand surface is
// pinned by ADR-019 — the Makefile and the container HEALTHCHECK are written
// against that table, so it is a contract, not a convenience.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kerti/uruni"
	uruniHTTP "github.com/kerti/uruni/internal/http"
)

const defaultPort = 8080

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "uruni: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("perintah belum diisi — coba: uruni serve")
	}

	switch args[0] {
	case "serve":
		return serve()
	default:
		return fmt.Errorf("perintah tidak dikenal: %s", args[0])
	}
}

// listenPort reads PORT (ADR-019); the rest of the runtime config lands with
// the CLI surface in M1.2.
func listenPort() (int, error) {
	raw := os.Getenv("PORT")
	if raw == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("PORT bukan nomor port yang sah: %q", raw)
	}
	return port, nil
}

func serve() error {
	assets, err := uruni.WebAssets()
	if err != nil {
		return fmt.Errorf("membuka aset web: %w", err)
	}

	port, err := listenPort()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: uruniHTTP.New(assets),
		// Set explicitly: a server with no header timeout can be held open by a
		// slow client indefinitely.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// SIGINT/SIGTERM close in-flight requests cleanly — `make restart` and
	// `docker compose down` both stop the process this way.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Printf("uruni listening on :%d", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
