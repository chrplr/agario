# Local build and check targets for agario.
#
# The authority on what has to pass is .github/workflows/*.yml; the targets here
# mirror those jobs so a failure shows up before the push rather than after it.
# `make ci` runs what ci.yml runs, `make python-ci` what python.yml runs.

GO     ?= go
BINARY := agario
ENVBIN := agario-env

# `agario -version` prints this. Release builds stamp the tag; a local build
# stamps `git describe`, so a binary handed to someone else traces back to a
# commit rather than saying "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -X main.version=$(VERSION)

# go-sdl3 is a purego binding: it dlopens SDL3 at runtime rather than linking
# it, so every target builds from this host with no SDK, cross-toolchain or SDL
# headers. Keeping cgo off is what makes that true.
CGO_ENABLED ?= 0
export CGO_ENABLED

# The browser build needs the fork, which supplies the js/wasm bindings that
# upstream go-sdl3 still ships stubbed. Override if the checkout is elsewhere:
#   make wasm WASM_FORK=/path/to/go-sdl3-wasm
WASM_FORK ?= ../go-sdl3-wasm

DIST ?= dist

# Release matrix, the same six pairs as release.yml. All purego Tier 1, all
# cgo-free, all built from this one host.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.DEFAULT_GOAL := help

# Every target is phony: go's own build cache decides what needs recompiling,
# and a file target here would skip a rebuild after a source edit.
.PHONY: help build install run ci fmt fmt-check vet test test-fast bench smoke shot \
        replay-check wasm-check wasm wasm-serve cross python-install python-test \
        python-baselines python-ci clean $(BINARY) $(ENVBIN)

help: ## Show this help
	@echo "agario — make targets"
	@echo
	@grep -hE '^[a-zA-Z0-9_$$()-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Version stamped into builds: $(VERSION)"

# ---------------------------------------------------------------- build

build: $(BINARY) $(ENVBIN) ## Build the game and the Gymnasium environment server

agario: ## Build the playable game
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

agario-env: ## Build the Gymnasium environment server
	$(GO) build -trimpath -o $(ENVBIN) ./cmd/agario-env

install: ## go install the game into GOBIN
	$(GO) install -trimpath -ldflags="$(LDFLAGS)" .

run: ## Run the game from the checkout (make run ARGS="-seed 7")
	$(GO) run -ldflags="$(LDFLAGS)" . $(ARGS)

# ---------------------------------------------------------------- checks

ci: fmt-check vet test wasm-check smoke replay-check ## Everything ci.yml runs

fmt: ## Rewrite files with gofmt
	gofmt -w .

fmt-check: ## Fail if any file needs gofmt, as CI does
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

vet: ## go vet ./...
	$(GO) vet ./...

# The race detector needs cgo, so this one target cannot inherit CGO_ENABLED=0.
test: ## Run the Go tests with the race detector
	CGO_ENABLED=1 $(GO) test -race ./...

test-fast: ## Run the Go tests without the race detector
	$(GO) test ./...

bench: ## Benchmark the simulation step
	$(GO) test ./internal/game -bench=Step -benchtime=3s

# internal/game imports no SDL, so this runs on a machine with no display and
# no SDL3 installed. It catches panics, NaNs and runaway populations that the
# unit tests do not see.
smoke: ## Headless long run: panics, NaNs, populations
	$(GO) run . -headless -ticks 30000 -seed 7

# Records a scripted session and replays it, comparing a state checksum at every
# checkpoint. This is the check that the recording format actually round-trips:
# the unit tests exercise the package, but only this one goes through the same
# binary a player would use. -checksum-every 1 costs about 20% here and buys the
# exact tick of a divergence rather than the second it happened in.
REPLAY ?= session.jsonl.gz

replay-check: ## Record a headless session and verify it replays exactly
	$(GO) run . -headless -ticks 20000 -seed 7 -checksum-every 1 -record $(REPLAY)
	$(GO) run . -headless -replay $(REPLAY)
	@rm -f $(REPLAY)

# Unlike `smoke`, this goes through the real SDL render path — window, renderer,
# ReadPixels — so it needs a display and a usable SDL3, and it is the only check
# here that exercises internal/render at all. A fixed seed and warmup make the
# frame reproducible: the same SHOT_SEED gives the same picture byte for byte,
# so two .bmp files are worth comparing. The warmup matters for what the frame
# shows — at 0 every blob is still at the starting mass of 20 and the
# leaderboard is flat, while 3000 ticks (25 simulated seconds) spreads it to
# roughly 120 down to 80, which is what the game actually looks like.
SHOT        ?= shot.bmp
SHOT_SEED   ?= 7
SHOT_WARMUP ?= 3000
SHOT_SIZE   ?= -w 1280 -h 720

