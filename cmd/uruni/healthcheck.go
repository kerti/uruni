package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kerti/uruni/internal/config"
)

// healthcheckTimeout is under the container HEALTHCHECK's own `--timeout=3s`, so
// a hung server produces our error rather than Docker's kill.
const healthcheckTimeout = 2 * time.Second

// healthcheck exists *only* because the runtime image is distroless — no shell,
// no curl — so the image's HEALTHCHECK has nothing else to call (ADR-019). It
// probes the server this same binary would serve, on the same PORT, and exits
// 0 when it answers.
func healthcheck() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// 127.0.0.1, not localhost: inside the container that avoids a DNS lookup
	// and any IPv6-first resolution of a server bound to :port.
	return probeHealth(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port), healthcheckTimeout)
}

func probeHealth(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Built as a request rather than http.Get so the timeout is the context's
	// and the URL is never a bare variable handed to the default client.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: %s returned %s", url, resp.Status)
	}
	return nil
}
