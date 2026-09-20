package apt

import (
	"testing"

	"airgapkit/internal/config"
)

const packagesStanza = `Package: nginx
Version: 1.24.0-2ubuntu7
Architecture: amd64
Depends: libc6 (>= 2.34), nginx-core (<< 1.24.0-2ubuntu7.1~) | nginx-full,
 libssl3t64 (>= 3.0.0)
Pre-Depends: init-system-helpers (>= 1.54~)
Recommends: ssl-cert
Provides: httpd, httpd-cgi
Filename: pool/universe/n/nginx/nginx_1.24.0-2ubuntu7_amd64.deb
Size: 521234
SHA256: 1111111111111111111111111111111111111111111111111111111111111111
Essential: no

Package: nginx-core
Version: 1.24.0-2ubuntu7
Architecture: amd64
Depends: libc6
Filename: pool/universe/n/nginx/nginx-core_1.24.0-2ubuntu7_amd64.deb
Size: 100
SHA256: 2222222222222222222222222222222222222222222222222222222222222222

Package: libc6
Version: 2.39-0ubuntu8
Architecture: amd64
Filename: pool/main/g/glibc/libc6_2.39-0ubuntu8_amd64.deb
Size: 200
SHA256: 3333333333333333333333333333333333333333333333333333333333333333

Package: libssl3t64
Version: 3.0.13-0ubuntu3
Architecture: amd64
Filename: pool/main/o/openssl/libssl3t64_3.0.13-0ubuntu3_amd64.deb
Size: 300
SHA256: 4444444444444444444444444444444444444444444444444444444444444444

Package: init-system-helpers
Version: 1.66ubuntu1
Architecture: all
Filename: pool/main/i/init-system-helpers/init-system-helpers_1.66ubuntu1_all.deb
Size: 400
SHA256: 5555555555555555555555555555555555555555555555555555555555555555

Package: ssl-cert
Version: 1.1.2ubuntu1
Architecture: all
Filename: pool/main/s/ssl-cert/ssl-cert_1.1.2ubuntu1_all.deb
Size: 500
SHA256: 6666666666666666666666666666666666666666666666666666666666666666
`

func testIndex(t *testing.T) *Index {
	t.Helper()
	pkgs := parsePackages([]byte(packagesStanza), config.AptSource{URI: "http://example.test/ubuntu", Name: "test"})
	if len(pkgs) != 6 {
		t.Fatalf("expected 6 stanzas, got %d", len(pkgs))
	}
	ix := NewIndex()
	ix.Add(pkgs)
	return ix
}

func TestParsePackagesFoldedFields(t *testing.T) {
	ix := testIndex(t)
	nginx := ix.Best("nginx", "", "")
	if nginx == nil {
		t.Fatal("nginx not indexed")
	}
	// The Depends field is folded across two lines; the continuation must be
	// joined or libssl3t64 would silently drop out of the closure.
	groups := ParseRelations(nginx.Depends)
	if len(groups) != 3 {
		t.Fatalf("expected 3 dependency groups, got %d from %q", len(groups), nginx.Depends)
	}
	if groups[0][0].Name != "libc6" || groups[0][0].Op != ">=" || groups[0][0].Version != "2.34" {
		t.Errorf("unexpected first dependency: %+v", groups[0][0])
	}
	if len(groups[1]) != 2 || groups[1][1].Name != "nginx-full" {
		t.Errorf("alternatives not parsed: %+v", groups[1])
	}
	if groups[2][0].Name != "libssl3t64" {
		t.Errorf("folded continuation lost: %+v", groups[2])
	}
	if nginx.URL() != "http://example.test/ubuntu/pool/universe/n/nginx/nginx_1.24.0-2ubuntu7_amd64.deb" {
		t.Errorf("unexpected URL %q", nginx.URL())
	}
	if nginx.PoolPath() != "apt/pool/n/nginx/nginx_1.24.0-2ubuntu7_amd64.deb" {
		t.Errorf("unexpected pool path %q", nginx.PoolPath())
	}
}

func TestVirtualPackageResolution(t *testing.T) {
	ix := testIndex(t)
	if p := ix.Best("httpd", "", ""); p == nil || p.Name != "nginx" {
		t.Errorf("virtual package httpd should resolve to nginx, got %v", p)
	}
	if p := ix.Best("libc6:any", "", ""); p == nil || p.Name != "libc6" {
		t.Errorf("architecture qualifier should be ignored, got %v", p)
	}
}

func TestClosureFollowsDependsAndAlternatives(t *testing.T) {
	ix := testIndex(t)
	res, err := (&Resolver{Index: ix, Exclude: map[string]bool{}, Prefer: map[string]string{}}).Closure([]string{"nginx"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range res.Packages {
		got[p.Name] = true
	}
	for _, want := range []string{"nginx", "libc6", "libssl3t64", "init-system-helpers", "nginx-core"} {
		if !got[want] {
			t.Errorf("%s missing from closure %v", want, got)
		}
	}
	if got["ssl-cert"] {
		t.Error("Recommends must not be followed by default")
	}
	if len(res.Missing) != 0 {
		t.Errorf("unexpected unresolved dependencies: %v", res.Missing)
	}
}

func TestClosureIncludesRecommendsWhenAsked(t *testing.T) {
	ix := testIndex(t)
	res, err := (&Resolver{Index: ix, IncludeRecommends: true, Exclude: map[string]bool{}, Prefer: map[string]string{}}).Closure([]string{"nginx"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Packages {
		if p.Name == "ssl-cert" {
			return
		}
	}
	t.Error("ssl-cert should be included when Recommends are followed")
}

func TestPreferPicksAlternative(t *testing.T) {
	ix := testIndex(t)
	r := &Resolver{Index: ix, Exclude: map[string]bool{}, Prefer: map[string]string{"nginx-core": "nginx-full"}}
	res, err := r.Closure([]string{"nginx"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Packages {
		if p.Name == "nginx-core" {
			t.Error("nginx-full was preferred, nginx-core should not be selected")
		}
	}
	// nginx-full is not in the index, so it must be reported as missing
	// rather than silently swapped back.
	if len(res.Missing) == 0 {
		t.Error("expected nginx-full to be reported as unresolved")
	}
}

func TestSourcesListParsing(t *testing.T) {
	cfg := config.Default()
	cfg.APT.Sources = []config.AptSource{{
		URI: "https://download.docker.com/linux/ubuntu/", Suites: []string{"noble"}, Components: []string{"stable"},
	}}
	got, err := cfg.AptSources()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].URI != "https://download.docker.com/linux/ubuntu" {
		t.Errorf("trailing slash should be trimmed, got %q", got[0].URI)
	}
	if len(got[0].Arch) != 1 || got[0].Arch[0] != "amd64" {
		t.Errorf("architecture should default to the global setting, got %v", got[0].Arch)
	}
}

func TestSuggestNearMisses(t *testing.T) {
	ix := testIndex(t)
	cases := []struct {
		query string
		want  string
	}{
		{"ngnix", "nginx"},          // transposition
		{"nginx-cor", "nginx-core"}, // truncated
		{"libc", "libc6"},           // prefix of a real name
		{"sslcert", "ssl-cert"},     // one edit away
		{"zzzzzzzz", ""},            // unrelated to anything indexed
	}
	for _, c := range cases {
		got := ix.Suggest(c.query, 5)
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("Suggest(%q) should have offered nothing, got %v", c.query, got)
			}
			continue
		}
		if len(got) == 0 || got[0] != c.want {
			t.Errorf("Suggest(%q) = %v, want %q first", c.query, got, c.want)
		}
	}
}
