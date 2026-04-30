BIN_DIR := bin
BINARY := $(BIN_DIR)/orders

.PHONY: build run test bench clean

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) ./examples/orders

run: build
	./$(BINARY)

test:
	go test ./...

bench:
	go test -bench=. -benchmem ./...

escape:
	go build -gcflags="-m" ./...

clean:
	rm -rf $(BIN_DIR)