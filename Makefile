.PHONY: build version test clean

# ── Version management ──────────────────────────────────────────────
#   Version is derived from git tags at build time.
#   Channel switch: gh workflow run bump.yml -f channel=beta

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

version:
	@echo $(VERSION)

# ── Build ───────────────────────────────────────────────────────────
build:
	go build -ldflags "-X github.com/jesse/codex-app-proxy/cmd.version=$(VERSION)" -o codex-proxy .

# ── Test ────────────────────────────────────────────────────────────
test:
	go test ./...

# ── Clean ───────────────────────────────────────────────────────────
clean:
	rm -f codex-proxy
