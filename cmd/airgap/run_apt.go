package main

import (
	"context"
	"fmt"
	"strings"

	"airgapkit/internal/apt"
	"airgapkit/internal/config"
	"airgapkit/internal/dl"
	"airgapkit/internal/fetch"
)

func runAPT(ctx context.Context, o *options) (fetch.Stats, error) {
	var st fetch.Stats
	cfg := o.cfg.APT
	if len(cfg.Packages) == 0 {
		return st, nil
	}
	sources, err := o.cfg.AptSources()
	if err != nil {
		return st, err
	}
	if len(sources) == 0 {
		return st, fmt.Errorf("no apt sources configured; add them to airgap.json or pass -sources")
	}

	fmt.Printf("\n== apt ==\n")
	loader := &apt.Loader{Client: o.client, Keyring: cfg.GPGVKeyring, Verbose: o.client.Verbose}
	index := apt.NewIndex()
	load := func(src config.AptSource) error {
		if len(src.Arch) == 0 {
			src.Arch = cfg.Arch
		}
		pkgs, err := loader.Load(ctx, src, src.Arch)
		if err != nil {
			return err
		}
		index.Add(pkgs)
		fmt.Printf("  source %s: %d packages\n", src.Name, len(pkgs))
		return nil
	}
	for _, src := range sources {
		if err := load(src); err != nil {
			return st, err
		}
	}

	exclude := map[string]bool{}
	for _, e := range cfg.Exclude {
		exclude[e] = true
	}

	// Resolve, and when something cannot be found offer to add another
	// repository right here instead of failing with a dead end.
	var (
		res   *apt.Result
		added []config.AptSource
	)
	for {
		res, err = (&apt.Resolver{
			Index:             index,
			IncludeRecommends: cfg.IncludeRecommends,
			Exclude:           exclude,
			Prefer:            cfg.Prefer,
		}).Closure(cfg.Packages)

		unresolved := err != nil || (res != nil && len(res.Missing) > 0)
		if !unresolved {
			break
		}
		if !interactive(o) {
			if err != nil {
				return st, fmt.Errorf("%w\n       add the repository that provides it to airgap.json, "+
					"pass -sources FILE, or run without -yes to be asked interactively", err)
			}
			break // missing transitive dependencies are reported as warnings
		}
		if err != nil {
			fmt.Printf("\n  %v\n", err)
			// A missing package is far more often a typo than a missing
			// repository, and the whole index is already in memory, so
			// offer the near misses before asking about repositories.
			if missing := missingRootName(err, cfg.Packages); missing != "" {
				if alts := index.Suggest(missing, 10); len(alts) > 0 {
					fmt.Printf("  Packages with a similar name:\n")
					if choice := askChoice("  Package", alts, 10); choice != "" {
						for i, p := range cfg.Packages {
							if rootName(p) == missing {
								cfg.Packages[i] = choice
							}
						}
						continue
					}
				}
			}
		} else {
			fmt.Printf("\n  %d dependencies are not in any configured repository: %s\n",
				len(res.Missing), strings.Join(res.Missing, ", "))
		}
		if !confirm("  Add another repository?", true) {
			if err != nil {
				return st, err
			}
			break
		}
		fmt.Println("  Give a catalogue key (see \"airgap repos\"), a PPA, or a full deb line:")
		fmt.Println("    docker | pgdg | nginx | kubernetes | ppa:owner/name |")
		fmt.Println("    deb [arch=amd64] https://host/repo noble main")
		line := askLine("  Repository")
		if line == "" {
			if err != nil {
				return st, err
			}
			break
		}
		src, perr := config.ParseSourceLine(line)
		if perr != nil {
			fmt.Printf("  %v\n", perr)
			continue
		}
		if lerr := load(src); lerr != nil {
			fmt.Printf("  cannot read that repository: %v\n", lerr)
			continue
		}
		added = append(added, src)
	}

	var totalSize int64
	jobs := make([]fetch.Job, 0, len(res.Packages))
	for _, p := range res.Packages {
		totalSize += p.Size
		jobs = append(jobs, fetch.Job{
			Eco:     "apt",
			URL:     p.URL(),
			RelPath: p.PoolPath(),
			Expect:  dl.SHA256(p.SHA256),
		})
	}
	fmt.Printf("  closure: %d packages, %s\n", len(res.Packages), fetch.HumanBytes(totalSize))
	for _, w := range res.Warnings {
		fmt.Printf("  note: %s\n", w)
	}
	if len(res.Missing) > 0 {
		fmt.Printf("  warning: %d unresolved dependencies: %s\n", len(res.Missing), strings.Join(res.Missing, ", "))
	}

	st = o.fetcher.Run(ctx, "apt", jobs)
	for _, err := range st.Errors {
		fmt.Printf("  error: %v\n", err)
	}

	// Only offer to remember a repository that actually produced a download.
	if len(added) > 0 && st.Failed == 0 && !o.dryRun && interactive(o) {
		names := make([]string, 0, len(added))
		for _, s := range added {
			names = append(names, s.URI)
		}
		if confirm(fmt.Sprintf("\n  Save %s to %s for next time?", strings.Join(names, ", "), o.cfgPath), true) {
			if err := config.AddSourcesToFile(o.cfgPath, added); err != nil {
				fmt.Printf("  could not save: %v\n", err)
			} else {
				fmt.Printf("  saved to %s\n", o.cfgPath)
			}
		}
	}

	if o.genRepo && !o.dryRun {
		arch := "amd64"
		if len(cfg.Arch) > 0 {
			arch = cfg.Arch[0]
		}
		if err := generateAptRepo(o, arch); err != nil {
			return st, err
		}
	}
	return st, nil
}

// missingRootName pulls the package name out of the resolver error, when the
// failure was one of the packages the user asked for.
func missingRootName(err error, roots []string) string {
	msg := err.Error()
	for _, r := range roots {
		name := rootName(r)
		if strings.Contains(msg, "\""+name+"\"") {
			return name
		}
	}
	return ""
}

// rootName strips any version constraint from a requested package.
func rootName(spec string) string {
	for _, op := range []string{">=", "<=", ">>", "<<", "="} {
		if i := strings.Index(spec, op); i > 0 {
			return strings.TrimSpace(spec[:i])
		}
	}
	return strings.TrimSpace(spec)
}

// generateAptRepo rebuilds the local Packages/Release covering everything in
// the mirror, not just this run, so the file:// repository stays complete.
func generateAptRepo(o *options, arch string) error {
	var entries []apt.RepoEntry
	for _, e := range o.store.All() {
		if e.Eco != "apt" || !strings.HasSuffix(e.RelPath, ".deb") {
			continue
		}
		entries = append(entries, apt.RepoEntry{RelPath: e.RelPath, SHA256: e.SHA256, Size: e.Size})
	}
	if len(entries) == 0 {
		return nil
	}
	if err := apt.GenerateRepo(o.store.Root, arch, entries); err != nil {
		return err
	}
	if err := o.store.MirrorPath("apt/dists"); err != nil {
		return err
	}
	fmt.Printf("  local repo metadata: %s/apt/dists/stable (deb [trusted=yes] file://%s/apt stable main)\n", o.store.Root, o.store.Root)
	return nil
}
