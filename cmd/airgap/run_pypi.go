package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"airgapkit/internal/dl"
	"airgapkit/internal/fetch"
	"airgapkit/internal/pypi"
)

// pyWant is one pinned distribution to mirror.
type pyWant struct {
	Name    string
	Version string
	// URL and SHA256 are set when a lockfile already told us the exact file.
	URL      string
	SHA256   string
	Filename string
	Size     int64
}

func runPyPI(ctx context.Context, o *options) (fetch.Stats, error) {
	var st fetch.Stats
	cfg := o.cfg.PyPI
	if len(cfg.Requirements) == 0 && len(cfg.UVLocks) == 0 && len(cfg.Projects) == 0 && len(cfg.Packages) == 0 {
		return st, nil
	}
	fmt.Printf("\n== pypi ==\n")

	target, err := pypi.ParseTarget(cfg.Python, cfg.GlibcMax, "x86_64")
	if err != nil {
		return st, err
	}
	env := pypi.DefaultEnvironment(target)
	fmt.Printf("  target: cp%d%d, glibc <= %d.%d, %s\n", target.PyMajor, target.PyMinor, target.GlibcMajor, target.GlibcMinor, target.Arch)

	var wants []pyWant
	var sdistWarnings []string

	// Lockfiles first: they already name the exact artifacts, but because
	// uv.lock is universal we still have to choose the right wheel.
	locks := append([]string{}, cfg.UVLocks...)
	for _, proj := range cfg.Projects {
		fmt.Printf("  resolving %s with uv...\n", proj)
		pkgs, err := pypi.LockProject(ctx, proj, cfg.Python)
		if err != nil {
			return st, err
		}
		got, warns := wantsFromLock(pkgs, target, cfg.AllowSdist())
		wants = append(wants, got...)
		sdistWarnings = append(sdistWarnings, warns...)
		fmt.Printf("    %s: %d distributions\n", proj, len(got))
	}
	for _, lock := range locks {
		pkgs, err := pypi.ParseUVLock(lock)
		if err != nil {
			return st, err
		}
		got, warns := wantsFromLock(pkgs, target, cfg.AllowSdist())
		wants = append(wants, got...)
		sdistWarnings = append(sdistWarnings, warns...)
		fmt.Printf("  %s: %d distributions\n", lock, len(got))
	}

	// Requirements and bare specs go through uv so the closure is complete.
	reqFiles := append([]string{}, cfg.Requirements...)
	if len(cfg.Packages) > 0 {
		tmp, err := os.CreateTemp("", "airgap-req-*.txt")
		if err != nil {
			return st, err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(strings.Join(cfg.Packages, "\n") + "\n"); err != nil {
			tmp.Close()
			return st, err
		}
		tmp.Close()
		reqFiles = append(reqFiles, tmp.Name())
	}
	for _, rf := range reqFiles {
		origin := rf
		if len(cfg.Packages) > 0 && rf == reqFiles[len(reqFiles)-1] && len(cfg.Requirements) < len(reqFiles) {
			origin = "-pkg " + strings.Join(cfg.Packages, ",")
		}
		reqs, err := resolveRequirements(ctx, rf, cfg.Python, env)
		if err != nil {
			return st, fmt.Errorf("%s: %w", origin, err)
		}
		for _, r := range reqs {
			wants = append(wants, pyWant{Name: r.Name, Version: r.Version})
		}
		fmt.Printf("  %s: %d pinned requirements\n", origin, len(reqs))
	}

	// Anything still missing a concrete file is looked up on the index.
	client := &pypi.Client{HTTP: o.client, Index: cfg.Index}
	resolved, warns, err := resolveFromIndex(ctx, client, target, wants, cfg.AllowSdist(), o.cfg.Concurrency)
	if err != nil {
		return st, err
	}
	sdistWarnings = append(sdistWarnings, warns...)

	seen := map[string]bool{}
	var jobs []fetch.Job
	var totalSize int64
	for _, w := range resolved {
		path := pypiBundlePath(w)
		if seen[path] {
			continue
		}
		seen[path] = true
		totalSize += w.Size
		jobs = append(jobs, fetch.Job{Eco: "pypi", URL: w.URL, RelPath: path, Expect: dl.SHA256(w.SHA256)})
	}
	if totalSize > 0 {
		fmt.Printf("  %d unique distributions, %s\n", len(jobs), fetch.HumanBytes(totalSize))
	} else {
		fmt.Printf("  %d unique distributions\n", len(jobs))
	}
	for _, w := range sdistWarnings {
		fmt.Printf("  warning: %s\n", w)
	}

	st = o.fetcher.Run(ctx, "pypi", jobs)
	for _, err := range st.Errors {
		fmt.Printf("  error: %v\n", err)
	}

	if o.genIndex && !o.dryRun {
		if err := generateSimpleIndex(o); err != nil {
			return st, err
		}
	}
	return st, nil
}

func pypiBundlePath(w pyWant) string {
	return "pypi/packages/" + pypi.Normalize(w.Name) + "/" + w.Filename
}

// wantsFromLock turns uv.lock packages into concrete artifacts, applying the
// wheel compatibility rules because the lockfile is platform independent.
func wantsFromLock(pkgs []pypi.LockPackage, target pypi.Target, allowSdist bool) ([]pyWant, []string) {
	var out []pyWant
	var warnings []string
	for _, p := range pkgs {
		if !p.Downloadable() {
			continue
		}
		best := -1
		var chosen pypi.LockArtifact
		for _, w := range p.Wheels {
			wheel, ok := pypi.ParseWheelName(w.Filename())
			if !ok {
				continue
			}
			s, ok := target.Score(wheel)
			if !ok {
				continue
			}
			if s > best {
				best, chosen = s, w
			}
		}
		if best >= 0 {
			out = append(out, pyWant{Name: p.Name, Version: p.Version, URL: chosen.URL, SHA256: chosen.SHA256, Filename: chosen.Filename()})
			continue
		}
		if p.Sdist.URL != "" && allowSdist {
			out = append(out, pyWant{Name: p.Name, Version: p.Version, URL: p.Sdist.URL, SHA256: p.Sdist.SHA256, Filename: p.Sdist.Filename()})
			warnings = append(warnings, fmt.Sprintf("%s %s: no compatible wheel, using sdist (needs a build toolchain offline)", p.Name, p.Version))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s %s: no compatible wheel and no sdist, skipped", p.Name, p.Version))
	}
	return out, warnings
}

// resolveRequirements pins a requirements file. uv is used whenever it is
// available so transitive dependencies are included; a fully pinned file is
// accepted as-is when uv is missing.
func resolveRequirements(ctx context.Context, path, python string, env pypi.Environment) ([]pypi.Requirement, error) {
	compiled, err := pypi.CompileRequirements(ctx, path, python)
	if err == nil {
		tmp, terr := os.CreateTemp("", "airgap-pinned-*.txt")
		if terr != nil {
			return nil, terr
		}
		defer os.Remove(tmp.Name())
		if _, werr := tmp.Write(compiled); werr != nil {
			tmp.Close()
			return nil, werr
		}
		tmp.Close()
		return pypi.ParseRequirements(tmp.Name(), env)
	}

	reqs, perr := pypi.ParseRequirements(path, env)
	if perr != nil {
		return nil, perr
	}
	var unpinned []string
	for _, r := range reqs {
		if !r.Pinned() {
			unpinned = append(unpinned, r.Name)
		}
	}
	if len(unpinned) > 0 {
		return nil, fmt.Errorf("%s has unpinned requirements (%s) and uv could not resolve them: %w",
			path, strings.Join(unpinned, ", "), err)
	}
	fmt.Printf("  note: using %s as-is (uv unavailable); transitive dependencies must already be listed\n", path)
	return reqs, nil
}

// resolveFromIndex fills in URL and hash for wants that came from a
// requirements file, querying the Simple API in parallel.
func resolveFromIndex(ctx context.Context, client *pypi.Client, target pypi.Target, wants []pyWant, allowSdist bool, workers int) ([]pyWant, []string, error) {
	type result struct {
		want pyWant
		warn string
		err  error
	}
	if workers <= 0 {
		workers = 8
	}
	var (
		mu       sync.Mutex
		out      []pyWant
		warnings []string
		firstErr error
		wg       sync.WaitGroup
	)
	ch := make(chan pyWant)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range ch {
				r := result{want: w}
				if w.URL != "" {
					mu.Lock()
					out = append(out, w)
					mu.Unlock()
					continue
				}
				files, err := client.Files(ctx, w.Name)
				if err != nil {
					r.err = err
				} else {
					version := w.Version
					if version == "" {
						version = pypi.LatestVersion(files)
					}
					f, isSdist, serr := pypi.Select(target, files, version, allowSdist)
					if serr != nil {
						r.err = fmt.Errorf("%s %s: %w", w.Name, version, serr)
					} else {
						r.want = pyWant{Name: w.Name, Version: version, URL: f.URL, SHA256: f.SHA256, Filename: f.Filename, Size: f.Size}
						if isSdist {
							r.warn = fmt.Sprintf("%s %s: no compatible wheel, using sdist (needs a build toolchain offline)", w.Name, version)
						}
					}
				}
				mu.Lock()
				if r.err != nil {
					warnings = append(warnings, r.err.Error())
					if firstErr == nil {
						firstErr = r.err
					}
				} else {
					out = append(out, r.want)
					if r.warn != "" {
						warnings = append(warnings, r.warn)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, w := range wants {
		select {
		case <-ctx.Done():
			close(ch)
			wg.Wait()
			return nil, warnings, ctx.Err()
		case ch <- w:
		}
	}
	close(ch)
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}

func generateSimpleIndex(o *options) error {
	var files []pypi.IndexFile
	for _, e := range o.store.All() {
		if e.Eco != "pypi" {
			continue
		}
		parts := strings.Split(e.RelPath, "/")
		if len(parts) < 3 {
			continue
		}
		files = append(files, pypi.IndexFile{
			Project:  parts[len(parts)-2],
			Filename: filepath.Base(e.RelPath),
			SHA256:   e.SHA256,
		})
	}
	if len(files) == 0 {
		return nil
	}
	if err := pypi.GenerateSimpleIndex(o.store.Root, files); err != nil {
		return err
	}
	if err := o.store.MirrorPath("pypi/simple"); err != nil {
		return err
	}
	fmt.Printf("  local index: pip install --index-url file://%s/pypi/simple <pkg>\n", o.store.Root)
	return nil
}
