# The gate for a change: lifetime vet, lint, then the tests under the race
# detector and once more without cgo, plus license inventory verification.
# The independently released kit runs its own gate. Run `make check` before
# calling anything done.
.PHONY: check vet lint test test-nocgo licenses licenses-check audit secrets package demo

check: vet lint licenses-check test test-nocgo

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
# long) and 24 open tabs, loaded by embedded PiG from a temporary folder.
demo:
	@dir=$$(mktemp -d) && python3 -I scripts/seed.py "$$dir" && \
		WADA_DATA_DIR="$$dir/data" go run .

# Regenerate after changing runtime dependencies or the Go toolchain.
licenses:
	python3 -I scripts/licenses.py

licenses-check:
	python3 -I scripts/licenses.py --check

audit:
	go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...

# Scan only publishable commit history; local app data is not an input.
secrets:
	go run github.com/zricethezav/gitleaks/v8@v8.30.1 git . --log-opts=HEAD --redact=100 --no-banner

# Validate the notices before producing a local app/installer.
package: licenses-check
	go tool mygo build
