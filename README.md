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
cosmup update                    # move every installed chain to its recommended version
cosmup install zigchaintestnet   # testnets work too (names from the registry's testnets/ folder)
cosmup cosmovisor zigchain       # validators: stage the next upgrade's binary for cosmovisor
```

`<chain>` is the chain's directory name in the chain registry, or a testnet's name under `testnets/`.

`cosmup check` runs your `zigchaind version`, either cosmup's or the one on your PATH. It asks the chain's public REST endpoints (from the registry) which version the network runs and whether an upgrade is scheduled. It exits 1 when your binary doesn't match the network, so you can use it in CI.

```
Local:       v5.1.2 (/usr/local/bin/zigchaind)
Network:     v6.0.0 (via https://api.zigchain.com)
Registry:    v6.0.0 recommended
Status:      does not match the network; run: cosmup install zigchain@v6.0.0
```

### Preparing a cosmovisor upgrade

`cosmup cosmovisor <chain> [upgrade]` downloads the upgrade's binary from the registry, verifies it, and puts it where cosmovisor looks for it:

```
$DAEMON_HOME/cosmovisor/upgrades/<upgrade name>/bin/<daemon>
```

With no upgrade name, it uses the upgrade currently scheduled on the network. `DAEMON_HOME` falls back to the chain's `node_home` from the registry (e.g. `~/.zigchain`), and the command refuses to run if there is no `cosmovisor` folder there. It doesn't touch the running node, cosmovisor's `current` link, or your active cosmup binary. Run it as the user cosmovisor runs as.

### update and shared daemon names

A mainnet and its testnet usually share a daemon name (`zigchaind`). `cosmup update` only switches the active binary for the chain that currently owns that name, so updating `zigchaintestnet` never replaces your mainnet `zigchaind`. It still installs the new testnet version; activate it with `cosmup use`.

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

The tests run the full CLI against a fake registry, fake binaries and a fake REST node, so they need no network.

## License

Apache-2.0
