package main

import (
	"airgapkit/internal/textio"
	"context"
	"fmt"

	"airgapkit/internal/dl"
	"airgapkit/internal/fetch"
	"airgapkit/internal/npm"
)

func runNPM(ctx context.Context, o *options) (fetch.Stats, error) {
	var st fetch.Stats
	cfg := o.cfg.NPM
	if len(cfg.Lockfiles) == 0 && len(cfg.Manifests) == 0 && len(cfg.Packages) == 0 {
		return st, nil
	}
	fmt.Printf("\n== npm ==\n")

	var entries []npm.Entry
	add := func(data []byte, origin string) error {
		got, err := npm.ParseLock(data)
		if err != nil {
			return fmt.Errorf("%s: %w", origin, err)
		}
		fmt.Printf("  %s: %d tarballs\n", origin, len(got))
		entries = append(entries, got...)
		return nil
	}

	for _, lock := range cfg.Lockfiles {
		data, err := textio.ReadFile(lock)
		if err != nil {
			return st, err
		}
		if err := add(data, lock); err != nil {
			return st, err
		}
	}
	for _, manifest := range cfg.Manifests {
		fmt.Printf("  resolving %s with npm...\n", manifest)
		data, err := npm.GenerateLockFromManifest(ctx, manifest, cfg.Registry)
		if err != nil {
			return st, err
		}
		if err := add(data, manifest); err != nil {
			return st, err
		}
	}
	if len(cfg.Packages) > 0 {
		fmt.Printf("  resolving %d package spec(s) with npm...\n", len(cfg.Packages))
		data, err := npm.GenerateLockFromSpecs(ctx, cfg.Packages, cfg.Registry)
		if err != nil {
			return st, err
		}
		if err := add(data, "package specs"); err != nil {
			return st, err
		}
	}

	seen := map[string]bool{}
	var jobs []fetch.Job
	var skippedPlatform, skippedDev int
	for _, e := range entries {
		if !npm.Keep(e, cfg.IncludeDevDeps(), cfg.LinuxOnly()) {
			if e.Dev {
				skippedDev++
			} else {
				skippedPlatform++
			}
			continue
		}
		path := e.TarballPath()
		if seen[path] {
			continue
		}
		seen[path] = true
		expect := dl.NoExpect
		if e.Integrity != "" {
			exp, err := dl.ParseSRI(e.Integrity)
			if err != nil {
				fmt.Printf("  warning: %s: %v\n", e.Name, err)
			} else {
				expect = exp
			}
		}
		jobs = append(jobs, fetch.Job{Eco: "npm", URL: e.Resolved, RelPath: path, Expect: expect})
	}
	if skippedDev > 0 {
		fmt.Printf("  skipped %d dev dependencies\n", skippedDev)
	}
	if skippedPlatform > 0 {
		fmt.Printf("  skipped %d platform specific packages\n", skippedPlatform)
	}
	fmt.Printf("  %d unique tarballs\n", len(jobs))

	st = o.fetcher.Run(ctx, "npm", jobs)
	for _, err := range st.Errors {
		fmt.Printf("  error: %v\n", err)
	}
	return st, nil
}
