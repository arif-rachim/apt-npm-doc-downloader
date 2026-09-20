// Package oci downloads container images straight from a registry's v2 API
// into an OCI layout with a shared blob store, so no Docker daemon is needed
// on either side of the air gap and identical layers are stored once.
package oci

import (
	"fmt"
	"strings"
)

// Ref is a parsed image reference.
type Ref struct {
	Registry   string // docker.io, ghcr.io, nexus.example.com:8082
	Repository string // library/nginx
	Tag        string
	Digest     string
	Original   string
}

// ParseRef understands the usual shorthand: "nginx:1.27",
// "ghcr.io/org/app:v1", "registry:5000/team/img@sha256:...".
func ParseRef(s string) (Ref, error) {
	r := Ref{Original: s, Registry: "docker.io", Tag: "latest"}
	rest := s
	if i := strings.Index(rest, "@"); i >= 0 {
		r.Digest = rest[i+1:]
		rest = rest[:i]
		r.Tag = ""
	}
	// A leading component is a registry only when it looks like a host.
	if i := strings.Index(rest, "/"); i >= 0 {
		head := rest[:i]
		if strings.ContainsAny(head, ".:") || head == "localhost" {
			r.Registry = head
			rest = rest[i+1:]
		}
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && !strings.Contains(rest[i:], "/") {
		r.Tag = rest[i+1:]
		rest = rest[:i]
	}
	if rest == "" {
		return Ref{}, fmt.Errorf("invalid image reference %q", s)
	}
	if r.Registry == "docker.io" && !strings.Contains(rest, "/") {
		rest = "library/" + rest
	}
	r.Repository = rest
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}
	return r, nil
}

// Host is the actual registry endpoint to talk to.
func (r Ref) Host() string {
	if r.Registry == "docker.io" {
		return "registry-1.docker.io"
	}
	return r.Registry
}

// Scheme is http for plain localhost style registries, https otherwise.
func (r Ref) Scheme() string {
	if strings.HasPrefix(r.Registry, "localhost") || strings.HasPrefix(r.Registry, "127.0.0.1") {
		return "http"
	}
	return "https"
}

// BaseURL is the /v2 endpoint of the registry.
func (r Ref) BaseURL() string { return r.Scheme() + "://" + r.Host() + "/v2" }

// Reference is the tag or digest used in manifest URLs.
func (r Ref) Reference() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}

// String renders the canonical reference.
func (r Ref) String() string {
	out := r.Registry + "/" + r.Repository
	if r.Digest != "" {
		return out + "@" + r.Digest
	}
	return out + ":" + r.Tag
}

// BundleDir is where this image's metadata lives inside the bundle.
func (r Ref) BundleDir() string {
	tag := r.Tag
	if tag == "" {
		tag = strings.ReplaceAll(r.Digest, ":", "-")
	}
	return "docker/images/" + r.Registry + "/" + r.Repository + "/" + tag
}
