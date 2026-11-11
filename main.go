// cosmup installs and switches Cosmos chain binaries using the Cosmos
// chain registry, and checks a local binary against the live network.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/saadaltafofficial/cosmup/internal/installer"
	"github.com/saadaltafofficial/cosmup/internal/network"
	"github.com/saadaltafofficial/cosmup/internal/registry"
	"github.com/saadaltafofficial/cosmup/internal/store"
)

var version = "dev"

// registryURL is always the official chain registry. It is a variable only
// so tests can point it at a fake registry.
var registryURL = registry.DefaultBaseURL

const usage = `cosmup - install and switch Cosmos chain binaries

Usage:
  cosmup install <chain>[@version] [--insecure]   install (default: registry's recommended version) and activate
  cosmup use <chain>@<version>                    activate an installed version
  cosmup list [chain]                             installed versions, or a chain's versions in the registry
  cosmup check <chain>                            compare your binary with the version the network runs
  cosmup update [--insecure]                      install the recommended version of every installed chain
  cosmup remove <chain>[@version]                 delete one installed version, or all of a chain's versions
  cosmup prune                                    delete every version except the one each chain uses
  cosmup cosmovisor <chain> [upgrade] [--insecure]
                                                  put a verified upgrade binary in cosmovisor's upgrades folder
                                                  (default: the upgrade scheduled on the network)
  cosmup version

Chain names:
  Use the chain's name from the Cosmos chain registry (github.com/cosmos/chain-registry),
  not the name of its binary: cosmoshub, not gaiad. Testnets have their own names,
  such as cosmoshubtestnet.

Where things go:
  cosmup keeps binaries in ~/.cosmup (set COSMUP_HOME to change it).
  Put ~/.cosmup/bin first on your PATH so cosmup's binaries are the ones that run.

cosmovisor:
  cosmup cosmovisor writes into your node's home folder: $DAEMON_HOME if set,
  otherwise the registry's default for the chain (~/.gaia for cosmoshub).
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

type app struct {
	out   io.Writer
	store store.Store
	reg   *registry.Client
	http  *http.Client
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	st, err := store.Default()
	if err != nil {
		fmt.Fprintln(stderr, "cosmup:", err)
		return 1
	}
	a := &app{
		out:   stdout,
		store: st,
		reg:   &registry.Client{BaseURL: registryURL, HTTP: &http.Client{Timeout: 30 * time.Second}},
		http:  &http.Client{},
	}

	var positional []string
	insecure := false
	for _, arg := range args[1:] {
		switch {
		case arg == "--insecure":
			insecure = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "cosmup: unknown flag %s\n", arg)
			return 1
		default:
			positional = append(positional, arg)
		}
	}

	cmd := args[0]
	switch {
	case cmd == "version":
		fmt.Fprintln(stdout, "cosmup", buildVersion())
		return 0
	case cmd == "install" && len(positional) == 1:
		err = a.install(ctx, positional[0], insecure, true)
	case cmd == "remove" && len(positional) == 1:
		err = a.remove(positional[0])
	case cmd == "prune" && len(positional) == 0:
		err = a.prune()
	case cmd == "update" && len(positional) == 0:
		err = a.update(ctx, insecure)
	case cmd == "cosmovisor" && (len(positional) == 1 || len(positional) == 2):
		err = a.prepareCosmovisor(ctx, positional, insecure)
	case cmd == "use" && len(positional) == 1:
		err = a.use(positional[0])
	case cmd == "list" && len(positional) <= 1:
		err = a.list(ctx, positional)
	case cmd == "check" && len(positional) == 1:
		var upToDate bool
		upToDate, err = a.check(ctx, positional[0])
		if err == nil && !upToDate {
			return 1
		}
	default:
		fmt.Fprint(stderr, usage)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "cosmup:", err)
		return 1
	}
	return 0
}

// buildVersion prefers a version set with -ldflags, then the module version
// that "go install module@version" records in the binary.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func splitSpec(spec string) (chain, ver string) {
	chain, ver, _ = strings.Cut(spec, "@")
	return chain, ver
}

func (a *app) install(ctx context.Context, spec string, insecure, activate bool) error {
	chainName, want := splitSpec(spec)
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return err
	}
	var versions []registry.Version
	if want != "" {
		if versions, err = a.reg.Versions(ctx, ch); err != nil {
			return err
		}
	}
	v, err := registry.Resolve(ch, versions, want)
	if err != nil {
		return err
	}

	dest := a.store.BinaryPath(chainName, v.ID(), ch.DaemonName)
	if _, err := os.Stat(dest); err == nil {
		fmt.Fprintf(a.out, "%s %s is already installed\n", chainName, v.ID())
	} else if err := a.fetch(ctx, chainName, v, ch.DaemonName, dest, insecure); err != nil {
		return err
	}
	if !activate {
		fmt.Fprintf(a.out, "Installed %s %s (not activated: %s belongs to another chain; run: cosmup use %s@%s)\n",
			chainName, v.ID(), ch.DaemonName, chainName, v.ID())
		return nil
	}
	if err := a.store.Activate(chainName, v.ID(), ch.DaemonName); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Active: %s %s\n", chainName, v.ID())
	a.smokeTest(ctx, dest, ch.DaemonName)
	a.pathHint(ch.DaemonName)
	return nil
}

// fetch downloads the version's binary for this platform to dest,
// verifying it against the registry's checksum.
func (a *app) fetch(ctx context.Context, chainName string, v registry.Version, daemon, dest string, insecure bool) error {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	raw, ok := v.Binaries[platform]
	if !ok {
		var have []string
		for p := range v.Binaries {
			have = append(have, p)
		}
		sort.Strings(have)
		return fmt.Errorf("the registry has no %s binary for %s %s (available: %s)", platform, chainName, v.ID(), orNone(have))
	}
	art, err := registry.ParseBinary(raw)
	if err != nil {
		return err
	}
	if art.SHA256 == "" && !insecure {
		return fmt.Errorf("the registry lists no checksum for %s %s, so it can't be verified\n  rerun with --insecure to install it unverified", chainName, v.ID())
	}
	fmt.Fprintf(a.out, "Downloading %s %s (%s)\n", chainName, v.ID(), platform)
	if err := installer.Install(ctx, a.http, art.URL, art.SHA256, daemon, dest); err != nil {
		return err
	}
	if art.SHA256 != "" {
		fmt.Fprintf(a.out, "Verified sha256 %s\n", art.SHA256)
	} else {
		fmt.Fprintln(a.out, "WARNING: installed without checksum verification")
	}
	return nil
}

func (a *app) smokeTest(ctx context.Context, bin, daemon string) {
	if out, err := localVersion(ctx, bin); err != nil {
		fmt.Fprintf(a.out, "WARNING: %s version failed: %v\n  CosmWasm chains may need libwasmvm installed separately.\n", daemon, err)
	} else {
		fmt.Fprintf(a.out, "%s version: %s\n", daemon, out)
	}
}

// update installs the recommended version of every installed chain. A
// chain's new version is activated unless its daemon name is currently
// active for a different chain (e.g. a testnet sharing gaiad).
func (a *app) update(ctx context.Context, insecure bool) error {
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	if len(installed) == 0 {
		fmt.Fprintln(a.out, "Nothing installed yet. See what you can install with: cosmup list cosmoshub")
		return nil
	}
	var chains []string
	activeChain := map[string]string{} // daemon -> chain that owns bin/<daemon>
	daemonOf := map[string]string{}
	for _, in := range installed {
		if daemonOf[in.Chain] == "" {
			chains = append(chains, in.Chain)
			daemonOf[in.Chain] = in.Daemon
		}
		if in.Active {
			activeChain[in.Daemon] = in.Chain
		}
	}
	var failed []string
	for _, c := range chains {
		fmt.Fprintf(a.out, "== %s\n", c)
		owner := activeChain[daemonOf[c]]
		if err := a.install(ctx, c, insecure, owner == "" || owner == c); err != nil {
			fmt.Fprintln(a.out, "cosmup:", err)
			failed = append(failed, c)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("update failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

// remove deletes one installed version (never the active one) or, with no
// version, every installed version of the chain including its active link.
func (a *app) remove(spec string) error {
	chainName, want := splitSpec(spec)
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	var targets []store.Installation
	var have []string
	for _, in := range installed {
		if in.Chain != chainName {
			continue
		}
		have = append(have, in.Version)
		if want == "" || strings.TrimPrefix(in.Version, "v") == strings.TrimPrefix(want, "v") {
			targets = append(targets, in)
		}
	}
	if len(have) == 0 {
		return fmt.Errorf("%s is not installed", chainName)
	}
	if len(targets) == 0 {
		return fmt.Errorf("%s %s is not installed (installed: %s)", chainName, want, orNone(have))
	}
	if want != "" && targets[0].Active {
		return fmt.Errorf("%s %s is the active version; switch first with: cosmup use %s@<version>\n  or remove every version with: cosmup remove %s",
			chainName, targets[0].Version, chainName, chainName)
	}
	return a.removeAll(targets)
}

// prune keeps one version per chain: the active one, or the most recently
// installed one if none is active (e.g. a testnet sharing a binary name).
func (a *app) prune() error {
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	keep := map[string]store.Installation{}
	for _, in := range installed {
		cur, ok := keep[in.Chain]
		switch {
		case !ok, in.Active:
			keep[in.Chain] = in
		case !cur.Active && a.store.InstalledAt(in).After(a.store.InstalledAt(cur)):
			keep[in.Chain] = in
		}
	}
	var targets []store.Installation
	for _, in := range installed {
		if keep[in.Chain] != in {
			targets = append(targets, in)
		}
	}
	if len(targets) == 0 {
		fmt.Fprintln(a.out, "Nothing to prune: each chain has only one version installed")
		return nil
	}
	return a.removeAll(targets)
}

func (a *app) removeAll(targets []store.Installation) error {
	var freed int64
	for _, in := range targets {
		n, err := a.store.Remove(in)
		if err != nil {
			return fmt.Errorf("removing %s %s: %w", in.Chain, in.Version, err)
		}
		freed += n
		note := ""
		if in.Active {
			note = fmt.Sprintf(" (was active; %s is no longer on cosmup's PATH)", in.Daemon)
		}
		fmt.Fprintf(a.out, "Removed %s %s, %s%s\n", in.Chain, in.Version, humanSize(n), note)
	}
	if len(targets) > 1 {
		fmt.Fprintf(a.out, "Freed %s\n", humanSize(freed))
	}
	return nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// prepareCosmovisor places a verified upgrade binary where cosmovisor (v1+)
// looks for it: $DAEMON_HOME/cosmovisor/upgrades/<plan name>/bin/<daemon>.
// With no upgrade name, it uses the upgrade scheduled on the network.
func (a *app) prepareCosmovisor(ctx context.Context, args []string, insecure bool) error {
	chainName := args[0]
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return err
	}
	home := os.Getenv("DAEMON_HOME")
	if home == "" {
		home = os.ExpandEnv(ch.NodeHome)
	}
	if home == "" {
		return errors.New("set DAEMON_HOME to your node's home directory")
	}
	cvDir := filepath.Join(home, "cosmovisor")
	if st, err := os.Stat(cvDir); err != nil || !st.IsDir() {
		return fmt.Errorf("no cosmovisor directory at %s\n  set DAEMON_HOME to the node home cosmovisor uses", cvDir)
	}

	plan := ""
	if len(args) > 1 {
		plan = args[1]
	} else {
		net, err := network.Query(ctx, a.http, restEndpoints(ch))
		if err != nil {
			return err
		}
		if net.Plan == nil {
			return fmt.Errorf("no upgrade is scheduled on %s; pass the upgrade name: cosmup cosmovisor %s <upgrade>", chainName, chainName)
		}
		plan = net.Plan.Name
		fmt.Fprintf(a.out, "Network has upgrade %q scheduled at height %s\n", plan, net.Plan.Height)
	}

	versions, err := a.reg.Versions(ctx, ch)
	if err != nil {
		return err
	}
	// Match against versions.json only: its entries carry the upgrade name.
	v, err := registry.Resolve(&registry.Chain{Name: chainName}, versions, plan)
	if err != nil {
		return fmt.Errorf("upgrade %q of %s is not in the chain registry", plan, chainName)
	}
	if v.Name == "" {
		return fmt.Errorf("the registry has no upgrade name for %s %s", chainName, v.ID())
	}

	dest := filepath.Join(cvDir, "upgrades", url.PathEscape(v.Name), "bin", ch.DaemonName)
	if _, err := os.Stat(dest); err == nil {
		fmt.Fprintf(a.out, "Upgrade %q (%s %s) is already prepared at %s\n", v.Name, chainName, v.ID(), dest)
		return nil
	}
	if err := a.fetch(ctx, chainName, v, ch.DaemonName, dest, insecure); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Prepared upgrade %q (%s %s) at %s\n", v.Name, chainName, v.ID(), dest)
	a.smokeTest(ctx, dest, ch.DaemonName)
	return nil
}

func (a *app) use(spec string) error {
	chainName, want := splitSpec(spec)
	if want == "" {
		return errors.New("usage: cosmup use <chain>@<version>")
	}
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	var have []string
	for _, in := range installed {
		if in.Chain != chainName {
			continue
		}
		have = append(have, in.Version)
		if strings.TrimPrefix(in.Version, "v") == strings.TrimPrefix(want, "v") {
			if err := a.store.Activate(chainName, in.Version, in.Daemon); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "Active: %s %s\n", chainName, in.Version)
			a.pathHint(in.Daemon)
			return nil
		}
	}
	return fmt.Errorf("%s %s is not installed (installed: %s); run: cosmup install %s", chainName, want, orNone(have), spec)
}

func (a *app) list(ctx context.Context, args []string) error {
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if len(installed) == 0 {
			fmt.Fprintln(a.out, "Nothing installed yet. See what you can install with: cosmup list cosmoshub")
			return nil
		}
		for _, in := range installed {
			fmt.Fprintf(a.out, "%s %-14s %-12s %s\n", mark(in.Active), in.Chain, in.Version, in.Daemon)
		}
		return nil
	}

	chainName := args[0]
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return err
	}
	versions, err := a.reg.Versions(ctx, ch)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		versions = []registry.Version{ch.Codebase}
	}
	state := map[string]string{}
	for _, in := range installed {
		if in.Chain == chainName {
			state[in.Version] = "installed"
			if in.Active {
				state[in.Version] = "active"
			}
		}
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	fmt.Fprintf(a.out, "%s (%s), newest first. * = active\n", chainName, ch.DaemonName)
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		if v.ID() == "" {
			continue
		}
		var notes []string
		if v.ID() == ch.Codebase.ID() {
			notes = append(notes, "recommended")
		}
		if s := state[v.ID()]; s == "installed" {
			notes = append(notes, s)
		}
		if _, ok := v.Binaries[platform]; !ok {
			notes = append(notes, "no "+platform+" binary")
		}
		fmt.Fprintf(a.out, "%s %-12s %s\n", mark(state[v.ID()] == "active"), v.ID(), strings.Join(notes, ", "))
	}
	return nil
}

func (a *app) check(ctx context.Context, chainName string) (bool, error) {
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return false, err
	}

	bin := filepath.Join(a.store.BinDir(), ch.DaemonName)
	if _, err := os.Stat(bin); err != nil {
		bin, _ = exec.LookPath(ch.DaemonName)
	}
	local := ""
	if bin != "" {
		if local, err = localVersion(ctx, bin); err != nil {
			return false, fmt.Errorf("%s version failed: %w", bin, err)
		}
	}

	net, err := network.Query(ctx, a.http, restEndpoints(ch))
	if err != nil {
		return false, err
	}

	if bin == "" {
		fmt.Fprintf(a.out, "Local:       %s not found\n", ch.DaemonName)
	} else {
		fmt.Fprintf(a.out, "Local:       %s (%s)\n", local, bin)
	}
	fmt.Fprintf(a.out, "Network:     %s (via %s)\n", net.AppVersion, net.Endpoint)
	fmt.Fprintf(a.out, "Registry:    %s recommended\n", ch.Codebase.ID())

	if net.Plan != nil {
		fmt.Fprintf(a.out, "Upgrade:     %q scheduled at height %s\n", net.Plan.Name, net.Plan.Height)
		if versions, err := a.reg.Versions(ctx, ch); err == nil {
			if v, err := registry.Resolve(ch, versions, net.Plan.Name); err == nil {
				fmt.Fprintf(a.out, "             prepare with: cosmup install %s@%s\n", chainName, v.ID())
			}
		}
	}

	if local != "" && strings.TrimPrefix(local, "v") == strings.TrimPrefix(net.AppVersion, "v") {
		fmt.Fprintln(a.out, "Status:      up to date")
		return true, nil
	}
	fmt.Fprintf(a.out, "Status:      does not match the network; run: cosmup install %s@%s\n", chainName, net.AppVersion)
	return false, nil
}

func restEndpoints(ch *registry.Chain) []string {
	var eps []string
	for _, e := range ch.APIs.REST {
		eps = append(eps, e.Address)
	}
	return eps
}

// localVersion runs "<bin> version". Older SDK versions print it to stderr.
func localVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", errors.New("printed nothing")
}

// pathHint warns when the daemon name on PATH is not cosmup's symlink.
func (a *app) pathHint(daemon string) {
	link := filepath.Join(a.store.BinDir(), daemon)
	found, err := exec.LookPath(daemon)
	if err == nil && found == link {
		return
	}
	if err == nil {
		fmt.Fprintf(a.out, "NOTE: %s on your PATH resolves to %s, not %s.\n", daemon, found, link)
	}
	fmt.Fprintf(a.out, "Put cosmup first on your PATH:  export PATH=\"%s:$PATH\"\n", a.store.BinDir())
}

func mark(active bool) string {
	if active {
		return "*"
	}
	return " "
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
