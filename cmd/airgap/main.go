// Command airgap builds an offline mirror of apt, npm, PyPI and container
// image dependencies that can be carried into an air-gapped environment and
// pushed to Nexus with scripts/push.sh.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"airgapkit"
	"airgapkit/internal/config"
	"airgapkit/internal/dl"
	"airgapkit/internal/fetch"
	"airgapkit/internal/store"
)

// Set at build time by the Makefile.
var (
	version   = "dev"
	buildDate = "unknown"
)

const usage = `airgap - offline dependency mirror builder (Ubuntu 24.04 / amd64)

Usage:
  airgap fetch  [apt|npm|pypi|docker|all] [flags]   download into the bundle
  airgap plan   [apt|npm|pypi|docker|all] [flags]   show what would be downloaded
  airgap index  [flags]                             regenerate SHA256SUMS and local repo metadata
  airgap verify [flags]                             re-hash the bundle and report drift
  airgap scripts [flags]                            (re)write the air-gap scripts into the bundle
  airgap repos                                      list the built-in third-party repositories
  airgap tags IMAGE                                 list the tags a registry publishes for an image
  airgap version                                    print the version

Common flags:
  -config FILE     configuration file (default: airgap.json when present)
  -out DIR         bundle / permanent mirror directory (default: ./bundle)
  -delta-out DIR   also copy newly downloaded artifacts here (for USB transfer)
  -j N             parallel downloads (default 8)
  -yes             never ask questions (for scripted runs)
  -v               verbose HTTP logging

apt flags:
  -pkg NAME        package to include, repeatable or comma separated
  -repo KEY        add a repository from the built-in catalogue (see
                   "airgap repos"), repeatable or comma separated
  -sources FILE    sources.list or deb822 .sources file, repeatable
                   (the Ubuntu 24.04 archives are always included unless
                   "default_sources": false is set in the config)
  -recommends      also follow Recommends
  -gen-repo        write Packages/Release so the mirror works over file://
  -keyring FILE    verify Release signatures with gpgv using this keyring

npm flags:
  -lock FILE       package-lock.json, repeatable
  -manifest FILE   package.json to resolve with npm, repeatable
  -pkg SPEC        package spec such as typescript@5.6.2, repeatable
  -no-dev          skip devDependencies
  -all-platforms   keep optional packages for other OS/CPU (dropped by default:
                   the target is always linux/amd64)

pypi flags:
  -req FILE        requirements.txt, repeatable
  -uv-lock FILE    uv.lock, repeatable
  -project FILE    pyproject.toml to resolve with uv, repeatable
  -pkg SPEC        package spec such as requests==2.32.3, repeatable
  -python VER      target interpreter (default 3.12)
  -glibc VER       maximum glibc baseline (default 2.39)
  -no-sdist        fail instead of falling back to a source distribution
  -gen-index       write a PEP 503 index so pip can install from file://

docker flags:
  -image REF       image reference, repeatable
  -platform P      platform to mirror (default linux/amd64)
  -convert-v2      rewrite OCI manifests to docker schema2 (Nexus < 3.71)

Examples:
  airgap fetch apt -pkg nginx,curl -sources ubuntu.sources -gen-repo
  airgap fetch pypi -req requirements.txt -gen-index
  airgap fetch docker -image nginx:1.27 -image ghcr.io/org/app:v1
  airgap fetch all -config airgap.json -delta-out ./bundle-delta
`

// multiFlag collects a repeatable, optionally comma separated flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*m = append(*m, part)
		}
	}
	return nil
}

