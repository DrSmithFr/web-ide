package install

import "testing"

func TestConstraints(t *testing.T) {
	for _, c := range []struct {
		constraint string
		yes, no    []string
	}{
		{"^1.2", []string{"1.2.0", "1.9.9"}, []string{"1.1.9", "2.0.0", "2.0.0-rc1"}},
		{"^0.3", []string{"0.3.0", "0.3.7"}, []string{"0.4.0", "0.2.9"}},
		{"^0.0.3", []string{"0.0.3"}, []string{"0.0.4"}},
		{"~1.2", []string{"1.2.0", "1.2.9"}, []string{"1.3.0"}},
		{"~1", []string{"1.0.0", "1.9.0"}, []string{"2.0.0"}},
		{">=1.0 <2", []string{"1.0.0", "1.5.0"}, []string{"0.9.0", "2.0.0"}},
		{">1.0.0, <=1.2.0", []string{"1.0.1", "1.2.0"}, []string{"1.0.0", "1.2.1"}},
		{"=1.2.3", []string{"1.2.3"}, []string{"1.2.4"}},
		{"1.2", []string{"1.2.0", "1.2.5"}, []string{"1.3.0"}},
		{"*", []string{"0.0.1", "9.9.9"}, []string{"1.0.0-beta"}},
		{"", []string{"1.0.0"}, nil},
		{"^1 || ^3", []string{"1.4.0", "3.0.0"}, []string{"2.0.0"}},
		{"=2.0.0-rc1", []string{"2.0.0-rc1"}, []string{"2.0.0"}},
	} {
		k, err := ParseConstraint(c.constraint)
		if err != nil {
			t.Fatalf("%q: %v", c.constraint, err)
		}
		for _, s := range c.yes {
			if v, _ := ParseVersion(s); !k.Allows(v) {
				t.Errorf("%q should allow %s", c.constraint, s)
			}
		}
		for _, s := range c.no {
			if v, _ := ParseVersion(s); k.Allows(v) {
				t.Errorf("%q should not allow %s", c.constraint, s)
			}
		}
	}
	for _, bad := range []string{"^x", ">=1.2.3.4", "1 ||", "01.2"} {
		if _, err := ParseConstraint(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestVersions(t *testing.T) {
	a, _ := ParseVersion("v1.10.0")
	b, _ := ParseVersion("1.9.3")
	rc, _ := ParseVersion("1.10.0-rc1")
	if a.Compare(b) != 1 || b.Compare(a) != -1 || rc.Compare(a) != -1 || a.String() != "1.10.0" {
		t.Fatal("compare")
	}
	if _, err := ParseVersion("1.2"); err == nil {
		t.Fatal("a version has three numbers")
	}
	for _, bad := range []string{"1.0.0-../../x", "1.0.0-a/b", "1.0.0-", "1.0.0-a..b"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q: a version names a folder", bad)
		}
	}
}
