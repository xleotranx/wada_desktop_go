# The gate for a change: lifetime vet, lint, then the tests under the race
# detector and once more without cgo. Run `make check` before calling
# anything done.
.PHONY: check vet lint test test-nocgo

check: vet lint test test-nocgo

vet:
	go tool mygo vet .

# Pinned here rather than as a go.mod tool: the linter's module graph would
# add hundreds of indirect requires to the app's dependency list.
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run

test:
	go test -race ./...

test-nocgo:
	CGO_ENABLED=0 go test ./...
