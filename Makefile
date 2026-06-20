.PHONY: build bump version test clean install-hooks install-git-alias

# ── Version management ──────────────────────────────────────────────
#   make bump              auto-increment (prerelease# or patch)
#   make bump-alpha        switch to alpha channel
#   make bump-beta         switch to beta channel
#   make bump-rc           switch to rc channel
#   make bump-release      drop prerelease, clean version
#   make bump-set V=2.1.0  manually set base version

bump:
	@./scripts/bump auto

bump-alpha:
	@./scripts/bump alpha

bump-beta:
	@./scripts/bump beta

bump-rc:
	@./scripts/bump rc

bump-release:
	@./scripts/bump release

bump-set:
	@test -n "$(V)" || (echo "Usage: make bump-set V=x.y.z" && exit 1)
	@./scripts/bump set $(V)

version:
	@cat .version | head -1

# ── Setup ───────────────────────────────────────────────────────────
install-hooks:
	@./scripts/install-hooks

install-git-alias:
	@echo "Add this to your shell profile (.zshrc / .bashrc):"
	@echo '  alias git='"'"'$(PWD)/scripts/git-wrapper.sh'"'"

# ── Build ───────────────────────────────────────────────────────────
VERSION ?= $(shell cat .version | head -1)

build:
	go build -ldflags "-X cmd.version=$(VERSION)" -o codex-proxy .

# ── Test ────────────────────────────────────────────────────────────
test:
	go test ./...

# ── Clean ───────────────────────────────────────────────────────────
clean:
	rm -f codex-proxy
