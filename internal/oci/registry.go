package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"airgapkit/internal/config"
	"airgapkit/internal/dl"
)

// Media types we accept when asking for a manifest. Both the docker and the
// OCI spellings are listed because registries serve whichever the image was
// pushed with.
const (
	MTDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MTDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	MTDockerConfig       = "application/vnd.docker.container.image.v1+json"
	MTDockerLayerGzip    = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	MTOCIManifest        = "application/vnd.oci.image.manifest.v1+json"
	MTOCIIndex           = "application/vnd.oci.image.index.v1+json"
	MTOCIConfig          = "application/vnd.oci.image.config.v1+json"
	MTOCILayerGzip       = "application/vnd.oci.image.layer.v1.tar+gzip"
	MTOCILayerTar        = "application/vnd.oci.image.layer.v1.tar"
)

var acceptManifest = strings.Join([]string{
	MTOCIIndex, MTOCIManifest, MTDockerManifestList, MTDockerManifest,
}, ", ")

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Platform    *Platform         `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Platform identifies an architecture inside a manifest list.
type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

type manifestDoc struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
	Manifests     []Descriptor `json:"manifests"`
}

// Image is a resolved single-platform image.
type Image struct {
	Ref       Ref
	Manifest  []byte
	MediaType string
	Digest    string
	Config    Descriptor
	Layers    []Descriptor
	// IndexDigest is set when the image was selected out of a manifest list.
	IndexDigest string
}

// Blobs lists every blob that must accompany the manifest, config first.
func (i *Image) Blobs() []Descriptor {
	return append([]Descriptor{i.Config}, i.Layers...)
}

// Registry reads images from a remote registry.
type Registry struct {
	Client   *dl.Client
	Platform string
	auth     *authenticator
}

// NewRegistry builds a client for the given credentials and target platform.
func NewRegistry(c *dl.Client, auths map[string]config.Auth, platform string) *Registry {
	if platform == "" {
		platform = "linux/amd64"
	}
	return &Registry{Client: c, Platform: platform, auth: newAuthenticator(c, auths)}
}

// AuthHeader exposes the pull credentials for blob downloads.
func (r *Registry) AuthHeader(ctx context.Context, ref Ref) (http.Header, error) {
	return r.auth.Header(ctx, ref, "pull")
}

// Resolve fetches the manifest for ref, descending into a manifest list when
// necessary to find the configured platform.
func (r *Registry) Resolve(ctx context.Context, ref Ref) (*Image, error) {
	body, mt, digest, err := r.manifest(ctx, ref, ref.Reference())
	if err != nil {
		return nil, err
	}
	var doc manifestDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: parse manifest: %w", ref, err)
	}
	if mt == "" {
		mt = doc.MediaType
	}
	indexDigest := ""
	if mt == MTOCIIndex || mt == MTDockerManifestList || len(doc.Manifests) > 0 {
		child, err := r.pickPlatform(doc.Manifests)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
		indexDigest = digest
		body, mt, digest, err = r.manifest(ctx, ref, child.Digest)
		if err != nil {
			return nil, err
		}
		doc = manifestDoc{}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("%s: parse child manifest: %w", ref, err)
		}
		if mt == "" {
			mt = doc.MediaType
		}
		if mt == "" {
			mt = child.MediaType
		}
	}
	if doc.Config.Digest == "" || len(doc.Layers) == 0 {
		return nil, fmt.Errorf("%s: manifest has no config or layers (schema 1 images are not supported)", ref)
	}
	return &Image{
		Ref: ref, Manifest: body, MediaType: mt, Digest: digest,
		Config: doc.Config, Layers: doc.Layers, IndexDigest: indexDigest,
	}, nil
}

func (r *Registry) pickPlatform(manifests []Descriptor) (Descriptor, error) {
	wantOS, wantArch, wantVariant := splitPlatform(r.Platform)
	var fallback Descriptor
	for _, m := range manifests {
		if m.Platform == nil {
			continue
		}
		if m.Platform.OS == "unknown" || m.Platform.Architecture == "unknown" {
			continue // attestation manifests
		}
		if m.Platform.OS != wantOS || m.Platform.Architecture != wantArch {
			continue
		}
		if wantVariant != "" && m.Platform.Variant != wantVariant {
			fallback = m
			continue
		}
		return m, nil
	}
	if fallback.Digest != "" {
		return fallback, nil
	}
	return Descriptor{}, fmt.Errorf("no manifest for platform %s", r.Platform)
}

func splitPlatform(p string) (os, arch, variant string) {
	parts := strings.Split(p, "/")
	switch len(parts) {
	case 1:
		return "linux", parts[0], ""
	case 2:
		return parts[0], parts[1], ""
	default:
		return parts[0], parts[1], parts[2]
	}
}

func (r *Registry) manifest(ctx context.Context, ref Ref, reference string) (body []byte, mediaType, digest string, err error) {
	h, err := r.auth.Header(ctx, ref, "pull")
	if err != nil {
		return nil, "", "", err
	}
	h = h.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Set("Accept", acceptManifest)
	url := fmt.Sprintf("%s/%s/manifests/%s", ref.BaseURL(), ref.Repository, reference)
	body, hdr, err := r.Client.GetBytes(ctx, url, h)
	if err != nil {
		return nil, "", "", fmt.Errorf("manifest %s: %w", ref, err)
	}
	sum := sha256.Sum256(body)
	digest = "sha256:" + hex.EncodeToString(sum[:])
	if d := hdr.Get("Docker-Content-Digest"); d != "" && d != digest {
		return nil, "", "", fmt.Errorf("manifest %s: digest mismatch (registry says %s, computed %s)", ref, d, digest)
	}
	return body, hdr.Get("Content-Type"), digest, nil
}

// BlobURL is the download URL of one blob.
func BlobURL(ref Ref, digest string) string {
	return fmt.Sprintf("%s/%s/blobs/%s", ref.BaseURL(), ref.Repository, digest)
}

// BlobPath is the shared bundle location of a blob.
func BlobPath(digest string) string {
	algo, hexsum, ok := strings.Cut(digest, ":")
	if !ok {
		algo, hexsum = "sha256", digest
	}
	return "docker/blobs/" + algo + "/" + hexsum
}
