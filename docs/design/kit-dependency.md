# Kit dependency maintenance

Wada consumes `github.com/xleotranx/wada-kit` as a tagged Go module. The kit is
maintained in its [own repository](https://github.com/xleotranx/wada-kit), with its
own local checks, tests, docs and releases. Wada owns its app contracts, feature
plugins, PiG backend, storage, branding and packaging. The kit never imports Wada.

## Adopted release

Wada pins wada-kit v0.2.0 with MyGo v0.7.4 and Onevra v0.2026.13. The shell
leaves automatic plugin restart disabled, and the PiG backend explicitly opts
out even if a host configures a default. Backend recovery stays explicit; failed
create/send/reply/rename/delete operations are not automatically retried.

Shutdown cancels plugin lifetimes before UI disposal. SDK cleanup stays in its
tracked worker; UI resources close in OnDispose, and navigation work stays in
child scopes. Cleanup does not depend on events or pipelines from disposed scopes.
Settings schemas and app data paths are unchanged by this upgrade. See the kit's
[v0.2 upgrade guide](https://github.com/xleotranx/wada-kit/blob/v0.2.0/docs/upgrading-v0.2.md).

## Reproducible builds

`go.mod` and `go.sum` identify Wada's kit release. Normal builds require no kit
checkout, local replacement, Git submodule or workspace. The kit is public and
uses normal Go module downloads. Local builds and CI need no kit-specific GitHub
credentials, deploy key, SSH URL rewrite, or `GOPRIVATE` configuration. Each app
owns its independently pinned dependency and CI checks.

## Coordinated local development

With separate Wada and kit checkouts under one parent directory, run from that
parent:

```sh
go work init ./wada-kit ./wada_desktop_go
```

Normal commands in Wada then use the local kit. Keep that workspace local and
uncommitted. `GOWORK=off make check` validates the declared release; run the kit's
own gate in its checkout too. Changes to the kit belong in its repository rather
than copied source or edits under the module cache.

## Upgrades

1. Read the kit's changelog and check compatibility with Wada's MyGo/Onevra
   requirements and settings format.
2. Update with `GOWORK=off go get github.com/xleotranx/wada-kit@<version>`,
   then run `GOWORK=off go mod tidy`.
3. Update the versioned kit documentation links in `README.md`, `AGENTS.md` and
   `docs/design/shell-and-plugins.md` to the adopted tag.
4. Run `make licenses` to refresh notices for the selected module graph.
5. Run `GOWORK=off make check` and `GOWORK=off go build ./...`, then commit the
   dependency, checks and documentation together. CI also runs with workspaces off.

App and kit releases are independent. During kit v0, breaking changes use a minor
bump with upgrade notes. A v1 stability commitment follows public API review and
validation by another independent app. Extraction changes no APIs or database
schemas.
