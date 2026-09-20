package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Docker Hub answers a pull for a repository that does not exist with 401
// rather than 404, because it will not reveal whether a name is private or
// simply absent. That makes "authentication required" the single most
// misleading error this tool can print, so for docker.io we ask Hub's public
// API what is actually going on and say so plainly.
const hubAPI = "https://hub.docker.com/v2"

// knownElsewhere maps names people reach for on Docker Hub to the registry
// that actually publishes them. Microsoft in particular ships nothing under
// docker.io any more, so "mssql" or "dotnet" will always look like a typo
// unless the real location is spelled out. Every entry was checked against
// that registry's own /v2/<repo>/tags/list.
var knownElsewhere = map[string]string{
	"mssql":         "mcr.microsoft.com/mssql/server",
	"mssql-server":  "mcr.microsoft.com/mssql/server",
	"sqlserver":     "mcr.microsoft.com/mssql/server",
	"sql-server":    "mcr.microsoft.com/mssql/server",
	"dotnet":        "mcr.microsoft.com/dotnet/sdk",
	"dotnet-sdk":    "mcr.microsoft.com/dotnet/sdk",
	"aspnet":        "mcr.microsoft.com/dotnet/aspnet",
	"powershell":    "mcr.microsoft.com/powershell",
	"pwsh":          "mcr.microsoft.com/powershell",
	"azure-cli":     "mcr.microsoft.com/azure-cli",
	"devcontainer":  "mcr.microsoft.com/devcontainers/base",
	"devcontainers": "mcr.microsoft.com/devcontainers/base",
}

// officialAlias maps names people commonly type to the official Docker Hub
// image that actually carries the software. Each target was checked against
// Hub's repository API.
var officialAlias = map[string]string{
	"pgsql":      "postgres",
	"postgresql": "postgres",
	"psql":       "postgres",
	"mongodb":    "mongo",
	"nodejs":     "node",
	"rabbit":     "rabbitmq",
	"memcache":   "memcached",
	"java":       "openjdk",
	"jdk":        "openjdk",
}

// notOnHub builds a diagnosis for a name Docker Hub does not publish, used
// by Tags where there is no pull error to inspect.
func (r *Registry) notOnHub(ctx context.Context, ref Ref, why string) *PullProblem {
	return &PullProblem{
		Kind:        ProblemNotFound,
		Ref:         ref,
		Message:     fmt.Sprintf("%s: %s", ref.Registry+"/"+ref.Repository, why),
		Suggestions: r.nameSuggestions(ctx, ref),
	}
}

// Problem kinds returned by Diagnose.
const (
	ProblemNotFound   = "not-found"
	ProblemTagMissing = "tag-missing"
	ProblemPrivate    = "private"
	ProblemOther      = "other"
)

// PullProblem is a diagnosed pull failure together with references that are
// worth trying instead, so the caller can offer them rather than stopping.
type PullProblem struct {
	Kind        string
	Ref         Ref
	Message     string
	Suggestions []string
	Err         error
}

// Error renders a diagnosis for a non-interactive run: the explanation plus
// whatever alternatives were found, so even a scripted run is told what to
// do next instead of only what went wrong.
func (p *PullProblem) Error() string {
	if len(p.Suggestions) == 0 {
		return p.Message
	}
	shown := p.Suggestions
	more := ""
	if len(shown) > 5 {
		more = fmt.Sprintf(" (and %d more)", len(shown)-5)
		shown = shown[:5]
	}
	label := "try instead"
	if p.Kind == ProblemTagMissing {
		label = "tags that exist"
	}
	return fmt.Sprintf("%s\n         %s: %s%s", p.Message, label, strings.Join(shown, ", "), more)
}

// Diagnose works out why a pull failed and what to try instead.
func (r *Registry) Diagnose(ctx context.Context, ref Ref, err error) *PullProblem {
	p := &PullProblem{Kind: ProblemOther, Ref: ref, Err: err, Message: err.Error()}
	switch {
	case isStatus(err, http.StatusUnauthorized), isStatus(err, http.StatusNotFound):
	default:
		return p
	}

	if ref.Registry != "docker.io" {
		if isStatus(err, http.StatusUnauthorized) {
			p.Kind = ProblemPrivate
			p.Message = fmt.Sprintf("%s: %s requires credentials; add them under docker.auths in airgap.json:\n"+
				"         \"auths\": { %q: { \"username\": \"...\", \"password\": \"...\" } }", ref, ref.Registry, ref.Registry)
			return p
		}
		p.Kind = ProblemNotFound
		p.Message = fmt.Sprintf("%s: not found on %s (check the repository name and tag)", ref, ref.Registry)
		if tags, terr := r.Tags(ctx, ref); terr == nil && len(tags) > 0 {
			p.Kind = ProblemTagMissing
			p.Suggestions = refsWithTags(ref, tags)
		}
		return p
	}

	exists, tagged := r.hubRepository(ctx, ref)
	switch {
	case exists && !tagged:
		p.Kind = ProblemTagMissing
		p.Message = fmt.Sprintf("%s: the repository exists but the tag %q does not", ref, ref.Tag)
		if tags, terr := r.Tags(ctx, ref); terr == nil {
			p.Suggestions = refsWithTags(ref, tags)
		}
	case exists:
		p.Kind = ProblemPrivate
		p.Message = fmt.Sprintf("%s: this Docker Hub repository is private; add credentials under docker.auths in airgap.json:\n"+
			"         \"auths\": { \"docker.io\": { \"username\": \"...\", \"password\": \"<access token>\" } }", ref)
	default:
		p.Kind = ProblemNotFound
		p.Message = fmt.Sprintf("%s: no such image on Docker Hub (Hub answers 401 instead of 404 for names that "+
			"do not exist, which is why the raw error says \"authentication required\")", ref)
		p.Suggestions = r.nameSuggestions(ctx, ref)
	}
	return p
}

