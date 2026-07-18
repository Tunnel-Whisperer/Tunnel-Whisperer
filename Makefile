BINARY  := tw
CMD     := ./cmd/tw
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tunnelwhisperer/tw/internal/version.Version=$(VERSION)

# Honor the toolchain pin in go.mod (xray-core requires go 1.26). 'auto' lets go
# fetch the pinned toolchain when the base go is older; switch back to 'local'
# once the system go is >= the go.mod toolchain directive.
export GOTOOLCHAIN := auto

.PHONY: build build-linux build-windows build-darwin build-all run clean proto e2e e2e-up e2e-down

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

build-linux:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

build-windows:
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY).exe $(CMD)

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
	docker compose -f e2e/docker-compose.yaml up -d --build

e2e-down:
	docker compose -f e2e/docker-compose.yaml down -v

e2e: e2e-up
	cd e2e && go test -tags e2e -timeout 30m -v . ; status=$$?; \
	cd ..; \
	if [ -z "$$E2E_KEEP" ]; then $(MAKE) e2e-down; fi; \
	exit $$status
