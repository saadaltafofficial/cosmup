# cosmup

Install, switch and check Cosmos chain binaries (`zigchaind`, `osmosisd`, `gaiad`, ...) the way `nvm` or `rustup` handle runtimes.

Cosmos chain binaries don't update themselves, and cosmovisor only manages binaries for a running node. `cosmup` is for everyone else: developers, CLI users and CI. It reads the [Cosmos chain registry](https://github.com/cosmos/chain-registry), downloads the binary for your platform, verifies its SHA256 and keeps several versions side by side.

## Install

```sh
go install github.com/saadaltafofficial/cosmup@latest

# Add to your shell rc. ~/.cosmup/bin goes first so cosmup's binaries win over
# older copies elsewhere on PATH; $(go env GOPATH)/bin is where go install put cosmup.
export PATH="$HOME/.cosmup/bin:$(go env GOPATH)/bin:$PATH"
```

## Usage

```sh
cosmup install zigchain          # registry's recommended version, verified and activated
cosmup install osmosis@v30.0.0   # a specific version (tag, version, or upgrade name like v30)
cosmup use zigchain@v5.1.0       # switch to an installed version
cosmup list                      # installed versions; * marks the active one
cosmup list zigchain             # versions in the registry, and which have a binary for your platform
cosmup check zigchain            # compare your binary with what the live network runs
```

`<chain>` is the chain's directory name in the chain registry.

`cosmup check` runs your `zigchaind version`, either cosmup's or the one on your PATH. It asks the chain's public REST endpoints (from the registry) which version the network runs and whether an upgrade is scheduled. It exits 1 when your binary doesn't match the network, so you can use it in CI.

```
Local:       v5.1.2 (/usr/local/bin/zigchaind)
Network:     v6.0.0 (via https://api.zigchain.com)
Registry:    v6.0.0 recommended
Status:      does not match the network; run: cosmup install zigchain@v6.0.0
```

## How it works

- Binaries live in `~/.cosmup/chains/<chain>/<version>/<daemon>` (override with `COSMUP_HOME`).
- `~/.cosmup/bin/<daemon>` is a symlink to the active version. Switching swaps the symlink atomically.
- Binary URLs and checksums come from `chain.json` / `versions.json` in the registry. Both `?checksum=sha256:<hex>` and bare `?checksum=<hex>` are understood.
- Archives (`.tar.gz`, `.zip`) and raw binaries are both supported; the format is detected from the file contents.
- If the registry has no checksum for a binary, `cosmup` refuses to install it unless you pass `--insecure`.
- `COSMUP_REGISTRY` points at another registry base URL (a fork, a mirror, or a local copy).

## Limitations (MVP)

- Linux and macOS only. Windows needs a different activation mechanism than symlinks.
- Mainnets only: testnets live under `testnets/` in the registry.
- Only as complete as the registry. Many chains list only `linux/amd64`, or have no checksum. `cosmup list <chain>` shows the gaps, and the fix is a PR to the registry.
- Some CosmWasm chains ship binaries that dynamically link `libwasmvm`. `cosmup` warns if the installed binary fails to run, but it doesn't install the library.

## Development

```sh
go test ./...
```

The tests run the full CLI against a fake registry, fake binaries and a fake REST node, so they need no network.

## License

Apache-2.0
