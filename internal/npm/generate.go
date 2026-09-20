package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GenerateLockFromManifest produces a package-lock.json for a project that has
// none. The project is copied to a temporary directory first so the user's
// working tree is never modified, then `npm install --package-lock-only` runs
// there. This is the "hybrid" path: npm decides the versions, airgapkit still
// does the downloading.
func GenerateLockFromManifest(ctx context.Context, manifestPath, registry string) ([]byte, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return nil, fmt.Errorf("npm is required to resolve %s; install npm or run "+
			"`npm install --package-lock-only --ignore-scripts` yourself and pass the lockfile", manifestPath)
	}
	srcDir := filepath.Dir(manifestPath)
	tmp, err := os.MkdirTemp("", "airgap-npm-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(srcDir, tmp); err != nil {
		return nil, err
	}

	args := []string{"install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"}
	if hasWorkspaces(filepath.Join(tmp, "package.json")) {
		args = append(args, "--workspaces", "--include-workspace-root")
	}
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Dir = tmp
	var errb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("npm %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return os.ReadFile(filepath.Join(tmp, "package-lock.json"))
}

// GenerateLockFromSpecs resolves a bare list of specs such as
// ["typescript@5.6.2", "express"] into a full lockfile.
func GenerateLockFromSpecs(ctx context.Context, specs []string, registry string) ([]byte, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	tmp, err := os.MkdirTemp("", "airgap-npm-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	deps := map[string]string{}
	for _, spec := range specs {
		name, version := splitSpec(spec)
		deps[name] = version
	}
	pkg := map[string]any{
		"name":         "airgap-bundle",
		"version":      "1.0.0",
		"private":      true,
		"dependencies": deps,
	}
	b, _ := json.MarshalIndent(pkg, "", "  ")
	manifest := filepath.Join(tmp, "package.json")
	if err := os.WriteFile(manifest, b, 0o644); err != nil {
		return nil, err
	}
	return GenerateLockFromManifest(ctx, manifest, registry)
}

func splitSpec(spec string) (name, version string) {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, "latest"
}

func hasWorkspaces(manifest string) bool {
	b, err := os.ReadFile(manifest)
	if err != nil {
		return false
	}
	var m struct {
		Workspaces any `json:"workspaces"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	return m.Workspaces != nil
}

// copyTree copies a project directory, skipping node_modules and VCS data.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := d.Name()
		if d.IsDir() && (base == "node_modules" || base == ".git") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
