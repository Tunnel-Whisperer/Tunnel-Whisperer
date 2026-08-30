BINARY  := tw
CMD     := ./cmd/tw
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tunnelwhisperer/tw/internal/version.Version=$(VERSION)

# Honor the toolchain pin in go.mod (xray-core requires go 1.26). 'auto' lets go
# fetch the pinned toolchain when the base go is older; switch back to 'local'
# once the system go is >= the go.mod toolchain directive.
export GOTOOLCHAIN := auto

.PHONY: build build-linux build-windows build-darwin build-all winres run clean proto e2e e2e-up e2e-down bench

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

build-linux:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

build-windows: winres
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY).exe $(CMD)

# Embed a version-info resource + manifest into the Windows binary. An anonymous
# PE (no metadata, no manifest) scores far worse in Defender's ML heuristics.
winres:
	go tool go-winres make --in $(CMD)/winres/winres.json --out $(CMD)/rsrc --arch amd64 --file-version "$(VERSION)" --product-version "$(VERSION)"

build-darwin:
	@mkdir -p $(BIN_DIR)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-darwin $(CMD)

build-all: build-linux build-windows build-darwin

run: build
	./$(BIN_DIR)/$(BINARY)

clean:
	rm -rf $(BIN_DIR)

proto:
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/api/v1/service.proto

e2e-up:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o e2e/images/tw/tw $(CMD)
	GOOS=linux GOARCH=amd64 go build -o e2e/images/tw/echo-server ./e2e/images/tw/echo
	GOOS=linux GOARCH=amd64 go build -o e2e/images/tw/socks5-server ./e2e/images/tw/socks5
	docker compose -f e2e/docker-compose.yaml up -d --build

e2e-down:
	docker compose -f e2e/docker-compose.yaml down -v

e2e: e2e-up
	# -skip (not -run '^TestE2E$$'): -run would also silently exclude this
	# package's own -tags e2e unit tests (TestParse*, TestSummarize, etc).
	cd e2e && go test -tags e2e -count=1 -timeout 30m -skip '^TestBench$$' -v . ; status=$$?; \
	cd ..; \
	if [ -z "$$E2E_KEEP" ]; then $(MAKE) e2e-down; fi; \
	exit $$status

# Throughput & footprint benchmark (pure SSH vs SSH-over-WireGuard vs
# SSH-over-tw) on the e2e topology. Opt-in, not part of `make e2e`/CI.
# Needs kernel WireGuard + netem on the Docker host:
#   sudo modprobe wireguard sch_netem
# Knobs: BENCH_SIZE_GB (5), BENCH_RUNS (3), BENCH_WAN (30ms:0.1% | off), E2E_KEEP.
# Always starts from a clean topology: TestBench reuses RelayInstall, which needs a fresh relay.
bench: e2e-down e2e-up
	@[ -d /sys/module/wireguard ] || { echo "WireGuard kernel module not loaded — run: sudo modprobe wireguard sch_netem"; exit 1; }
	@[ "$${BENCH_WAN:-30ms:0.1%}" = off ] || [ -d /sys/module/sch_netem ] || { echo "netem kernel module not loaded — run: sudo modprobe wireguard sch_netem  (or BENCH_WAN=off)"; exit 1; }
	cd e2e && go test -tags e2e -count=1 -timeout 90m -run '^TestBench$$' -v . ; status=$$?; \
	[ $$status -eq 0 ] && cp bench-chart.svg bench-chart-dark.svg ../docs/assets/; \
	cd ..; \
	if [ -z "$$E2E_KEEP" ]; then $(MAKE) e2e-down; fi; \
	exit $$status
