package oci

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ImagesIndex is the plain-text image catalogue push.sh reads. It is a TSV so
// the air-gapped side needs neither jq nor any JSON parsing in bash.
const ImagesIndex = "docker/IMAGES.tsv"

// ImageRecord is one line of IMAGES.tsv.
type ImageRecord struct {
	Ref          string
	ManifestPath string // bundle relative
	MediaType    string
	Digest       string
	BlobsPath    string // bundle relative TSV of blobs, config first
}

func (r ImageRecord) line() string {
	return strings.Join([]string{r.Ref, r.ManifestPath, r.MediaType, r.Digest, r.BlobsPath}, "\t")
}

// WriteImage stores an image's metadata in the bundle. Blob contents are
// downloaded separately into the shared store; this writes the OCI layout
// that points at them plus the TSV sidecars used by push.sh.
func WriteImage(bundleRoot string, img *Image, convert bool) (ImageRecord, error) {
	manifest, mediaType, digest := img.Manifest, img.MediaType, img.Digest
	if convert {
		var err error
		manifest, mediaType, digest, err = toDockerV2(img)
		if err != nil {
			return ImageRecord{}, err
		}
	}

	// The manifest is itself a blob in the shared store, which is what makes
	// the per-image directory a valid OCI layout.
	manifestBlob := BlobPath(digest)
	if err := writeFile(filepath.Join(bundleRoot, filepath.FromSlash(manifestBlob)), manifest); err != nil {
		return ImageRecord{}, err
	}

	dir := img.Ref.BundleDir()
	absDir := filepath.Join(bundleRoot, filepath.FromSlash(dir))
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return ImageRecord{}, err
	}
	if err := writeFile(filepath.Join(absDir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`+"\n")); err != nil {
		return ImageRecord{}, err
	}
	index := map[string]any{
		"schemaVersion": 2,
		"manifests": []map[string]any{{
			"mediaType": mediaType,
			"digest":    digest,
			"size":      len(manifest),
			"annotations": map[string]string{
				"org.opencontainers.image.ref.name": img.Ref.Tag,
				"io.airgapkit.source":               img.Ref.String(),
			},
		}},
	}
	idxBytes, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return ImageRecord{}, err
	}
	if err := writeFile(filepath.Join(absDir, "index.json"), append(idxBytes, '\n')); err != nil {
		return ImageRecord{}, err
	}
	// A relative symlink makes the directory a self-contained OCI layout
	// while keeping a single copy of every layer. Filesystems without
	// symlink support simply do not get it; the TSV sidecars still work.
	linkTarget := strings.Repeat("../", strings.Count(dir, "/")) + "blobs"
	linkPath := filepath.Join(absDir, "blobs")
	if _, err := os.Lstat(linkPath); err != nil {
		_ = os.Symlink(linkTarget, linkPath)
	}
	if err := writeFile(filepath.Join(absDir, "manifest.json"), manifest); err != nil {
		return ImageRecord{}, err
	}

	var blobs strings.Builder
	for _, d := range img.Blobs() {
		mt := d.MediaType
		if convert {
			mt = convertMediaType(mt)
		}
		fmt.Fprintf(&blobs, "%s\t%d\t%s\t%s\n", d.Digest, d.Size, BlobPath(d.Digest), mt)
	}
	blobsRel := dir + "/blobs.tsv"
	if err := writeFile(filepath.Join(bundleRoot, filepath.FromSlash(blobsRel)), []byte(blobs.String())); err != nil {
		return ImageRecord{}, err
	}

	rec := ImageRecord{
		Ref:          img.Ref.String(),
		ManifestPath: dir + "/manifest.json",
		MediaType:    mediaType,
		Digest:       digest,
		BlobsPath:    blobsRel,
	}
	if err := upsertImageRecord(bundleRoot, rec); err != nil {
		return ImageRecord{}, err
	}
	return rec, nil
}

// upsertImageRecord keeps IMAGES.tsv sorted and free of duplicates so a
// re-download of the same tag replaces the old line instead of stacking up.
func upsertImageRecord(bundleRoot string, rec ImageRecord) error {
	path := filepath.Join(bundleRoot, filepath.FromSlash(ImagesIndex))
	lines := map[string]string{}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			key := strings.SplitN(line, "\t", 2)[0]
			lines[key] = line
		}
		f.Close()
	} else if !os.IsNotExist(err) {
		return err
	}
	lines[rec.Ref] = rec.line()

	keys := make([]string, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(lines[k])
		b.WriteByte('\n')
	}
	return writeFile(path, []byte(b.String()))
}

// toDockerV2 rewrites an OCI manifest into docker schema2 for registries that
// predate OCI support (Nexus older than 3.71).
func toDockerV2(img *Image) ([]byte, string, string, error) {
	if img.MediaType == MTDockerManifest {
		return img.Manifest, img.MediaType, img.Digest, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(img.Manifest, &doc); err != nil {
		return nil, "", "", err
	}
	doc["mediaType"] = MTDockerManifest
	if cfg, ok := doc["config"].(map[string]any); ok {
		cfg["mediaType"] = convertMediaType(str(cfg["mediaType"]))
	}
	if layers, ok := doc["layers"].([]any); ok {
		for _, l := range layers {
			lm, ok := l.(map[string]any)
			if !ok {
				continue
			}
			mt := str(lm["mediaType"])
			converted := convertMediaType(mt)
			if converted == mt && strings.Contains(mt, "zstd") {
				return nil, "", "", fmt.Errorf("%s: zstd layers cannot be converted to docker schema2; push to a registry with OCI support instead", img.Ref)
			}
			lm["mediaType"] = converted
		}
	}
	delete(doc, "annotations")
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, "", "", err
	}
	sum := sha256.Sum256(out)
	return out, MTDockerManifest, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func convertMediaType(mt string) string {
	switch mt {
	case MTOCIConfig:
		return MTDockerConfig
	case MTOCILayerGzip:
		return MTDockerLayerGzip
	case MTOCILayerTar:
		return "application/vnd.docker.image.rootfs.diff.tar"
	}
	return mt
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
