# uwutv — build helpers
BIN := uwutv
export CGO_ENABLED = 0

.PHONY: all build run clean tidy fmt vet

all: build

go.sum: go.mod
	go mod tidy

build: go.sum
	go build -trimpath -ldflags="-s -w" -o $(BIN) .

run: build
	./$(BIN)

tidy:
	go mod tidy

fmt:
	gofmt -w *.go

vet:
	go vet ./...

clean:
	rm -f $(BIN)
