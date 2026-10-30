# Source publication

Wada's repository is prepared for public source distribution. Repository visibility
is a separate maintainer action; publishing source does not create a signed binary
release or change provider accounts, local data, or API credentials.

## Included

- The root MIT license for Wada and a pinned MIT-licensed wada-kit release.
- Build and contributor instructions, security policy, changelog, issue templates
  and desktop/design-canvas screenshots.
- A versioned runtime license inventory and complete collected notices, checked
  during CI and packaging. MyGo includes them in the app's resources.
- Lifetime vet, lint, license verification, race tests, no-cgo tests, secret scanning,
  known-vulnerability scanning and Windows compilation in CI.

Generated `designs/` output is kept locally and ignored by Git. Conversation data,
provider credentials, databases, signing keys and build output are not source
release inputs. The publishable branch is `main`; local recovery bundles and
Codex-internal refs are not part of the source release.

## Verification

Run from the source checkout:

```sh
GOWORK=off make check
GOWORK=off go build ./...
make secrets
make audit
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
go tool mygo build -platform darwin/arm64 -skip-dmg
```

Check that the packaged app contains `LICENSE` and
`licenses/DEPENDENCIES.txt`, `licenses/inventory.json`, and the PiG notices.
The artifact is a locally ad-hoc-signed build, not a notarized public installer.

The dependency audit addresses GO-2026-6860 and GO-2026-6866 by selecting
`golang.org/x/net v0.62.0`. Those advisories concern the HTML renderer; the initial
scan did not find vulnerable imported packages or reachable calls in Wada.
[Go vulnerability report](https://pkg.go.dev/vuln/GO-2026-6866).
Re-run the scanner at publication time because the advisory database changes.

## Historical GitHub surfaces

The merged pull request #1 and 17 downloadable historical Actions logs were
reviewed during preparation. A secret scan flagged 12 plain runner/checkout commit
hashes as Sourcegraph candidates; each was verified as a hash, with no `sgp_`
credential prefix. Seven historical runs had no downloadable logs. No repository
issues, stored Actions artifacts or Actions secrets were present at review time.
These findings do not change the scanner's rules or suppress future matches.

The old pull request remains on GitHub and exposes its historical code diff when
the repository becomes public. Rewriting `main` does not erase pull requests or
Actions metadata. No historical GitHub records were deleted during preparation.

## Publish the repository

1. Review the exact files and `main` history, the MIT license and third-party
   inventory. Also review GitHub issues, pull requests, Actions logs and artifacts
   before changing visibility; a source scan does not inspect those surfaces.
2. Confirm the current `main` CI run passes. Local test results and hosted CI are
   separate evidence; a queued run is not a passing run.
3. Change only `xleotranx/wada_desktop_go` to Public in GitHub repository settings.
4. Enable private vulnerability reporting from the Security settings when the
   repository is public. Follow `SECURITY.md` for reporting until it is available.
5. Verify an anonymous fresh clone can download modules and build. Ensure GitHub
   recognizes the root MIT license and renders the README image and links.

Keep release tags immutable. Binary releases need their own version, checksums,
signing/notarization and packaged-license verification; no binary release is
published by these source-preparation steps.
