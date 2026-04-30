BIN_DIR := bin
BINARY := $(BIN_DIR)/orders

.PHONY: build run test test-fast test-race test-cover bench escape clean

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) ./examples/orders

run: build
	./$(BINARY)

# Full suite. Boots embedded Postgres on first run (~30s download); cached after.
test:
	go test -timeout 5m ./...

# Non-Postgres packages only — fast feedback loop while iterating on
# the framework or in-process code paths.
test-fast:
	go test $$(go list ./... | grep -v -E 'store/postgres|internal/pgtest')

# Race detector. The bus, dispatcher, and claim code do enough cross-
# goroutine work that this catches things plain `test` won't.
test-race:
	go test -race -timeout 10m ./...

# HTML coverage report at coverage.html.
test-cover:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "open coverage.html"

bench:
	go test -bench=. -benchmem ./...

escape:
	go build -gcflags="-m" ./...

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html