shot: ## Render one frame to shot.bmp — needs a display (SHOT=, SHOT_SEED=)
	@if [ -z "$$DISPLAY" ] && [ -z "$$WAYLAND_DISPLAY" ]; then \
		echo "No DISPLAY or WAYLAND_DISPLAY: -shot opens a real window and will fail."; \
		echo "On a headless box, run it under a virtual display:"; \
		echo "  xvfb-run -a make shot"; exit 1; fi
	$(GO) run . -shot $(SHOT) -seed $(SHOT_SEED) -warmup $(SHOT_WARMUP) $(SHOT_SIZE)

# Compiles against the *published* go-sdl3, so this only proves no
# platform-specific import has crept in that would break the browser build. The
# bundle that actually runs is built by `make wasm` against the fork.
wasm-check: ## Check the tree still compiles for js/wasm
	GOOS=js GOARCH=wasm $(GO) build ./...

# ---------------------------------------------------------------- browser

# wasmsdl runs from inside the fork and takes this checkout as its argument,
# exactly as pages.yml does. The `replace` is undone by the trap whether the
# build succeeds, fails or is interrupted: go.mod must stay on the published
# go-sdl3, or `go get`, CI and the release matrix break for everyone else.
#
# The trap's paths are absolute on purpose: it fires after the recipe has cd'd
# into the fork, so a relative path would look for go.mod over there and the
# replace would survive the build.
define with_wasm_fork
	@test -d "$(WASM_FORK)" || { \
		echo "go-sdl3-wasm not found at $(WASM_FORK). Either"; \
		echo "  git clone -b wasm-render-fixes https://github.com/chrplr/go-sdl3-wasm $(WASM_FORK)"; \
		echo "or pass WASM_FORK=/path/to/checkout"; exit 1; }
	@cp go.mod $(CURDIR)/.go.mod.bak
	@trap 'mv -f $(CURDIR)/.go.mod.bak $(CURDIR)/go.mod' EXIT INT TERM; \
	$(GO) mod edit -replace github.com/Zyko0/go-sdl3=$(abspath $(WASM_FORK)) && \
	cd $(WASM_FORK) && $(GO) run ./cmd/wasmsdl $(1)
endef

wasm: ## Build the browser bundle into dist/ (needs the go-sdl3-wasm fork)
	$(call with_wasm_fork,build -out "$(abspath $(DIST))" \
		-html "$(CURDIR)/web/index.html" "$(CURDIR)")
	@ls -lh $(DIST)

wasm-serve: ## Serve the browser build on localhost:8080 (needs the fork)
	$(call with_wasm_fork,serve -html "$(CURDIR)/web/index.html" "$(CURDIR)")

# ---------------------------------------------------------------- release

cross: ## Build every release target into dist/
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		out="$(DIST)/agario-$(VERSION)-$$goos-$$goarch"; \
		if [ "$$goos" = "windows" ]; then out="$$out.exe"; fi; \
		echo "building $$out"; \
		GOOS=$$goos GOARCH=$$goarch $(GO) build -trimpath \
			-ldflags="-s -w $(LDFLAGS)" -o "$$out" . || exit 1; \
	done
	@ls -lh $(DIST)

# ---------------------------------------------------------------- python

PY ?= python3

python-install: ## Install the Python package in editable mode with dev extras
	$(PY) -m pip install -e "python[dev]"

# The package can build its own server, but pointing AGARIO_ENV_BIN at the one
# just built is what makes the tests exercise *this* tree's simulation.
python-test: $(ENVBIN) ## Run the Gymnasium tests against the freshly built server
	AGARIO_ENV_BIN=$(abspath $(ENVBIN)) $(PY) -m pytest python/tests -q

# The end-to-end check: if greedy stops beating random, the observation encoding
# or the heading mapping has broken in a way the unit tests cannot see.
python-baselines: $(ENVBIN) ## Check the greedy baseline still beats random
	AGARIO_ENV_BIN=$(abspath $(ENVBIN)) \
		$(PY) python/examples/random_agent.py --episodes 3 --steps 600

python-ci: python-test python-baselines ## Everything python.yml runs

# ---------------------------------------------------------------- housekeeping

clean: ## Remove build outputs
	rm -f $(BINARY) $(ENVBIN) .go.mod.bak *.bmp *.test *.out *.jsonl *.jsonl.gz
	rm -rf $(DIST)
