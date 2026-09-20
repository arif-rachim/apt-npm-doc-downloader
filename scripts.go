// Package airgapkit embeds the air-gapped side of the toolkit: the push,
// merge and verify scripts are written into every bundle so the USB drive
// that reaches the isolated network is self contained and needs nothing from
// this repository.
package airgapkit

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed scripts
var scriptFS embed.FS

// BundleQuickstart is dropped at the bundle root as a one page reminder of
// what to do with the directory once it has crossed the air gap.
const BundleQuickstart = `Bundle airgapkit
================

Folder ini berisi seluruh artefak (apt / npm / pypi / docker) beserta script
untuk mendorongnya ke Nexus. Tidak perlu internet, Go, python, npm, docker,
atau jq di sisi airgap - cukup bash dan curl.

Langkah di mesin airgap:

  1. Gabungkan ke mirror permanen (lewati bila folder ini memang mirrornya):
       ./scripts/merge.sh /media/usb/<folder-ini> /srv/airgap-mirror

  2. Pastikan tidak ada file rusak setelah transfer:
       ./scripts/verify.sh --bundle /srv/airgap-mirror

  3. Dorong ke Nexus (interaktif saat pertama kali; berikutnya incremental):
       ./scripts/push.sh --bundle /srv/airgap-mirror
       ./scripts/push.sh --bundle /srv/airgap-mirror apt npm   # sebagian saja
       ./scripts/push.sh --bundle /srv/airgap-mirror --dry-run

Isi folder:
  MANIFEST.tsv   daftar seluruh artefak (eco, sha256, ukuran, path, sumber)
  SHA256SUMS     dipakai verify.sh
  scripts/       push.sh, merge.sh, verify.sh + lib/
  apt/ npm/ pypi/ docker/   artefaknya
`

// Version and BuildDate are set by the caller from the binary's build
// stamps, so the scripts a bundle carries can say which build wrote them.
var (
	Version   = "dev"
	BuildDate = "unknown"
)

// ScriptsDigest is a content hash of the embedded scripts. The build date
// changes on every rebuild even when the scripts did not, so this is what
// actually answers "is this bundle's copy the current one?".
func ScriptsDigest() string {
	h := sha256.New()
	_ = fs.WalkDir(scriptFS, "scripts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := scriptFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		fmt.Fprintf(h, "%s\n%d\n", p, len(data))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// WriteScripts materialises the embedded scripts under dir/scripts. Files are
// only rewritten when their content differs, so re-running a fetch does not
// churn timestamps on the mirror.
//
// A version stamp is written alongside them: the scripts inside a bundle are
// a copy, and without this there is no way to tell that a bundle carries an
// older set than the binary in hand.
func WriteScripts(dir string) error {
	_, err := WriteScriptsReport(dir)
	return err
}

// WriteScriptsReport writes the scripts and reports whether anything on disk
// actually differed, so "airgap scripts" can say plainly whether a bundle was
// already current.
func WriteScriptsReport(dir string) (changed bool, err error) {
	if old, rerr := os.ReadFile(filepath.Join(dir, "scripts", ".airgap-version")); rerr == nil {
		if !strings.Contains(string(old), "AIRGAP_SCRIPTS_SHA256="+ScriptsDigest()) {
			changed = true
		}
	} else {
		changed = true
	}
	return changed, writeScripts(dir)
}

func writeScripts(dir string) error {
	stamp := fmt.Sprintf("AIRGAP_SCRIPTS_VERSION=%s\nAIRGAP_SCRIPTS_BUILT=%s\nAIRGAP_SCRIPTS_SHA256=%s\n",
		Version, BuildDate, ScriptsDigest())
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", ".airgap-version"), []byte(stamp), 0o644); err != nil {
		return err
	}
	return fs.WalkDir(scriptFS, "scripts", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := scriptFS.ReadFile(p)
		if err != nil {
			return err
		}
		// Entry points must stay executable; lib/ files are only sourced.
		mode := os.FileMode(0o644)
		if filepath.Dir(p) == "scripts" {
			mode = 0o755
		}
		if old, err := os.ReadFile(target); err == nil && string(old) == string(data) {
			return os.Chmod(target, mode)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, mode)
	})
}

// WriteQuickstart drops the README that explains the bundle to whoever
// receives it.
func WriteQuickstart(dir string) error {
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(BundleQuickstart), 0o644)
}
