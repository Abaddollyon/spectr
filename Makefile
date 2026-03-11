.PHONY: build test lint run docker clean

build:
	go build -o bin/spectr ./cmd/spectr

test:
	go test ./... -race -cover -coverprofile=coverage.out

lint:
	golangci-lint run ./...

run: build
	./bin/spectr

docker:
	docker build -t spectr:dev .

clean:
	rm -rf bin/ coverage.out
