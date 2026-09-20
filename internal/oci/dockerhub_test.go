package oci

import "strings"

import "testing"

func TestSuggestionMapsAreWellFormed(t *testing.T) {
	for key, target := range officialAlias {
		if strings.Contains(target, "/") || strings.Contains(target, ":") {
			t.Errorf("officialAlias[%q] = %q: an official image is a bare name, e.g. \"postgres\"", key, target)
		}
		if key == target {
			t.Errorf("officialAlias[%q] points at itself", key)
		}
		if _, clash := knownElsewhere[key]; clash {
			t.Errorf("%q is in both officialAlias and knownElsewhere", key)
		}
	}
	for key, target := range knownElsewhere {
		host, rest, ok := strings.Cut(target, "/")
		if !ok || !strings.Contains(host, ".") || rest == "" {
			t.Errorf("knownElsewhere[%q] = %q: must be <registry-host>/<repository>", key, target)
		}
		if strings.Contains(target, ":") {
			t.Errorf("knownElsewhere[%q] = %q: must not carry a tag", key, target)
		}
	}
}

func TestShortName(t *testing.T) {
	if got := shortName("library/postgres"); got != "postgres" {
		t.Errorf("shortName = %q", got)
	}
	if got := shortName("bitnami/redis"); got != "bitnami/redis" {
		t.Errorf("shortName should only strip the implicit library prefix, got %q", got)
	}
}
