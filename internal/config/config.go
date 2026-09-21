// Package config loads airgap.json plus apt sources given in sources.list or
// deb822 syntax, so third party repositories can simply be pasted in.
package config

import (
	"airgapkit/internal/textio"
	"encoding/json"
	"fmt"
	"strings"
)

// Config is the whole tool configuration. Every field has a usable default so
// a minimal config file (or none at all, with CLI flags) works.
type Config struct {
	Out         string `json:"out"`
	DeltaOut    string `json:"delta_out"`
	Concurrency int    `json:"concurrency"`

	APT    APT    `json:"apt"`
	NPM    NPM    `json:"npm"`
	PyPI   PyPI   `json:"pypi"`
	Docker Docker `json:"docker"`
}

// AptSource is one binary repository.
type AptSource struct {
	Name       string   `json:"name"`
	URI        string   `json:"uri"`
	Suites     []string `json:"suites"`
	Components []string `json:"components"`
	Arch       []string `json:"arch"`
	// Trusted skips the (optional) gpgv check for this source only.
	Trusted bool `json:"trusted"`
	// Keyring verifies this source's Release with its own vendor key, which
	// is what third party repositories need: one global keyring cannot hold
	// the right key for every vendor.
	Keyring string `json:"keyring"`
}

// APT configures the Debian package mirror.
type APT struct {
	Arch    []string    `json:"arch"`
	Sources []AptSource `json:"sources"`
	// SourcesFiles are paths to sources.list / .sources files to merge in.
	SourcesFiles []string `json:"sources_files"`
	// Repos names entries from the built-in catalogue, e.g. ["docker","pgdg"].
	Repos    []string `json:"repos"`
	Packages []string `json:"packages"`
	// IncludeRecommends widens the closure to Recommends as well.
	IncludeRecommends bool `json:"include_recommends"`
	// Exclude drops packages (and their otherwise-unreachable subtrees).
	Exclude []string `json:"exclude"`
	// Prefer picks a winner for "a | b" alternatives, keyed by the first
	// alternative's name.
	Prefer map[string]string `json:"prefer"`
	// GPGVKeyring enables Release signature verification with gpgv.
	GPGVKeyring string `json:"gpgv_keyring"`
	// GenRepo writes local Packages/Release metadata for file:// use.
	GenRepo bool `json:"gen_repo"`
	// DefaultSources controls whether the built-in Ubuntu 24.04 archives are
	// added on top of whatever the user configured. The target is always
	// noble, and every package needs libc6 and friends, so this defaults to
	// true; set it to false to mirror from an internal Ubuntu mirror only.
	DefaultSources *bool `json:"default_sources"`
}

// NPM configures the npm mirror.
type NPM struct {
	Registry   string   `json:"registry"`
	Lockfiles  []string `json:"lockfiles"`
	Manifests  []string `json:"manifests"`
	Packages   []string `json:"packages"`
	IncludeDev *bool    `json:"include_dev"`
	// AllPlatforms keeps optional packages built for other operating systems
	// and architectures. The target is always Ubuntu 24.04 amd64, so by
	// default those are dropped: npm lockfiles list darwin, win32, android
	// and arm binaries that can never run there.
	AllPlatforms bool `json:"all_platforms"`
}

// PyPI configures the Python mirror.
type PyPI struct {
	Index         string   `json:"index"`
	Requirements  []string `json:"requirements"`
	UVLocks       []string `json:"uv_locks"`
	Projects      []string `json:"projects"`
	Packages      []string `json:"packages"`
	Python        string   `json:"python"`
	GlibcMax      string   `json:"glibc_max"`
	SdistFallback *bool    `json:"sdist_fallback"`
	GenIndex      bool     `json:"gen_index"`
}

// Auth is a registry credential.
type Auth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

// Docker configures the OCI image mirror.
type Docker struct {
	Images   []string        `json:"images"`
	Platform string          `json:"platform"`
	Auths    map[string]Auth `json:"auths"`
	// ConvertToDockerV2 rewrites OCI manifests to docker schema2 for
	// registries older than Nexus 3.71.
	ConvertToDockerV2 bool `json:"convert_to_docker_v2"`
}

// UbuntuCodename is the release this toolkit targets.
const UbuntuCodename = "noble"

// DefaultAptSources are the Ubuntu 24.04 archives every mirror needs.
func DefaultAptSources() []AptSource {
	comps := []string{"main", "universe", "restricted", "multiverse"}
	return []AptSource{
		{
			Name: "ubuntu-" + UbuntuCodename,
			// HTTPS: apt itself can rely on plain HTTP because it checks
			// the GPG signature on Release, but gpgv here is optional, so
			// transport security is what stops a tampered index.
			URI:        "https://archive.ubuntu.com/ubuntu",
			Suites:     []string{UbuntuCodename, UbuntuCodename + "-updates"},
			Components: comps,
		},
		{
			Name:       "ubuntu-" + UbuntuCodename + "-security",
			URI:        "https://security.ubuntu.com/ubuntu",
			Suites:     []string{UbuntuCodename + "-security"},
			Components: comps,
		},
	}
}

// UseDefaultSources reports whether the built-in Ubuntu archives apply.
func (a APT) UseDefaultSources() bool { return a.DefaultSources == nil || *a.DefaultSources }

// Default returns the built-in configuration for Ubuntu 24.04 / amd64.
func Default() *Config {
	t := true
	return &Config{
		Out:         "./bundle",
		Concurrency: 8,
		APT: APT{
			Arch:   []string{"amd64"},
			Prefer: map[string]string{},
		},
		NPM: NPM{
			Registry:   "https://registry.npmjs.org",
			IncludeDev: &t,
		},
		PyPI: PyPI{
			Index:         "https://pypi.org/simple",
			Python:        "3.12",
			GlibcMax:      "2.39",
			SdistFallback: &t,
		},
		Docker: Docker{
			Platform: "linux/amd64",
			Auths:    map[string]Auth{},
		},
	}
}

// Load reads path (may be empty) on top of Default.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := textio.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(cfg.APT.Arch) == 0 {
		cfg.APT.Arch = []string{"amd64"}
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if cfg.APT.Prefer == nil {
		cfg.APT.Prefer = map[string]string{}
	}
	if cfg.Docker.Auths == nil {
		cfg.Docker.Auths = map[string]Auth{}
	}
	return cfg, nil
}

// IncludeDevDeps reports whether npm dev dependencies are wanted.
func (n NPM) IncludeDevDeps() bool { return n.IncludeDev == nil || *n.IncludeDev }

// LinuxOnly reports whether optional packages for other platforms are
// filtered out, which is the default for this fixed target.
func (n NPM) LinuxOnly() bool { return !n.AllPlatforms }

// AllowSdist reports whether a source distribution may stand in for a missing
// wheel.
func (p PyPI) AllowSdist() bool { return p.SdistFallback == nil || *p.SdistFallback }
