package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// runIndex regenerates everything derived from the artifacts already present:
// SHA256SUMS, the local apt repository metadata and the PyPI simple index.
// It is what to run on the air-gapped side after merging a delta with a tool
// other than merge.sh.
func runIndex(o *options) error {
	arch := "amd64"
	if len(o.cfg.APT.Arch) > 0 {
		arch = o.cfg.APT.Arch[0]
	}
	if err := generateAptRepo(o, arch); err != nil {
		return err
	}
	if err := generateSimpleIndex(o); err != nil {
		return err
	}
	if err := writeBundleScripts(o); err != nil {
		return err
	}
	if err := o.store.WriteSums(); err != nil {
		return err
	}
	fmt.Printf("index rebuilt for %s (%d artifacts)\n", o.store.Root, len(o.store.All()))
	return nil
}

// runVerify re-hashes every artifact listed in the manifest. This is the
// check to run after copying the bundle across a USB drive.
func runVerify(o *options) error {
	entries := o.store.All()
	var missing, corrupt int
	for _, e := range entries {
		f, err := os.Open(o.store.Abs(e.RelPath))
		if err != nil {
			fmt.Printf("MISSING  %s\n", e.RelPath)
			missing++
			continue
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != e.SHA256 {
			fmt.Printf("CORRUPT  %s (manifest %s, file %s)\n", e.RelPath, short(e.SHA256), short(got))
			corrupt++
		}
	}
	fmt.Printf("verified %d artifacts: %d missing, %d corrupt\n", len(entries), missing, corrupt)
	if missing+corrupt > 0 {
		return fmt.Errorf("%d artifacts failed verification", missing+corrupt)
	}
	return nil
}

func short(sum string) string {
	if len(sum) <= 12 {
		return sum
	}
	return strings.ToLower(sum[:12])
}
