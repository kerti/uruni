# ADR-009 — Reverse proxy & TLS: Caddy

**Status:** Accepted · [ADR index](./README.md)

**Decision.** **Caddy** in front of the Go app, automatic Let's Encrypt certificates. If an operator hosts under a wildcard subdomain, Caddy's **DNS-challenge** covers it.

**Consequences.** One extra container in compose; near-zero TLS config for a host with a domain.
