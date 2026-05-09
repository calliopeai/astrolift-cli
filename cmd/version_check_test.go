package cmd

import "testing"

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.3.0", "1.2.99", 1},
		{"2.0.0", "1.99.99", 1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3-rc1", "1.2.3", 0},
		{"1.2.3+build", "1.2.3", 0},
	}
	for _, c := range cases {
		got, err := CompareSemver(c.a, c.b)
		if err != nil {
			t.Errorf("CompareSemver(%q, %q): unexpected error %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareSemverInvalid(t *testing.T) {
	if _, err := CompareSemver("not.a.version", "1.2.3"); err == nil {
		t.Error("expected error on non-numeric components")
	}
	if _, err := CompareSemver("1.2", "1.2.3"); err == nil {
		t.Error("expected error on missing patch component")
	}
}
