# syntax=docker/dockerfile:1

# Multi-arch (linux/amd64 + linux/arm64) without QEMU: every build stage runs on
# the *build* platform, and the Go compiler cross-compiles to the target. That is
# only possible because CGO is off (pure-Go SQLite driver, ADR-004) — emulating
# the `npm ci` stage under QEMU instead would cost tens of minutes per release.

# 1) Build the React PWA -> web/dist
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2) Build the Go binary with the SPA embedded via embed.FS.
#    CGO_ENABLED=0 requires the pure-Go SQLite driver (modernc.org/sqlite) so the
#    final image can be static/distroless.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
# Both files are required now that the module has dependencies (M1.3: the SQLite
# driver and goose). This was `go.sum*` while there were none — a literal COPY of
# a file that doesn't exist fails the whole build — and is deliberately back to
# the strict form: a missing go.sum should fail here, not be silently built
# without checksum verification.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
# VERSION and COMMIT are what `uruni version` reports, and that line is the
# operator's half of the upgrade contract (ADR-018) — an image that says `dev`
# makes the contract unverifiable. release.yml fills both from the pushed tag.
# COMMIT needs its own build-arg because .dockerignore keeps .git out of the
# build context, so Go's own VCS stamping has nothing to read here.
ARG VERSION=dev
ARG COMMIT=""
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/uruni ./cmd/uruni

# 2b) Stage the writable data directories. Docker seeds a fresh named volume from
#     the image's directory at that path — including its ownership. If the path
#     does not exist in the image, the volume is created root:root and the
#     nonroot runtime user cannot write it (SQLite opens read-only, receipt
#     uploads fail). Distroless has no shell, so mkdir must happen here.
FROM build AS dirs
RUN mkdir -p /stage/data /stage/uploads /stage/backups

# 3) Minimal runtime
FROM gcr.io/distroless/static-debian12:nonroot
# 65532 is distroless' `nonroot` uid/gid — numeric so the chown never depends on
# name lookup in the target image.
COPY --from=dirs --chown=65532:65532 /stage/data /data
COPY --from=dirs --chown=65532:65532 /stage/uploads /uploads
COPY --from=dirs --chown=65532:65532 /stage/backups /backups
COPY --from=build /out/uruni /uruni
EXPOSE 8080
USER nonroot:nonroot
# No shell and no curl in distroless, so the binary checks itself (ADR-019).
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/uruni", "healthcheck"]
ENTRYPOINT ["/uruni"]
