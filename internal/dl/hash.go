package dl

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// Expect describes an expected digest for a downloaded artifact.
// Algo is one of "sha256", "sha512", "sha1"; Value is hex encoded.
// An empty Expect disables verification (but the sha256 is still computed).
type Expect struct {
	Algo  string
	Value string
}

// NoExpect is the zero value, meaning "verify nothing, just report sha256".
var NoExpect = Expect{}

// ParseSRI parses a Subresource Integrity string as used by npm lockfiles,
// e.g. "sha512-Xy9z...==" (base64), and returns a hex encoded Expect.
// Multiple space separated entries are allowed; the strongest one wins.
func ParseSRI(s string) (Expect, error) {
	best := Expect{}
	rank := map[string]int{"sha1": 1, "sha256": 2, "sha384": 3, "sha512": 4}
	for _, part := range strings.Fields(s) {
		algo, b64, ok := strings.Cut(part, "-")
		if !ok {
			continue
		}
		algo = strings.ToLower(algo)
		if rank[algo] == 0 || rank[algo] <= rank[best.Algo] {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return Expect{}, fmt.Errorf("integrity %q: %w", part, err)
		}
		best = Expect{Algo: algo, Value: hex.EncodeToString(raw)}
	}
	if best.Algo == "" {
		return Expect{}, fmt.Errorf("no usable digest in integrity %q", s)
	}
	if best.Algo == "sha384" {
		// Not supported by the writer below; fall back to no verification.
		return Expect{}, nil
	}
	return best, nil
}

// SHA256 returns an Expect for a hex encoded sha256 digest, tolerating a
// "sha256:" prefix as used by OCI descriptors.
func SHA256(hexsum string) Expect {
	return Expect{Algo: "sha256", Value: strings.TrimPrefix(strings.ToLower(hexsum), "sha256:")}
}

func newHasher(algo string) hash.Hash {
	switch algo {
	case "sha1":
		return sha1.New()
	case "sha512":
		return sha512.New()
	default:
		return sha256.New()
	}
}
