# The gate for a change: lifetime vet, lint, then the tests under the race
# detector and once more without cgo, for the app and then the kit beside
# it. Run `make check` before calling anything done.
.PHONY: check vet lint test test-nocgo kit demo

check: vet lint test test-nocgo kit

kit:
	$(MAKE) -C wada-kit check

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

# The app on throwaway data: 12 folders, 56 chats (some hundreds of messages
# long) and 24 open tabs, served by Pi from a temporary folder.
demo:
	@dir=$$(mktemp -d) && python3 -I scripts/seed.py "$$dir" && \
		WADA_DATA_DIR="$$dir/data" PI_CODING_AGENT_SESSION_DIR="$$dir/pi-sessions" go run .
