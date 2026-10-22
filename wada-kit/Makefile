# The gate for a change to the kit: lifetime vet, lint, then the tests under
# the race detector and once more without cgo.
.PHONY: check vet lint test test-nocgo

check: vet lint test test-nocgo

vet:
	go tool mygo vet .

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run

test:
	go test -race ./...

test-nocgo:
	CGO_ENABLED=0 go test ./...
