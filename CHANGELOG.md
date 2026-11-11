# Changelog

All notable changes to cosmup are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.4] - 2026-11-11

### Fixed
- macOS release binaries are now built with current stable Go. The 0.2.3 binaries were built with Go 1.22, which produces macOS binaries without an LC_UUID load command that current macOS refuses to run. Linux binaries were not affected.

## [0.2.3] - 2026-11-11

### Added
- Prebuilt release binaries for Linux and macOS (amd64 and arm64), with a `checksums.txt`, built by GoReleaser when a version tag is pushed. Release notes come from this changelog.
- `install.sh`, a one-line installer: `curl -sSfL https://raw.githubusercontent.com/saadaltafofficial/cosmup/main/install.sh | sh`. It picks the right binary, verifies its SHA256, and installs into `~/.cosmup/bin`. `COSMUP_VERSION` pins a release.
- CI on Linux and macOS: gofmt, `go vet` and `go test` on every push and pull request.

### Changed
- README: badges, a short check → install → check demo, the installer as the main install method, and release steps.

## [0.2.2] - 2026-11-11

### Added
- `cosmup remove <chain>@<version>` deletes one installed version. It refuses to delete the active version.
- `cosmup remove <chain>` deletes every installed version of a chain, including its link in `~/.cosmup/bin`.
- `cosmup prune` keeps one version per chain and deletes the rest. It keeps the active version, or the most recently installed one if the chain isn't active (such as a testnet sharing its binary name with the mainnet).
- Both commands report how much disk space they freed. They only delete inside `~/.cosmup`.

## [0.2.1] - 2026-11-11

### Changed
- Rewrote the notes at the end of `cosmup --help` in plain language.
- Examples in the help text and README now use the Cosmos Hub (`cosmoshub`, `gaiad`). Install examples use `osmosis`, because the registry has no checksums for recent Cosmos Hub releases.
- With nothing installed, cosmup now suggests `cosmup list cosmoshub` instead of an install command.

## [0.2.0] - 2026-11-11

### Added
- `cosmup update` installs the recommended version of every installed chain. It only switches the active binary for the chain that currently owns that binary name, so updating a testnet never replaces the mainnet binary.
- Testnet support: names not found among the registry's mainnets are looked up in its `testnets/` folder (for example `cosmoshubtestnet`).
- `cosmup cosmovisor <chain> [upgrade]` downloads and verifies an upgrade's binary and puts it in `$DAEMON_HOME/cosmovisor/upgrades/<upgrade>/bin/`. With no upgrade name, it uses the upgrade scheduled on the network. It doesn't touch the running node or cosmovisor's `current` link.

### Security
- Chain names containing `/` or `.` are rejected, so a name can't point outside the registry.

## [0.1.4] - 2026-11-11

### Changed
- The PATH note now names the exact file cosmup expected, so it reads correctly when `COSMUP_HOME` is set.

## [0.1.3] - 2026-11-11

### Removed
- The `COSMUP_REGISTRY` environment variable. cosmup now always reads from the official chain registry (`cosmos/chain-registry`).

## [0.1.2] - 2026-11-11

### Fixed
- `cosmup version` prints the installed module version (for example `v0.1.2`) instead of `dev` when installed with `go install`.

## [0.1.1] - 2026-11-11

Tagged on the same commit as 0.1.0, with no changes. The fix intended for this release shipped in 0.1.2.

## [0.1.0] - 2026-11-11

### Added
- `cosmup install <chain>[@version]` installs a chain's binary from the chain registry, verifies its SHA256 and makes it active. Versions without a checksum need `--insecure`.
- `cosmup use <chain>@<version>` switches between installed versions.
- `cosmup list` shows installed versions; `cosmup list <chain>` shows the versions in the registry.
- `cosmup check <chain>` compares your binary with the version the network runs and reports scheduled upgrades. It exits 1 on a mismatch.
- Supports `.tar.gz` and `.zip` archives and raw binaries, on Linux and macOS.

[Unreleased]: https://github.com/saadaltafofficial/cosmup/compare/v0.2.4...HEAD
[0.2.4]: https://github.com/saadaltafofficial/cosmup/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/saadaltafofficial/cosmup/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/saadaltafofficial/cosmup/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/saadaltafofficial/cosmup/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/saadaltafofficial/cosmup/compare/v0.1.4...v0.2.0
[0.1.4]: https://github.com/saadaltafofficial/cosmup/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/saadaltafofficial/cosmup/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/saadaltafofficial/cosmup/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/saadaltafofficial/cosmup/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/saadaltafofficial/cosmup/releases/tag/v0.1.0
