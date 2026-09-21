package oci

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in       string
		registry string
		repo     string
		tag      string
		digest   string
		dir      string
	}{
		{"nginx:1.27", "docker.io", "library/nginx", "1.27", "", "docker/images/docker.io/library/nginx/1.27"},
		{"alpine", "docker.io", "library/alpine", "latest", "", "docker/images/docker.io/library/alpine/latest"},
		{"bitnami/redis:7.2", "docker.io", "bitnami/redis", "7.2", "", "docker/images/docker.io/bitnami/redis/7.2"},
		{"ghcr.io/org/app:v1", "ghcr.io", "org/app", "v1", "", "docker/images/ghcr.io/org/app/v1"},
		{"registry.test:5000/team/img:dev", "registry.test:5000", "team/img", "dev", "", "docker/images/registry.test_5000/team/img/dev"},
		{"alpine@sha256:abc", "docker.io", "library/alpine", "", "sha256:abc", "docker/images/docker.io/library/alpine/sha256-abc"},
	}
	for _, c := range cases {
		got, err := ParseRef(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got.Registry != c.registry || got.Repository != c.repo || got.Tag != c.tag || got.Digest != c.digest {
			t.Errorf("ParseRef(%q) = %+v", c.in, got)
		}
		if got.BundleDir() != c.dir {
			t.Errorf("ParseRef(%q).BundleDir() = %q, want %q", c.in, got.BundleDir(), c.dir)
		}
	}
}

func TestHostAndScheme(t *testing.T) {
	r, _ := ParseRef("nginx:1.27")
	if r.Host() != "registry-1.docker.io" {
		t.Errorf("docker.io must resolve to registry-1.docker.io, got %q", r.Host())
	}
	if r.BaseURL() != "https://registry-1.docker.io/v2" {
		t.Errorf("unexpected base URL %q", r.BaseURL())
	}
	l, _ := ParseRef("localhost:5000/team/img:dev")
	if l.Scheme() != "http" {
		t.Errorf("localhost registries should default to http, got %q", l.Scheme())
	}
}

func TestBlobPath(t *testing.T) {
	if got := BlobPath("sha256:deadbeef"); got != "docker/blobs/sha256/deadbeef" {
		t.Errorf("unexpected blob path %q", got)
	}
}

func TestBundleDirHasNoColon(t *testing.T) {
	// ':' cannot appear in a Windows path, and the bundle is written there.
	for in, want := range map[string]string{
		"registry:5000/team/img:v1": "docker/images/registry_5000/team/img/v1",
		"nginx:1.27":                "docker/images/docker.io/library/nginx/1.27",
	} {
		r, err := ParseRef(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.BundleDir(); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
