// Package apt builds a full binary dependency closure from Debian/Ubuntu
// repositories and downloads the resulting .deb files.
package apt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"airgapkit/internal/config"
	"airgapkit/internal/dl"
)

// Package is one stanza of a Packages index.
type Package struct {
	Name         string
	Version      string
	Architecture string
	Filename     string // pool path relative to the repository root
	Size         int64
	SHA256       string
	Depends      string
	PreDepends   string
	Recommends   string
	Provides     string
	Essential    bool
	Priority     string
	MultiArch    string

	// SourceURI is the repository this stanza came from.
	SourceURI string
	// SourceName identifies the repository for diagnostics.
	SourceName string
}

// URL is the absolute download location of the .deb.
func (p *Package) URL() string {
	return strings.TrimRight(p.SourceURI, "/") + "/" + strings.TrimLeft(p.Filename, "/")
}

// PoolPath is where the .deb lands inside the bundle.
func (p *Package) PoolPath() string {
	base := path.Base(p.Filename)
	first := p.Name
	if strings.HasPrefix(first, "lib") && len(first) > 3 {
		first = first[:4]
	} else if first != "" {
		first = first[:1]
	}
	return path.Join("apt/pool", first, p.Name, base)
}

// Index is a searchable view over every repository in the configuration.
type Index struct {
	byName   map[string][]*Package
	provides map[string][]*Package
	all      []*Package
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{byName: map[string][]*Package{}, provides: map[string][]*Package{}}
}

// Add inserts packages, keeping every version so constraints can be resolved.
func (ix *Index) Add(pkgs []*Package) {
	for _, p := range pkgs {
		ix.byName[p.Name] = append(ix.byName[p.Name], p)
		ix.all = append(ix.all, p)
		for _, pv := range parseProvides(p.Provides) {
			ix.provides[pv] = append(ix.provides[pv], p)
		}
	}
}

// Len reports how many stanzas are loaded.
func (ix *Index) Len() int { return len(ix.all) }

// Best returns the highest version of name satisfying the constraint, falling
// back to any package that Provides the name (virtual packages).
func (ix *Index) Best(name, op, version string) *Package {
	name = strings.TrimSuffix(name, ":any")
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i] // drop the architecture qualifier
	}
	var best *Package
	for _, p := range ix.byName[name] {
		if !SatisfiesConstraint(p.Version, op, version) {
			continue
		}
		if best == nil || CompareVersions(p.Version, best.Version) > 0 {
			best = p
		}
	}
	if best != nil {
		return best
	}
	// Virtual package: any provider will do. Version constraints on virtual
	// packages are rare and unversioned providers cannot satisfy them.
	for _, p := range ix.provides[name] {
		if best == nil || CompareVersions(p.Version, best.Version) > 0 {
			best = p
		}
	}
	return best
}

func parseProvides(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.IndexAny(part, " ("); i >= 0 {
			part = strings.TrimSpace(part[:i])
		}
		out = append(out, part)
	}
	return out
}

// Loader fetches and parses repository indexes.
type Loader struct {
	Client  *dl.Client
	Keyring string // optional gpgv keyring for Release verification
	Verbose bool
}

// candidate index file names in the order we prefer them: gzip first because
// compress/gzip is in the standard library, then plain, then formats that need
// an external decompressor.
var indexCandidates = []string{"Packages.gz", "Packages", "Packages.xz", "Packages.bz2", "Packages.zst"}

// Load fetches every component/arch index of one source.
func (l *Loader) Load(ctx context.Context, src config.AptSource, arches []string) ([]*Package, error) {
	var out []*Package
	for _, suite := range src.Suites {
		// A flat repository is written "deb URI /" or "deb URI ./": the
		// index sits directly under the URI instead of under dists/.
		flat := strings.HasSuffix(suite, "/")
		var distBase string
		switch {
		case flat:
			rel := strings.TrimSuffix(strings.TrimPrefix(suite, "./"), "/")
			distBase = strings.TrimRight(src.URI+"/"+rel, "/")
		default:
			distBase = src.URI + "/dists/" + suite
		}
		rel, err := l.loadRelease(ctx, distBase, src)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", distBase, err)
		}
		var paths []string
		if flat {
			paths = []string{""}
		} else {
			paths = src.Components
			if len(paths) == 0 {
				return nil, fmt.Errorf("%s: suite %q needs at least one component", src.URI, suite)
			}
		}
		for _, comp := range paths {
			for _, arch := range arches {
				var prefix string
				if flat {
					prefix = ""
				} else {
					prefix = comp + "/binary-" + arch + "/"
				}
				pkgs, err := l.loadPackages(ctx, distBase, prefix, rel, src)
				if err != nil {
					return nil, err
				}
				out = append(out, pkgs...)
			}
		}
	}
	return out, nil
}

// release holds the digests declared by a Release file.
type release struct {
	sha256 map[string]string // path -> hex digest
	sizes  map[string]int64
}

func (l *Loader) loadRelease(ctx context.Context, distBase string, src config.AptSource) (*release, error) {
	body, _, err := l.Client.GetBytes(ctx, distBase+"/InRelease", nil)
	signed := true
	if err != nil {
		if !dl.IsNotFound(err) {
			return nil, err
		}
		body, _, err = l.Client.GetBytes(ctx, distBase+"/Release", nil)
		if err != nil {
			return nil, err
		}
		signed = false
	}
	keyring := src.Keyring
	if keyring == "" {
		keyring = l.Keyring
	}
	if keyring != "" && !src.Trusted {
		if err := l.verify(ctx, distBase, body, signed, keyring); err != nil {
			return nil, err
		}
	}
	return parseRelease(stripClearsign(body)), nil
}

