.PHONY: all build run test clean

BINARY_NAME=bin/atlas

all: test build

build:
	@mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY_NAME) ./cmd/atlas

run: build
	./$(BINARY_NAME)

test:
	go test -v ./internal/...

clean:
	rm -rf bin/
