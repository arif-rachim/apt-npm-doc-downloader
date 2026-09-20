package pypi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// uvBinary returns the uv executable or an actionable error.
func uvBinary() (string, error) {
	p, err := exec.LookPath("uv")
	if err != nil {
		return "", fmt.Errorf("uv is required to resolve unpinned Python requirements; " +
			"install uv, or pass an already pinned requirements.txt / uv.lock")
	}
	return p, nil
}

// LockProject runs `uv lock` on a copy of a project directory and returns the
// parsed lockfile. The copy keeps the user's working tree untouched.
func LockProject(ctx context.Context, pyproject, python string) ([]LockPackage, error) {
	uv, err := uvBinary()
	if err != nil {
		return nil, err
	}
	srcDir := filepath.Dir(pyproject)
	tmp, err := os.MkdirTemp("", "airgap-uv-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(srcDir, tmp); err != nil {
		return nil, err
	}
	args := []string{"lock"}
	if python != "" {
		args = append(args, "--python", python)
	}
	cmd := exec.CommandContext(ctx, uv, args...)
	cmd.Dir = tmp
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("uv %s in %s: %v: %s", strings.Join(args, " "), srcDir, err, strings.TrimSpace(errb.String()))
	}
	return ParseUVLock(filepath.Join(tmp, "uv.lock"))
}

// CompileRequirements pins an unpinned requirements file with
// `uv pip compile`, returning the pinned content.
func CompileRequirements(ctx context.Context, path, python string) ([]byte, error) {
	uv, err := uvBinary()
	if err != nil {
		return nil, err
	}
	args := []string{"pip", "compile", "--quiet", "--no-header"}
	if python != "" {
		args = append(args, "--python-version", python)
	}
	args = append(args, path)
	cmd := exec.CommandContext(ctx, uv, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("uv pip compile %s: %v: %s", path, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// copyTree copies a project directory, skipping virtualenvs and VCS data.
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
		if d.IsDir() && (base == ".venv" || base == ".git" || base == "__pycache__") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
