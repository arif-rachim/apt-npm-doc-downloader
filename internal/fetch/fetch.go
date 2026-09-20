// Package fetch wires the HTTP client to the bundle store: it skips artifacts
// the mirror already has (that is what makes repeated runs incremental) and
// downloads the rest with a bounded worker pool.
package fetch

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"airgapkit/internal/dl"
	"airgapkit/internal/store"
)

// Job is a single artifact to place in the bundle.
type Job struct {
	Eco     string
	URL     string
	RelPath string
	Expect  dl.Expect
	Header  http.Header
}

// Stats summarises a Run.
type Stats struct {
	Downloaded int
	Skipped    int
	Failed     int
	Bytes      int64
	Errors     []error
}

// Fetcher downloads Jobs into a Store.
type Fetcher struct {
	Client      *dl.Client
	Store       *store.Store
	Concurrency int
	DryRun      bool
	Quiet       bool
}

// Run downloads every job, returning aggregate statistics. Individual
// failures do not abort the run; they are collected in Stats.Errors so one
// broken package cannot cost the user a multi-gigabyte download.
func (f *Fetcher) Run(ctx context.Context, eco string, jobs []Job) Stats {
	var (
		st   Stats
		mu   sync.Mutex
		done int64
	)
	total := len(jobs)
	if total == 0 {
		return st
	}
	workers := f.Concurrency
	if workers <= 0 {
		workers = 8
	}
	if workers > total {
		workers = total
	}

	// Pre-filter so the progress counter reflects real work.
	var todo []Job
	for _, j := range jobs {
		if j.Eco == "" {
			j.Eco = eco
		}
		if _, ok := f.Store.HasPath(j.RelPath); ok {
			st.Skipped++
			continue
		}
		if j.Expect.Algo == "sha256" && j.Expect.Value != "" {
			if e, ok := f.Store.HasSHA(j.Expect.Value); ok && e.RelPath == j.RelPath {
				st.Skipped++
				continue
			}
		}
		todo = append(todo, j)
	}
	if f.DryRun {
		for _, j := range todo {
			fmt.Printf("  would download %s -> %s\n", j.URL, j.RelPath)
		}
		st.Downloaded = len(todo)
		return st
	}

	ch := make(chan Job)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				size, sum, err := f.Client.Download(ctx, j.URL, f.Store.Abs(j.RelPath), j.Expect, j.Header)
				n := atomic.AddInt64(&done, 1)
				mu.Lock()
				if err != nil {
					st.Failed++
					st.Errors = append(st.Errors, fmt.Errorf("%s: %w", j.RelPath, err))
					fmt.Fprintf(os.Stderr, "  [%d/%d] FAIL %s: %v\n", n, len(todo), j.RelPath, err)
					mu.Unlock()
					continue
				}
				rerr := f.Store.Record(store.Entry{
					Eco: j.Eco, SHA256: sum, Size: size, RelPath: j.RelPath, Source: j.URL,
				})
				if rerr != nil {
					st.Failed++
					st.Errors = append(st.Errors, fmt.Errorf("record %s: %w", j.RelPath, rerr))
					mu.Unlock()
					continue
				}
				st.Downloaded++
				st.Bytes += size
				mu.Unlock()
				if !f.Quiet {
					fmt.Printf("  [%d/%d] %s (%s)\n", n, len(todo), j.RelPath, HumanBytes(size))
				}
			}
		}()
	}
	for _, j := range todo {
		select {
		case <-ctx.Done():
			close(ch)
			wg.Wait()
			st.Errors = append(st.Errors, ctx.Err())
			return st
		case ch <- j:
		}
	}
	close(ch)
	wg.Wait()
	return st
}

// HumanBytes renders a byte count for progress output.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
