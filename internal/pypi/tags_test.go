package pypi

import "testing"

func target() Target {
	t, _ := ParseTarget("3.12", "2.39", "x86_64")
	return t
}

func mustWheel(t *testing.T, name string) Wheel {
	t.Helper()
	w, ok := ParseWheelName(name)
	if !ok {
		t.Fatalf("cannot parse %q", name)
	}
	return w
}

func TestIncompatibleWheelsRejected(t *testing.T) {
	tg := target()
	for _, name := range []string{
		"numpy-2.1.0-cp311-cp311-manylinux_2_17_x86_64.whl",
		"numpy-2.1.0-cp312-cp312-musllinux_1_2_x86_64.whl",
		"numpy-2.1.0-cp312-cp312-manylinux_2_17_aarch64.whl",
		"numpy-2.1.0-cp312-cp312-win_amd64.whl",
		"numpy-2.1.0-cp312-cp312-macosx_11_0_arm64.whl",
		"numpy-2.1.0-cp313-cp313-manylinux_2_17_x86_64.whl",
		"old-1.0-py2-none-any.whl",
		"toonew-1.0-cp312-cp312-manylinux_2_50_x86_64.whl",
	} {
		if _, ok := tg.Score(mustWheel(t, name)); ok {
			t.Errorf("%s should be rejected for cp312/glibc2.39/x86_64", name)
		}
	}
}

func TestCompatibleWheelPreference(t *testing.T) {
	tg := target()
	// Listed worst to best.
	order := []string{
		"pkg-1.0-py3-none-any.whl",
		"pkg-1.0-py312-none-any.whl",
		"pkg-1.0-cp312-none-any.whl",
		"pkg-1.0-cp312-cp312-linux_x86_64.whl",
		"pkg-1.0-cp310-abi3-manylinux2014_x86_64.whl",
		"pkg-1.0-cp312-cp312-manylinux2014_x86_64.whl",
		"pkg-1.0-cp312-cp312-manylinux_2_28_x86_64.whl",
		"pkg-1.0-cp312-cp312-manylinux_2_39_x86_64.whl",
	}
	prev := -1
	for _, name := range order {
		s, ok := tg.Score(mustWheel(t, name))
		if !ok {
			t.Fatalf("%s should be compatible", name)
		}
		if s <= prev {
			t.Errorf("%s scored %d, expected more than %d", name, s, prev)
		}
		prev = s
	}
}

func TestAbi3PrefersNewerInterpreter(t *testing.T) {
	tg := target()
	older, _ := tg.Score(mustWheel(t, "pkg-1.0-cp38-abi3-manylinux_2_28_x86_64.whl"))
	newer, _ := tg.Score(mustWheel(t, "pkg-1.0-cp311-abi3-manylinux_2_28_x86_64.whl"))
	if newer <= older {
		t.Errorf("cp311-abi3 (%d) should outrank cp38-abi3 (%d)", newer, older)
	}
}

func TestCompoundTagsExpanded(t *testing.T) {
	tg := target()
	if _, ok := tg.Score(mustWheel(t, "pkg-1.0-py2.py3-none-any.whl")); !ok {
		t.Error("py2.py3-none-any must be accepted via its py3 tag")
	}
}

func TestParseWheelNameWithBuildTag(t *testing.T) {
	w := mustWheel(t, "pkg-1.0-1234-cp312-cp312-manylinux_2_39_x86_64.whl")
	if w.Build != "1234" || w.Version != "1.0" {
		t.Fatalf("unexpected parse: %+v", w)
	}
}

func TestUVPlatform(t *testing.T) {
	tg, err := ParseTarget("3.12", "2.39", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := tg.UVPlatform(); got != "x86_64-manylinux_2_39" {
		t.Errorf("got %s", got)
	}
}