type options struct {
	cfg       *config.Config
	store     *store.Store
	fetcher   *fetch.Fetcher
	client    *dl.Client
	dryRun    bool
	genRepo   bool
	genIndex  bool
	assumeYes bool
	// cfgPath is where newly added repositories are saved.
	cfgPath string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	case "version", "-version", "--version":
		fmt.Printf("airgap %s (built %s, %s %s/%s)\n", version, buildDate, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		fmt.Printf("bundled air-gap scripts: %s\n", airgapkit.ScriptsDigest())
		return
	case "repos":
		printRepoCatalog()
		return
	case "tags":
	case "fetch", "plan", "index", "verify", "scripts":
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	var ecos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		ecos = append(ecos, args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Print(usage) }
	var (
		cfgPath  = fs.String("config", "", "configuration file")
		out      = fs.String("out", "", "bundle directory")
		deltaOut = fs.String("delta-out", "", "delta directory")
		jobs     = fs.Int("j", 0, "parallel downloads")
		verbose  = fs.Bool("v", false, "verbose")
		aptPkgs  multiFlag
		aptSrcs  multiFlag
		aptRepos multiFlag
		npmLocks multiFlag
		npmMans  multiFlag
		npmPkgs  multiFlag
		pyReqs   multiFlag
		pyLocks  multiFlag
		pyProjs  multiFlag
		pyPkgs   multiFlag
		images   multiFlag
	)
	fs.Var(&aptPkgs, "pkg", "package (apt/npm/pypi depending on the subject)")
	fs.Var(&aptSrcs, "sources", "apt sources file")
	fs.Var(&aptRepos, "repo", "repository from the built-in catalogue")
	recommends := fs.Bool("recommends", false, "follow Recommends")
	genRepo := fs.Bool("gen-repo", false, "generate local apt metadata")
	keyring := fs.String("keyring", "", "gpgv keyring")
	fs.Var(&npmLocks, "lock", "package-lock.json")
	fs.Var(&npmMans, "manifest", "package.json")
	noDev := fs.Bool("no-dev", false, "skip npm devDependencies")
	allPlatforms := fs.Bool("all-platforms", false, "keep npm packages for other platforms")
	fs.Var(&pyReqs, "req", "requirements.txt")
	fs.Var(&pyLocks, "uv-lock", "uv.lock")
	fs.Var(&pyProjs, "project", "pyproject.toml")
	python := fs.String("python", "", "target python version")
	glibc := fs.String("glibc", "", "maximum glibc baseline")
	noSdist := fs.Bool("no-sdist", false, "do not fall back to sdists")
	genIndex := fs.Bool("gen-index", false, "generate a local PEP 503 index")
	fs.Var(&images, "image", "container image reference")
	platform := fs.String("platform", "", "container platform")
	convertV2 := fs.Bool("convert-v2", false, "rewrite OCI manifests to docker schema2")
	assumeYes := fs.Bool("yes", false, "never ask questions")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	path := *cfgPath
	if path == "" {
		if _, err := os.Stat("airgap.json"); err == nil {
			path = "airgap.json"
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		fatal(err)
	}

	// The shared -pkg flag belongs to whichever ecosystem is being fetched.
	for _, eco := range ecos {
		switch eco {
		case "npm":
			npmPkgs = append(npmPkgs, aptPkgs...)
			aptPkgs = nil
		case "pypi":
			pyPkgs = append(pyPkgs, aptPkgs...)
			aptPkgs = nil
		}
	}

	if *out != "" {
		cfg.Out = *out
	}
	if *deltaOut != "" {
		cfg.DeltaOut = *deltaOut
	}
	if *jobs > 0 {
		cfg.Concurrency = *jobs
	}
	cfg.APT.Packages = append(cfg.APT.Packages, aptPkgs...)
	cfg.APT.SourcesFiles = append(cfg.APT.SourcesFiles, aptSrcs...)
	cfg.APT.Repos = append(cfg.APT.Repos, aptRepos...)
	if *recommends {
		cfg.APT.IncludeRecommends = true
	}
	if *keyring != "" {
		cfg.APT.GPGVKeyring = *keyring
	}
	cfg.NPM.Lockfiles = append(cfg.NPM.Lockfiles, npmLocks...)
	cfg.NPM.Manifests = append(cfg.NPM.Manifests, npmMans...)
	cfg.NPM.Packages = append(cfg.NPM.Packages, npmPkgs...)
	if *noDev {
		f := false
		cfg.NPM.IncludeDev = &f
	}
	if *allPlatforms {
		cfg.NPM.AllPlatforms = true
	}
	cfg.PyPI.Requirements = append(cfg.PyPI.Requirements, pyReqs...)
	cfg.PyPI.UVLocks = append(cfg.PyPI.UVLocks, pyLocks...)
	cfg.PyPI.Projects = append(cfg.PyPI.Projects, pyProjs...)
	cfg.PyPI.Packages = append(cfg.PyPI.Packages, pyPkgs...)
	if *python != "" {
		cfg.PyPI.Python = *python
	}
	if *glibc != "" {
		cfg.PyPI.GlibcMax = *glibc
	}
	if *noSdist {
		f := false
		cfg.PyPI.SdistFallback = &f
	}
	cfg.Docker.Images = append(cfg.Docker.Images, images...)
	if *platform != "" {
		cfg.Docker.Platform = *platform
	}
	if *convertV2 {
		cfg.Docker.ConvertToDockerV2 = true
	}

	airgapkit.Version, airgapkit.BuildDate = version, buildDate

	client := dl.New()
	client.Verbose = *verbose

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// "tags" only reads a registry, so it must not create a bundle.
	if cmd == "tags" {
		if len(ecos) != 1 {
			fatal(fmt.Errorf("usage: airgap tags IMAGE (e.g. airgap tags postgres)"))
		}
		if err := runTags(ctx, cfg, client, ecos[0]); err != nil {
			fatal(err)
		}
		return
	}

	st, err := store.Open(cfg.Out, cfg.DeltaOut)
	if err != nil {
		fatal(err)
	}
	defer st.Close()
	savePath := path
	if savePath == "" {
		savePath = "airgap.json"
	}
	opts := &options{
		cfg:       cfg,
		store:     st,
		client:    client,
		dryRun:    cmd == "plan",
		genRepo:   *genRepo || cfg.APT.GenRepo,
		genIndex:  *genIndex || cfg.PyPI.GenIndex,
		assumeYes: *assumeYes,
		cfgPath:   savePath,
	}
	opts.fetcher = &fetch.Fetcher{Client: client, Store: st, Concurrency: cfg.Concurrency, DryRun: opts.dryRun}

	switch cmd {
	case "scripts":
		changed, err := airgapkit.WriteScriptsReport(cfg.Out)
		if err != nil {
			fatal(err)
		}
		if err := airgapkit.WriteQuickstart(cfg.Out); err != nil {
			fatal(err)
		}
		state := "already up to date"
		if changed {
			state = "updated"
		}
		fmt.Printf("%s/scripts: %s (content %s)\n", cfg.Out, state, airgapkit.ScriptsDigest())
		return
	case "index":
		if err := runIndex(opts); err != nil {
			fatal(err)
		}
		return
	case "verify":
		if err := runVerify(opts); err != nil {
			fatal(err)
		}
		return
	}

	if len(ecos) == 0 {
		ecos = []string{"all"}
	}
	selected := map[string]bool{}
	for _, e := range ecos {
		if e == "all" {
			selected["apt"], selected["npm"], selected["pypi"], selected["docker"] = true, true, true, true
			continue
		}
		selected[e] = true
	}

	var failures int
	total := fetch.Stats{}
	for _, eco := range []string{"apt", "npm", "pypi", "docker"} {
		if !selected[eco] {
			continue
		}
		var (
			st  fetch.Stats
			err error
		)
		switch eco {
		case "apt":
			st, err = runAPT(ctx, opts)
		case "npm":
			st, err = runNPM(ctx, opts)
		case "pypi":
			st, err = runPyPI(ctx, opts)
		case "docker":
			st, err = runDocker(ctx, opts)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", eco, err)
			failures++
		}
		total.Downloaded += st.Downloaded
		total.Skipped += st.Skipped
		total.Failed += st.Failed
		total.Bytes += st.Bytes
		failures += st.Failed
	}

	if !opts.dryRun {
		// The bundle travels alone, so it carries its own push tooling.
		if err := writeBundleScripts(opts); err != nil {
			fmt.Fprintf(os.Stderr, "write scripts: %v\n", err)
		}
		if err := opts.store.WriteSums(); err != nil {
			fmt.Fprintf(os.Stderr, "write SHA256SUMS: %v\n", err)
		}
		if err := opts.store.WriteDeltaManifest(); err != nil {
			fmt.Fprintf(os.Stderr, "write delta manifest: %v\n", err)
		}
	}

	if opts.dryRun {
		fmt.Printf("\nTotal: %d to download, %d already present\n", total.Downloaded, total.Skipped)
		fmt.Printf("Bundle: %s (nothing was written: this was a plan)\n", cfg.Out)
	} else {
		fmt.Printf("\nTotal: %d downloaded (%s), %d already present, %d failed\n",
			total.Downloaded, fetch.HumanBytes(total.Bytes), total.Skipped, total.Failed)
		fmt.Printf("Bundle: %s (push with %s/scripts/push.sh)\n", cfg.Out, cfg.Out)
	}
	if cfg.DeltaOut != "" {
		fmt.Printf("Delta:  %s\n", cfg.DeltaOut)
	}
	if failures > 0 {
		os.Exit(1)
	}
}

// writeBundleScripts copies the embedded push/merge/verify scripts and the
// quickstart note into the bundle, and into the delta directory when one is
// configured, so whichever directory is carried across the air gap is usable
// on its own.
func writeBundleScripts(o *options) error {
	for _, dir := range []string{o.store.Root, o.store.Delta} {
		if dir == "" {
			continue
		}
		if err := airgapkit.WriteScripts(dir); err != nil {
			return err
		}
		if err := airgapkit.WriteQuickstart(dir); err != nil {
			return err
		}
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
