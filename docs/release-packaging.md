# v1.2 release-candidate packaging

This prepares artifacts, not a tag or publication. Archive names remain
`walite_<version>_<os>_<arch>.tar.gz`, with SHA-256 entries in `SHA256SUMS`.
No test fixtures, credentials, session/cache files or downloaded media
belong in an archive. Payload verification uses an exact file whitelist.

## Build on the correct host

Linux defaults to both Linux architectures; macOS defaults to both Darwin
architectures. `WALITE_TARGETS` can select one supported target. A Darwin target
on Linux fails before outputs are created: no dummy app or Linux Swift substitute.
Go 1.26 is required; native helper builds additionally use Apple Command Line
Tools (`xcrun swiftc`, `sips`, `iconutil`, `plutil`, `lipo`, `codesign`). GNU tar is
required for normalized archives; `gtar` is detected on macOS. It is preinstalled
on the selected GitHub runners, not a user/runtime dependency. Checksums use
`sha256sum` or macOS `shasum -a 256`.

Linux amd64 candidate, without replacing an installed binary:

```sh
walite_dist=$(mktemp -d /tmp/walite-rc.XXXXXX)
WALITE_TARGETS=linux/amd64 WALITE_DIST_DIR="$walite_dist" \
  ./scripts/build-release.sh v1.2.0-rc1
./scripts/verify-release.sh v1.2.0-rc1 linux/amd64 "$walite_dist"
./scripts/test-release.sh v1.2.0-rc1 "$walite_dist"
```

On the corresponding native Mac:

```sh
./platform/macos-notifier/test.sh
walite_dist=$(mktemp -d /private/tmp/walite-rc.XXXXXX)
WALITE_TARGETS=darwin/arm64 WALITE_DIST_DIR="$walite_dist" \
  ./scripts/build-release.sh v1.2.0-rc1
./scripts/verify-release.sh v1.2.0-rc1 darwin/arm64 "$walite_dist"
```

Use `darwin/amd64` on Intel. The helper is built inside a private staging tree,
never into the installed Applications directory. Its target is explicitly
`arm64-apple-macosx12.0` or `x86_64-apple-macosx12.0`; source, official icon,
bundle ID, versions and ad hoc signing are shared across architectures. This
deployment target aligns with Go 1.26's macOS 12 minimum; older macOS runtime
acceptance is not claimed. Version `v1.2.0-rc1` uses helper display version
`1.2.0` and build `4`. Raise the explicit helper build number for future
published helper updates; never change its permanent identifier.

Archive ordering, owners, modes, gzip headers and timestamps are normalized;
timestamps use the HEAD commit time or explicit `SOURCE_DATE_EPOCH`. Compiler/SDK
versions must be held fixed for a byte comparison. Cross-SDK Swift/icon/signature
byte reproducibility is not promised. The verifier checks the extracted helper's
architecture, icon presence, plist identity/version and strict signature on Mac;
it does not launch the helper, request permissions or connect to WhatsApp.

## GitHub Actions

The existing workflow retains validate-before-build and tag-only publication.
Its matrix builds Linux archives on Ubuntu, Darwin amd64 on `macos-15-intel`,
and Darwin arm64 on `macos-15`. Both Mac jobs run native helper tests before
packaging and verify the extracted app before uploading. The assembly job
requires exactly the four expected archives and verifies the combined checksums.
Failure of any validation/build/verification prevents publication.

`workflow_dispatch` accepts a candidate version (default `v1.2.0-rc1`) and uploads
the combined four archives plus `SHA256SUMS` as an artifact retained for seven
days. **Manual dispatch never publishes, including when its ref is a tag.**
The workflow must first be available on GitHub's default branch for the Run
workflow UI; after approval it can be dispatched against an appropriate ref.
Preparing these files does not push, dispatch, tag or publish anything.

## Acceptance gates

- Run full Go tests/vet and race checks serially on small machines. The known
  `TestCoreLazy100000HistoryLivePrecedenceAndRetention` is slow under race;
  use an adequate timeout, e.g. `go test -race -p 1 -timeout=90m ./...`, not
  weaker assertions. Quiet active tests are not necessarily hung.
- Inspect Linux architecture, exact four-file content, executable mode 0755,
  exact official 48×48 PNG, help/version and checksums. Negative checks reject
  unexpected content, missing icon, nonexecutable binaries and bad checksums.
- Complete both native macOS CI builds and strict extracted signatures. No
  native result can be inferred from Linux Go cross-compilation alone.
- Follow [install-upgrade.md](install-upgrade.md), retain backups, and obtain
  native permission/delivery/click acceptance before final release. Ad hoc
  signing is not Developer ID signing, Gatekeeper approval or notarization.
