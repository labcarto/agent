<a href="https://labcarto.com/"><picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand/labcarto-logo-dark-bg.svg">
  <img src="brand/labcarto-logo-light-bg.svg" alt="Labcarto" width="240">
</picture></a>

# Labcarto agent

The program the [Labcarto](https://labcarto.com/) setup asks you to run. It runs `docker ps` and `docker inspect` (read-only), reduces each container to
names, images, state, ports, networks, mount paths and env variable **names**, and either writes a local runbook or sends that
JSON to your Labcarto server with `--push`. Env values, file contents and logs are never read or sent (see `Normalize` in
`internal/lab/normalize.go` and `TestNormalize`).

- See exactly what would be sent, without sending it: `~/.labcarto --dry-run`
- Main program: [`cmd/agent/main.go`](cmd/agent/main.go). Standard library only, no dependencies.

## Build it yourself

This builds the same bytes as the binary Labcarto serves. Compare the hash with the SHA-256 shown in Settings.

```sh
for a in amd64:x86_64 arm64:aarch64; do
  docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false -e GOARCH=${a%:*} golang:1.25.14 \
    go build -trimpath -ldflags=-s -o labcarto-${a#*:} ./cmd/agent
done
sha256sum labcarto-*
```

Or use your own build instead of the download: put it at `~/.labcarto` and skip step 1.

---

Labcarto turns these snapshots into a homelab map, a living runbook and a change log: **[labcarto.com](https://labcarto.com/)**
