.PHONY: build test lint e2e fuzz

build:
	go build -o blast-radius .

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...
	golangci-lint run --build-tags e2e ./e2e/...

e2e:
	go test -tags e2e -count=1 -v ./e2e/

fuzz:
	go test ./internal/shell/ -run '^$$' -fuzz FuzzParse -fuzztime 60s
