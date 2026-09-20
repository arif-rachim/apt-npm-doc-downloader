package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"airgapkit/internal/config"
	"airgapkit/internal/dl"
	"airgapkit/internal/oci"
)

// runTags lists the tags a registry publishes for an image, which is what you
// need after a pull fails because the tag was wrong.
func runTags(ctx context.Context, cfg *config.Config, client *dl.Client, spec string) error {
	ref, err := oci.ParseRef(spec)
	if err != nil {
		return err
	}
	reg := oci.NewRegistry(client, cfg.Docker.Auths, cfg.Docker.Platform)
	tags, err := reg.Tags(ctx, ref)
	// A name that does not exist should not be a dead end here either: show
	// the alternatives and list the tags of whichever one is picked.
	for attempt := 0; err != nil && attempt < 3; attempt++ {
		var problem *oci.PullProblem
		if !errors.As(err, &problem) || len(problem.Suggestions) == 0 || !interactiveStdin() {
			return err
		}
		fmt.Printf("%s\n", problem.Message)
		fmt.Println("  Try one of these instead:")
		choice := askChoice("  Image", uniqueRepos(problem.Suggestions), 10)
		if choice == "" {
			return err
		}
		if ref, err = oci.ParseRef(choice); err != nil {
			return err
		}
		tags, err = reg.Tags(ctx, ref)
	}
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return fmt.Errorf("%s: no tags returned", ref.Registry+"/"+ref.Repository)
	}
	sort.Strings(tags)
	fmt.Printf("%s/%s: %d tags\n", ref.Registry, ref.Repository, len(tags))
	const perLine = 4
	for i := 0; i < len(tags); i += perLine {
		end := i + perLine
		if end > len(tags) {
			end = len(tags)
		}
		row := make([]string, 0, perLine)
		for _, t := range tags[i:end] {
			row = append(row, fmt.Sprintf("%-24s", t))
		}
		fmt.Printf("  %s\n", strings.TrimRight(strings.Join(row, " "), " "))
	}
	return nil
}

// uniqueRepos strips tags from suggestions, because listing tags only needs
// the repository.
func uniqueRepos(refs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		repo := r
		if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
			repo = repo[:i]
		}
		if seen[repo] {
			continue
		}
		seen[repo] = true
		out = append(out, repo)
	}
	return out
}
