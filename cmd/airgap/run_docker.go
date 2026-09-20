package main

import (
	"context"
	"fmt"
	"strings"

	"airgapkit/internal/dl"
	"airgapkit/internal/fetch"
	"airgapkit/internal/oci"
)

func runDocker(ctx context.Context, o *options) (fetch.Stats, error) {
	var total fetch.Stats
	cfg := o.cfg.Docker
	if len(cfg.Images) == 0 {
		return total, nil
	}
	fmt.Printf("\n== docker ==\n")

	reg := oci.NewRegistry(o.client, cfg.Auths, cfg.Platform)
	var failed []string
	for _, spec := range cfg.Images {
		// A single unreachable image must not throw away the images that
		// come after it: report it and carry on.
		ref, err := oci.ParseRef(spec)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			failed = append(failed, spec)
			total.Failed++
			continue
		}
		img, ref, err := resolveWithSuggestions(ctx, o, reg, ref)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			failed = append(failed, spec)
			total.Failed++
			continue
		}

		header, err := reg.AuthHeader(ctx, ref)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			failed = append(failed, spec)
			total.Failed++
			continue
		}
		blobs := img.Blobs()
		var size int64
		jobs := make([]fetch.Job, 0, len(blobs))
		for _, b := range blobs {
			size += b.Size
			jobs = append(jobs, fetch.Job{
				Eco:     "docker",
				URL:     oci.BlobURL(ref, b.Digest),
				RelPath: oci.BlobPath(b.Digest),
				Expect:  dl.SHA256(b.Digest),
				Header:  header,
			})
		}
		fmt.Printf("  %s -> %s (%d blobs, %s)\n", ref.String(), cfg.Platform, len(blobs), fetch.HumanBytes(size))

		st := o.fetcher.Run(ctx, "docker", jobs)
		for _, err := range st.Errors {
			fmt.Printf("  error: %v\n", err)
		}
		total.Downloaded += st.Downloaded
		total.Skipped += st.Skipped
		total.Failed += st.Failed
		total.Bytes += st.Bytes

		if o.dryRun {
			continue
		}
		if st.Failed > 0 {
			fmt.Printf("  skipping manifest for %s because some blobs failed\n", ref)
			continue
		}
		rec, err := oci.WriteImage(o.store.Root, img, cfg.ConvertToDockerV2)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			failed = append(failed, spec)
			total.Failed++
			continue
		}
		for _, extra := range []string{ref.BundleDir(), oci.ImagesIndex} {
			if err := o.store.MirrorPath(extra); err != nil {
				return total, err
			}
		}
		fmt.Printf("    manifest %s (%s)\n", rec.Digest, rec.MediaType)
	}
	if len(failed) > 0 {
		return total, fmt.Errorf("%d image(s) could not be mirrored: %s", len(failed), strings.Join(failed, ", "))
	}
	return total, nil
}

// resolveWithSuggestions resolves a reference, and when it cannot, offers the
// alternatives the registry diagnosis produced instead of stopping. A wrong
// name is answered with the names that exist; a wrong tag with the tags that
// exist. Non-interactive runs get the explanation as an error, unchanged.
func resolveWithSuggestions(ctx context.Context, o *options, reg *oci.Registry, ref oci.Ref) (*oci.Image, oci.Ref, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		img, err := reg.Resolve(ctx, ref)
		if err == nil {
			return img, ref, nil
		}
		if !interactive(o) {
			return nil, ref, reg.Diagnose(ctx, ref, err)
		}
		lastErr = err

		problem := reg.Diagnose(ctx, ref, err)
		lastErr = problem
		fmt.Printf("\n  %s\n", problem.Message)
		if len(problem.Suggestions) == 0 {
			return nil, ref, problem
		}
		switch problem.Kind {
		case oci.ProblemTagMissing:
			fmt.Printf("  Tags that do exist:\n")
		default:
			fmt.Printf("  Try one of these instead:\n")
		}
		choice := askChoice("  Image", problem.Suggestions, 15)
		if choice == "" {
			return nil, ref, problem
		}
		next, perr := oci.ParseRef(choice)
		if perr != nil {
			fmt.Printf("  %v\n", perr)
			continue
		}
		ref = next
	}
	return nil, ref, lastErr
}
