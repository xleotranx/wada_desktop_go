# Contributing to Wada

Wada is an experimental native Go desktop app. The supported development platform
is macOS; Windows compilation is checked, but interactive Windows behavior has
not been verified. Install the Go version in `go.mod` and Apple's Command Line
Tools (`xcode-select --install`) and Python 3 before building on macOS. MyGo downloads its
native dependencies on first use, so the first run needs network access.

## Start locally

```sh
git clone https://github.com/xleotranx/wada_desktop_go.git
cd wada_desktop_go
go mod download
go tool mygo dev .
```

`go run .` also runs the app. Configure a provider in Settings → Providers; tests
use a fake provider and need no paid account, API key, or developer credentials.
For experiments, use `WADA_DATA_DIR="$(mktemp -d)" go run .` to isolate app data.
Never reset another person's development settings to verify a change.

## Make a change

Discuss significant behavior or architecture changes in an issue first. Keep a
pull request focused on one problem and include reproduction steps and relevant
verification. Features belong in plugins, shared declarations in
`internal/contract`, and composition in `main.go`. Read `AGENTS.md` and the design
notes before changing these boundaries. The independently maintained
[wada-kit](https://github.com/xleotranx/wada-kit) supplies the kernel and shell;
see [kit development](docs/design/kit-dependency.md) for local workspaces.

Follow the existing Go formatting and MyGo lifetime rules. Keep plugin state on
the UI thread, tie background work to scopes, use stable UI keys, and take colors
from `native.PaletteFor`. Update design notes alongside public behavior changes.

## Verify

```sh
GOWORK=off make check
GOWORK=off go build ./...
make audit
```

`make check` runs MyGo lifetime vet, pinned lint, license inventory verification,
race tests and no-cgo tests. `make audit` runs the pinned Go vulnerability scanner.
When a dependency changes, run `make licenses` and commit the generated inventory
and notices. License generation covers macOS and Windows on amd64 and arm64.

For visual changes, include a screenshot made with throwaway data and exercise
relevant interactions. `make demo` uses synthetic data. Generated conversation
files, credentials, logs, databases and build artifacts do not belong in commits.

## Packaging

`make package` checks the license inventory and writes an app bundle and disk
image under `build/`. Local
builds are ad-hoc signed by default. Public binary distribution requires the
maintainer's signing and notarization setup; never contribute signing keys.
Do not change the bundle identifier or data-directory conventions casually.

By submitting a contribution, you agree that your contribution is provided under
the repository's MIT license. Third-party material must retain its notices and
have an identified source and license.
