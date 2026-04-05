.PHONY: build run test vet tidy clean

BIN := bin/claude-proxy

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/claude-proxy

run: build
	./$(BIN)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin state.json
