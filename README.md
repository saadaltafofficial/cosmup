# cosmup

[![ci](https://github.com/saadaltafofficial/cosmup/actions/workflows/ci.yml/badge.svg)](https://github.com/saadaltafofficial/cosmup/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/saadaltafofficial/cosmup)](https://github.com/saadaltafofficial/cosmup/releases/latest)
[![license](https://img.shields.io/github/license/saadaltafofficial/cosmup)](LICENSE)

Install, switch and check Cosmos chain binaries (`gaiad`, `osmosisd`, ...) the way `nvm` or `rustup` handle runtimes.

Cosmos chain binaries don't update themselves, and cosmovisor only manages binaries for a running node. `cosmup` is for everyone else: developers, CLI users and CI. It reads the [Cosmos chain registry](https://github.com/cosmos/chain-registry), downloads the binary for your platform, verifies its SHA256 and keeps several versions side by side.

```console
$ cosmup check osmosis
Local:       v30.0.0 (/usr/local/bin/osmosisd)
Network:     v31.0.0 (via https://lcd.osmosis.zone)
Registry:    v31.0.0 recommended
Status:      does not match the network; run: cosmup install osmosis@v31.0.0

$ cosmup install osmosis
Downloading osmosis v31.0.0 (linux/amd64)
Verified sha256 d435408b845e79a2594594a315d9d22797fdbb7871a5936df68f51f0df557957
Active: osmosis v31.0.0
osmosisd version: 31.0.0

$ cosmup check osmosis
...
Status:      up to date
```

## Install

```sh
curl -sSfL https://raw.githubusercontent.com/saadaltafofficial/cosmup/main/install.sh | sh
```

The script downloads the release for your OS and CPU (Linux or macOS, amd64 or arm64), verifies its SHA256 against the release's `checksums.txt`, and installs `cosmup` into `~/.cosmup/bin`. Set `COSMUP_VERSION=v0.2.3` to pin a version. Binaries are also on the [releases page](https://github.com/saadaltafofficial/cosmup/releases).

Then put `~/.cosmup/bin` first on your PATH, in your shell's rc file. It goes first so the binaries cosmup installs win over older copies elsewhere on PATH:

```sh
export PATH="$HOME/.cosmup/bin:$PATH"
```

With Go installed, you can instead build from source with `go install github.com/saadaltafofficial/cosmup@latest`. That puts `cosmup` in `$(go env GOPATH)/bin`, so add that folder to PATH too.

## Usage

```sh
cosmup list cosmoshub            # versions in the registry, and which have a binary for your platform
cosmup install osmosis           # registry's recommended version, verified and activated
cosmup install osmosis@v30.0.0   # a specific version (tag, version, or upgrade name like v30)
cosmup use osmosis@v30.0.0       # switch to an installed version
cosmup list                      # installed versions; * marks the active one
cosmup check cosmoshub           # compare your gaiad with what the live network runs
cosmup update                    # move every installed chain to its recommended version
cosmup remove osmosis@v30.0.0    # delete one installed version (not the active one)
cosmup remove osmosis            # delete every installed version of a chain
cosmup prune                     # keep only the version each chain uses; delete the rest
cosmup install cosmoshubtestnet  # testnets work too (names from the registry's testnets/ folder)
cosmup cosmovisor cosmoshub      # validators: stage the next upgrade's binary for cosmovisor
```

Use the chain's name from the chain registry, not its binary's name: `cosmoshub`, not `gaiad`. Testnets have their own names, such as `cosmoshubtestnet`.

Some chains have no checksums in the registry. Recent Cosmos Hub releases are one example, so `cosmup install cosmoshub` stops and asks for `--insecure`. Adding checksums to the registry fixes this for everyone.

`cosmup check` runs your binary's `version` command (`gaiad version` for cosmoshub), either cosmup's or the one on your PATH. It asks the chain's public REST endpoints (from the registry) which version the network runs and whether an upgrade is scheduled. It exits 1 when your binary doesn't match the network, so you can use it in CI.

```
Local:       v27.6.0 (/usr/local/bin/gaiad)
Network:     v28.0.0 (via https://rest.cosmoshub-main.ccvalidators.com:443)
Registry:    v28.0.0 recommended
Status:      does not match the network; run: cosmup install cosmoshub@v28.0.0
```

### Preparing a cosmovisor upgrade

`cosmup cosmovisor <chain> [upgrade]` downloads the upgrade's binary from the registry, verifies it, and puts it where cosmovisor looks for it:

```
$DAEMON_HOME/cosmovisor/upgrades/<upgrade name>/bin/<daemon>
```

With no upgrade name, it uses the upgrade currently scheduled on the network. `DAEMON_HOME` falls back to the chain's `node_home` from the registry (e.g. `~/.gaia` for cosmoshub), and the command refuses to run if there is no `cosmovisor` folder there. It doesn't touch the running node, cosmovisor's `current` link, or your active cosmup binary. Run it as the user cosmovisor runs as.

### update and shared daemon names

A mainnet and its testnet usually share a binary name: `cosmoshub` and `cosmoshubtestnet` both use `gaiad`. `cosmup update` only switches the active binary for the chain that currently owns that name, so updating `cosmoshubtestnet` never replaces your mainnet `gaiad`. It still installs the new testnet version; activate it with `cosmup use`.

### Cleaning up

`cosmup remove <chain>@<version>` won't delete the version you're using. Switch with `cosmup use` first, or remove the whole chain with `cosmup remove <chain>`. That also removes its binary from cosmup's PATH folder.

`cosmup prune` keeps one version per chain: the active one, or the most recently installed one if the chain isn't active (for example a testnet that shares its binary name with the mainnet). Both commands only delete inside `~/.cosmup`. Binaries you installed elsewhere and cosmovisor folders are never touched.

## How it works

- Binaries live in `~/.cosmup/chains/<chain>/<version>/<daemon>` (override with `COSMUP_HOME`).
- `~/.cosmup/bin/<daemon>` is a symlink to the active version. Switching swaps the symlink atomically.
- Binary URLs and checksums come from `chain.json` / `versions.json` in the registry. Both `?checksum=sha256:<hex>` and bare `?checksum=<hex>` are understood.
- Archives (`.tar.gz`, `.zip`) and raw binaries are both supported; the format is detected from the file contents.
- If the registry has no checksum for a binary, `cosmup` refuses to install it unless you pass `--insecure`.
- Only chains and versions listed in the official chain registry (`cosmos/chain-registry`) can be installed; the registry URL is not configurable.

## Limitations (MVP)

- Linux and macOS only. Windows needs a different activation mechanism than symlinks.
- Only as complete as the registry. Many chains list only `linux/amd64`, or have no checksum. `cosmup list <chain>` shows the gaps, and the fix is a PR to the registry.
- Some CosmWasm chains ship binaries that dynamically link `libwasmvm`. `cosmup` warns if the installed binary fails to run, but it doesn't install the library.

## Development

```sh
go test ./...
```

### Releasing

1. Move the `Unreleased` entries in `CHANGELOG.md` under a new version heading, and add its compare link at the bottom.
2. Commit, then tag and push: `git tag v0.2.3 && git push origin main v0.2.3`.

Pushing the tag runs `.github/workflows/release.yml`. It runs the tests, then GoReleaser builds the binaries and `checksums.txt` and publishes the GitHub Release, with that version's changelog section as the release notes. The release fails if the changelog has no section for the tag. To check the GoReleaser config locally: `goreleaser check && goreleaser release --snapshot --clean`.

The tests run the full CLI against a fake registry, fake binaries and a fake REST node, so they need no network.

## License

Apache-2.0
