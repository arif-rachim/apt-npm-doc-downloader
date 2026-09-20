package apt

import "testing"

func TestCompareVersions(t *testing.T) {
	// Vectors cross-checked against `dpkg --compare-versions`.
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "1.1", -1},
		{"1.10", "1.9", 1},
		{"1.0~rc1", "1.0", -1},
		{"1.0~rc1", "1.0~rc2", -1},
		{"1:1.0", "2.0", 1},
		{"1.0-1", "1.0-2", -1},
		{"1.0-1ubuntu1", "1.0-1ubuntu2", -1},
		{"2.4.7-1ubuntu1", "2.4.7-1", 1},
		{"1.0+git20240101", "1.0", 1},
		{"0.9", "1.0~beta", -1},
		{"1.0.0", "1.0", 1},
		{"1.0a", "1.0", 1},
		{"1.0-0ubuntu0.24.04.1", "1.0-0ubuntu0.24.04.2", -1},
		{"7.88.1-10ubuntu1.6", "7.88.1-10ubuntu1.10", -1},
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if sign(got) != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
		if sign(CompareVersions(c.b, c.a)) != -c.want {
			t.Errorf("CompareVersions(%q,%q) not antisymmetric", c.b, c.a)
		}
	}
}

func TestSatisfiesConstraint(t *testing.T) {
	cases := []struct {
		have, op, want string
		ok             bool
	}{
		{"1.2-3", ">=", "1.2-3", true},
		{"1.2-3", ">>", "1.2-3", false},
		{"1.2-4", ">>", "1.2-3", true},
		{"1.2-3", "<<", "1.2-4", true},
		{"1.2-3", "=", "1.2-3", true},
		{"1.2-3", "", "", true},
	}
	for _, c := range cases {
		if got := SatisfiesConstraint(c.have, c.op, c.want); got != c.ok {
			t.Errorf("Satisfies(%q %s %q) = %v", c.have, c.op, c.want, got)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
