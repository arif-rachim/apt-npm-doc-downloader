package pypi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"airgapkit/internal/dl"
)

// File is one artifact listed by the Simple index.
type File struct {
	Filename       string
	URL            string
	SHA256         string
	RequiresPython string
	Size           int64
	Yanked         bool
	Version        string
	IsWheel        bool
}

// BundlePath is where the artifact lands inside the bundle.
func (f File) BundlePath() string {
	return path.Join("pypi/packages", Normalize(projectOf(f.Filename)), f.Filename)
}

var nameSep = regexp.MustCompile(`[-_.]+`)

// Normalize applies PEP 503 name normalisation.
func Normalize(name string) string {
	return strings.ToLower(nameSep.ReplaceAllString(name, "-"))
}

func projectOf(filename string) string {
	if strings.HasSuffix(filename, ".whl") {
		if w, ok := ParseWheelName(filename); ok {
			return w.Name
		}
	}
	for _, ext := range []string{".tar.gz", ".zip", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(filename, ext) {
			stem := strings.TrimSuffix(filename, ext)
			if i := strings.LastIndex(stem, "-"); i > 0 {
				return stem[:i]
			}
			return stem
		}
	}
	return filename
}

// versionOf extracts the version from an artifact filename.
func versionOf(filename string) string {
	if strings.HasSuffix(filename, ".whl") {
		if w, ok := ParseWheelName(filename); ok {
			return w.Version
		}
		return ""
	}
	for _, ext := range []string{".tar.gz", ".zip", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(filename, ext) {
			stem := strings.TrimSuffix(filename, ext)
			if i := strings.LastIndex(stem, "-"); i > 0 {
				return stem[i+1:]
			}
		}
	}
	return ""
}

// Client talks to a PEP 691 Simple index.
type Client struct {
	HTTP  *dl.Client
	Index string
}

type simpleResponse struct {
	Files []struct {
		Filename       string            `json:"filename"`
		URL            string            `json:"url"`
		Hashes         map[string]string `json:"hashes"`
		RequiresPython string            `json:"requires-python"`
		Size           int64             `json:"size"`
		Yanked         json.RawMessage   `json:"yanked"`
	} `json:"files"`
}

// Files lists every artifact of a project.
func (c *Client) Files(ctx context.Context, project string) ([]File, error) {
	url := strings.TrimRight(c.Index, "/") + "/" + Normalize(project) + "/"
	h := http.Header{}
	h.Set("Accept", "application/vnd.pypi.simple.v1+json")
	body, _, err := c.HTTP.GetBytes(ctx, url, h)
	if err != nil {
		return nil, fmt.Errorf("simple index %s: %w", url, err)
	}
	var resp simpleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("simple index %s: %w", url, err)
	}
	out := make([]File, 0, len(resp.Files))
	for _, f := range resp.Files {
		yanked := false
		if len(f.Yanked) > 0 {
			var b bool
			if err := json.Unmarshal(f.Yanked, &b); err == nil {
				yanked = b
			} else {
				yanked = true // a string reason means yanked
			}
		}
		out = append(out, File{
			Filename:       f.Filename,
			URL:            f.URL,
			SHA256:         f.Hashes["sha256"],
			RequiresPython: f.RequiresPython,
			Size:           f.Size,
			Yanked:         yanked,
			Version:        versionOf(f.Filename),
			IsWheel:        strings.HasSuffix(f.Filename, ".whl"),
		})
	}
	return out, nil
}

// Select picks the artifact to mirror for one pinned version: the
// best-scoring compatible wheel, or the sdist when no wheel fits and sdists
// are allowed. The second return value is true when the result is an sdist
// fallback, which the caller reports as a warning because installing it in an
// air-gapped environment needs a build toolchain.
func Select(t Target, files []File, version string, allowSdist bool) (File, bool, error) {
	var (
		bestWheel File
		bestScore = -1
		sdist     File
		haveSdist bool
	)
	for _, f := range files {
		if version != "" && f.Version != version {
			continue
		}
		if f.Yanked {
			continue
		}
		if !RequiresPythonOK(f.RequiresPython, t) {
			continue
		}
		if f.IsWheel {
			w, ok := ParseWheelName(f.Filename)
			if !ok {
				continue
			}
			s, ok := t.Score(w)
			if !ok {
				continue
			}
			if s > bestScore {
				bestScore, bestWheel = s, f
			}
			continue
		}
		if isSdist(f.Filename) && !haveSdist {
			sdist, haveSdist = f, true
		}
	}
	if bestScore >= 0 {
		return bestWheel, false, nil
	}
	if haveSdist && allowSdist {
		return sdist, true, nil
	}
	return File{}, false, fmt.Errorf("no compatible wheel for version %s (cp%d%d / glibc %d.%d / %s)",
		version, t.PyMajor, t.PyMinor, t.GlibcMajor, t.GlibcMinor, t.Arch)
}

func isSdist(filename string) bool {
	for _, ext := range []string{".tar.gz", ".zip", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

// LatestVersion returns the highest non-yanked version available, used when a
// requirement has no pin.
func LatestVersion(files []File) string {
	var versions []string
	seen := map[string]bool{}
	for _, f := range files {
		if f.Yanked || f.Version == "" || seen[f.Version] {
			continue
		}
		seen[f.Version] = true
		versions = append(versions, f.Version)
	}
	sort.Slice(versions, func(i, j int) bool { return CompareVersions(versions[i], versions[j]) < 0 })
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}
