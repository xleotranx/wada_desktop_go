# Third-party licenses

Wada's own source is covered by the root [MIT license](../../LICENSE).
Dependencies retain their upstream licenses and copyright notices.

- [inventory.json](inventory.json) records the pinned runtime modules, versions
  and SHA-256 hashes of the copied notices.
- [DEPENDENCIES.txt](DEPENDENCIES.txt) contains the complete collected license
  and notice texts, including the Go standard library, MyGo, Onevra, Lucide,
  wada-kit, PiG and transitive runtime dependencies.
- `pig/` retains PiG's upstream license, provenance notice and third-party notices.
  The aggregate also contains the canonical texts referenced by those notices.

Run `make licenses` after changing Go versions or dependencies, then commit both
generated files. `make licenses-check` compares them with macOS and Windows
runtime dependency graphs on amd64 and arm64. Build tools and test-only packages
are not part of this runtime inventory; native libraries supplied by MyGo retain
their upstream licensing. SQLite itself is public-domain software.

MyGo copies this entire `resources/` directory into packaged app resources. The
root Wada license is separately listed in `mygo.json` so it ships too. The
`make package` target refuses a stale inventory. Review the
inventory whenever changing dependencies or adding bundled third-party assets;
a new asset's license is not automatically discovered from its filename.