// refsWithTags turns a tag list into full references, newest first.
func refsWithTags(ref Ref, tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, ref.Registry+"/"+ref.Repository+":"+t)
	}
	return out
}

// nameSuggestions proposes other repositories for a name Docker Hub does not
// have: first the curated mappings, then the most pulled search hits. The
// tag the user asked for is carried over.
func (r *Registry) nameSuggestions(ctx context.Context, ref Ref) []string {
	name := strings.ToLower(shortName(ref.Repository))
	tag := ref.Tag
	if tag == "" {
		tag = "latest"
	}
	var out []string
	if alt, ok := officialAlias[name]; ok {
		out = append(out, "docker.io/library/"+alt+":"+tag)
	}
	if alt, ok := knownElsewhere[name]; ok {
		out = append(out, alt+":"+tag)
	}
	for _, hit := range r.hubSearch(ctx, shortName(ref.Repository)) {
		out = append(out, "docker.io/"+hit+":"+tag)
	}
	return out
}

func isStatus(err error, code int) bool {
	return err != nil && strings.Contains(err.Error(), fmt.Sprintf("%d %s", code, http.StatusText(code)))
}

func shortName(repo string) string {
	return strings.TrimPrefix(repo, "library/")
}

// hubRepository reports whether the repository exists publicly on Docker Hub
// and whether the requested tag is listed.
func (r *Registry) hubRepository(ctx context.Context, ref Ref) (exists, tagged bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body, _, err := r.Client.GetBytes(ctx, hubAPI+"/repositories/"+ref.Repository+"/", nil)
	if err != nil {
		return false, false
	}
	exists = len(body) > 0
	if !exists || ref.Tag == "" {
		return exists, false
	}
	tagBody, _, err := r.Client.GetBytes(ctx, hubAPI+"/repositories/"+ref.Repository+"/tags/"+ref.Tag+"/", nil)
	return exists, err == nil && len(tagBody) > 0
}

// hubSearch returns a few existing repositories with a similar name, so a
// typo is easy to spot.
func (r *Registry) hubSearch(ctx context.Context, query string) []string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body, _, err := r.Client.GetBytes(ctx, hubAPI+"/search/repositories/?page_size=5&query="+query, nil)
	if err != nil {
		return nil
	}
	var resp struct {
		Results []struct {
			Name       string `json:"repo_name"`
			IsOfficial bool   `json:"is_official"`
			PullCount  int64  `json:"pull_count"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	// Relevance order puts obscure forks first; the most pulled ones are the
	// likelier answer.
	sort.Slice(resp.Results, func(i, j int) bool {
		if resp.Results[i].IsOfficial != resp.Results[j].IsOfficial {
			return resp.Results[i].IsOfficial
		}
		return resp.Results[i].PullCount > resp.Results[j].PullCount
	})
	var out []string
	for _, e := range resp.Results {
		if len(out) == 3 {
			break
		}
		out = append(out, e.Name)
	}
	return out
}

// Tags lists the tags Docker Hub publishes for a repository.
func (r *Registry) Tags(ctx context.Context, ref Ref) ([]string, error) {
	if ref.Registry != "docker.io" {
		// The registry API exposes the same thing, but it needs a pull token.
		h, err := r.auth.Header(ctx, ref, "pull")
		if err != nil {
			return nil, err
		}
		body, _, err := r.Client.GetBytes(ctx, fmt.Sprintf("%s/%s/tags/list", ref.BaseURL(), ref.Repository), h)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		return resp.Tags, nil
	}
	body, _, err := r.Client.GetBytes(ctx, hubAPI+"/repositories/"+ref.Repository+"/tags/?page_size=100&ordering=last_updated", nil)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return nil, r.notOnHub(ctx, ref, "no such repository on Docker Hub")
		}
		return nil, fmt.Errorf("list tags for %s: %w", ref, err)
	}
	var resp struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Results))
	for _, t := range resp.Results {
		out = append(out, t.Name)
	}
	return out, nil
}
