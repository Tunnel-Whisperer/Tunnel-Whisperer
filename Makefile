BINARY  := tw
CMD     := ./cmd/tw
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tunnelwhisperer/tw/internal/version.Version=$(VERSION)

# Honor the toolchain pin in go.mod (the vendored xray-core requires go 1.26).
# 'auto' lets go fetch the pinned toolchain when the base go is older; switch
# back to 'local' once the system go is >= the go.mod toolchain directive.
export GOTOOLCHAIN := auto

# Patched xray-core: adds outbound client-cert (mutual-TLS) support that upstream
# lacks. The patched copy is generated and git-ignored; only the regen script and
# the patch are committed. It is rebuilt whenever the script or patch changes, and
# is referenced by the `replace` directive in go.mod. See scripts/patch-xray-core.sh.
PATCHED_XRAY := .xray-core-patched
$(PATCHED_XRAY)/.tw-patched: scripts/patch-xray-core.sh scripts/xray-core-client-cert.patch
	./scripts/patch-xray-core.sh

.PHONY: build build-linux build-windows build-darwin build-all run clean proto patch-xray

patch-xray:
	./scripts/patch-xray-core.sh

build: $(PATCHED_XRAY)/.tw-patched
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

build-linux: $(PATCHED_XRAY)/.tw-patched
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

build-windows: $(PATCHED_XRAY)/.tw-patched
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY).exe $(CMD)

build-darwin: $(PATCHED_XRAY)/.tw-patched
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
