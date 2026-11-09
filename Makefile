.PHONY: build test golden lint install

build:
	go build -o gh-pj-synker .

test:
	go test -race ./...

# Rewrite the golden transcripts after an intended output change, then review the diff.
golden:
	go test ./internal/cli -update

lint:
	go vet ./...
	golangci-lint run ./...

install: build
	gh extension install .