func (l *Loader) verify(ctx context.Context, distBase string, body []byte, inline bool, keyring string) error {
	tmp, err := os.CreateTemp("", "airgap-release-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	args := []string{"--keyring", keyring}
	if inline {
		args = append(args, tmp.Name())
	} else {
		sig, _, err := l.Client.GetBytes(ctx, distBase+"/Release.gpg", nil)
		if err != nil {
			return fmt.Errorf("Release.gpg: %w", err)
		}
		sigf, err := os.CreateTemp("", "airgap-release-*.gpg")
		if err != nil {
			return err
		}
		defer os.Remove(sigf.Name())
		if _, err := sigf.Write(sig); err != nil {
			sigf.Close()
			return err
		}
		sigf.Close()
		args = append(args, sigf.Name(), tmp.Name())
	}
	cmd := exec.CommandContext(ctx, "gpgv", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gpgv failed for %s: %v: %s", distBase, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// stripClearsign removes the OpenPGP clearsign envelope of an InRelease file.
func stripClearsign(b []byte) []byte {
	s := string(b)
	if !strings.HasPrefix(s, "-----BEGIN PGP SIGNED MESSAGE-----") {
		return b
	}
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.Index(s, "-----BEGIN PGP SIGNATURE-----"); i >= 0 {
		s = s[:i]
	}
	return []byte(s)
}

func parseRelease(b []byte) *release {
	r := &release{sha256: map[string]string{}, sizes: map[string]int64{}}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	inSHA := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if !inSHA {
				continue
			}
			f := strings.Fields(line)
			if len(f) != 3 {
				continue
			}
			size, _ := strconv.ParseInt(f[1], 10, 64)
			r.sha256[f[2]] = f[0]
			r.sizes[f[2]] = size
			continue
		}
		key, _, _ := strings.Cut(line, ":")
		inSHA = strings.EqualFold(strings.TrimSpace(key), "SHA256")
	}
	return r
}

func (l *Loader) loadPackages(ctx context.Context, distBase, prefix string, rel *release, src config.AptSource) ([]*Package, error) {
	var lastErr error
	for _, cand := range indexCandidates {
		relPath := prefix + cand
		want, listed := rel.sha256[relPath]
		if len(rel.sha256) > 0 && !listed {
			continue // not advertised by Release; try the next encoding
		}
		body, _, err := l.Client.GetBytes(ctx, distBase+"/"+relPath, nil)
		if err != nil {
			if dl.IsNotFound(err) {
				lastErr = err
				continue
			}
			return nil, err
		}
		if want != "" {
			sum := sha256.Sum256(body)
			if got := hex.EncodeToString(sum[:]); got != want {
				return nil, fmt.Errorf("%s/%s: sha256 mismatch (Release says %s, got %s)", distBase, relPath, want, got)
			}
		}
		plain, err := decompress(cand, body)
		if err != nil {
			lastErr = err
			continue
		}
		pkgs := parsePackages(plain, src)
		if l.Verbose {
			fmt.Fprintf(os.Stderr, "  %s/%s: %d packages\n", distBase, relPath, len(pkgs))
		}
		return pkgs, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable Packages index under %s/%s", distBase, prefix)
	}
	return nil, fmt.Errorf("%s/%s: %w", distBase, prefix, lastErr)
}

// parsePackages reads deb822 stanzas, keeping only the fields the resolver and
// downloader need.
func parsePackages(b []byte, src config.AptSource) []*Package {
	var out []*Package
	cur := &Package{SourceURI: src.URI, SourceName: src.Name}
	has := false
	var lastKey string

	flush := func() {
		if has && cur.Name != "" && cur.Filename != "" {
			out = append(out, cur)
		}
		cur = &Package{SourceURI: src.URI, SourceName: src.Name}
		has = false
		lastKey = ""
	}

	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			// Continuation of a folded field; only dependency fields matter.
			cont := strings.TrimSpace(line)
			switch lastKey {
			case "depends":
				cur.Depends += " " + cont
			case "pre-depends":
				cur.PreDepends += " " + cont
			case "recommends":
				cur.Recommends += " " + cont
			case "provides":
				cur.Provides += " " + cont
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		lastKey = strings.ToLower(key)
		val = strings.TrimSpace(val)
		has = true
		switch lastKey {
		case "package":
			cur.Name = val
		case "version":
			cur.Version = val
		case "architecture":
			cur.Architecture = val
		case "filename":
			cur.Filename = val
		case "size":
			cur.Size, _ = strconv.ParseInt(val, 10, 64)
		case "sha256":
			cur.SHA256 = val
		case "depends":
			cur.Depends = val
		case "pre-depends":
			cur.PreDepends = val
		case "recommends":
			cur.Recommends = val
		case "provides":
			cur.Provides = val
		case "essential":
			cur.Essential = strings.EqualFold(val, "yes")
		case "priority":
			cur.Priority = val
		case "multi-arch":
			cur.MultiArch = val
		}
	}
	flush()
	return out
}
