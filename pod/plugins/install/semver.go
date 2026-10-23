package install

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// preRe is a pre-release: semver's identifiers (a version also names a folder: no / nor ..).
var preRe = regexp.MustCompile(`^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$`)

// Version is a semantic version (major.minor.patch). A pre-release (v1.2.0-rc1) is parsed but
// never picked by a resolution.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion reads "1.2.3" or "v1.2.3", with an optional "-pre".
func ParseVersion(s string) (Version, error) {
	v, n, err := parsePartial(s)
	if err == nil && n != 3 {
		err = fmt.Errorf("version %q: major.minor.patch", s)
	}
	return v, err
}

// parsePartial reads a version of one to three numbers (1, 1.2, 1.2.3), n being how many.
func parsePartial(s string) (v Version, n int, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+") // build metadata is ignored
	var hasPre bool
	s, v.Pre, hasPre = strings.Cut(s, "-")
	if hasPre && !preRe.MatchString(v.Pre) {
		return v, 0, fmt.Errorf("version %q", s)
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, 0, fmt.Errorf("version %q", s)
	}
	nums := [3]int{}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || (len(p) > 1 && p[0] == '0') {
			return v, 0, fmt.Errorf("version %q", s)
		}
		nums[i] = x
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, len(parts), nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare is -1, 0 or 1; a pre-release comes before its release.
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	case v.Pre < o.Pre:
		return -1
	}
	return 1
}

// Constraint is a set of versions, as Composer and npm write it: alternatives joined by ||,
// each the conjunction of terms separated by spaces or commas: ^1.2 (>=1.2.0 <2.0.0; ^0.3:
// <0.4.0), ~1.2 (>=1.2.0 <1.3.0; ~1: <2.0.0), >=, >, <=, <, =, a bare version (exact), *.
type Constraint struct {
	src  string
	alts [][]term
}

type term struct {
	op string // >= > <= < =
	v  Version
}

// ParseConstraint reads a constraint; "" is "*".
func ParseConstraint(s string) (Constraint, error) {
	c := Constraint{src: strings.TrimSpace(s)}
	if c.src == "" {
		c.src = "*"
	}
	for _, alt := range strings.Split(c.src, "||") {
		var terms []term
		for _, f := range strings.FieldsFunc(alt, func(r rune) bool { return r == ' ' || r == ',' }) {
			t, err := parseTerm(f)
			if err != nil {
				return c, fmt.Errorf("constraint %q: %v", s, err)
			}
			terms = append(terms, t...)
		}
		if len(terms) == 0 {
			return c, fmt.Errorf("constraint %q: an empty alternative", s)
		}
		c.alts = append(c.alts, terms)
	}
	return c, nil
}

func parseTerm(f string) ([]term, error) {
	if f == "*" || f == "x" {
		return []term{{op: ">=", v: Version{}}}, nil
	}
	for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if !strings.HasPrefix(f, op) {
			continue
		}
		v, n, err := parsePartial(f[len(op):])
		if err != nil {
			return nil, err
		}
		switch op {
		case "^":
			return []term{{">=", v}, {"<", caret(v, n)}}, nil
		case "~":
			return []term{{">=", v}, {"<", tilde(v, n)}}, nil
		case "=":
			return exact(v, n), nil
		}
		return []term{{op, v}}, nil
	}
	v, n, err := parsePartial(f)
	if err != nil {
		return nil, err
	}
	return exact(v, n), nil
}

// exact: 1.2.3 is that version, 1.2 any 1.2.x, 1 any 1.x.
func exact(v Version, n int) []term {
	switch n {
	case 3:
		return []term{{"=", v}}
	case 2:
		return []term{{">=", v}, {"<", Version{Major: v.Major, Minor: v.Minor + 1}}}
	}
	return []term{{">=", v}, {"<", Version{Major: v.Major + 1}}}
}

func caret(v Version, n int) Version {
	switch {
	case v.Major > 0 || n == 1:
		return Version{Major: v.Major + 1}
	case v.Minor > 0 || n == 2:
		return Version{Minor: v.Minor + 1}
	}
	return Version{Patch: v.Patch + 1}
}

func tilde(v Version, n int) Version {
	if n == 1 {
		return Version{Major: v.Major + 1}
	}
	return Version{Major: v.Major, Minor: v.Minor + 1}
}

func (c Constraint) String() string { return c.src }

// Allows tells whether v is in the set. A pre-release is allowed only when a term names it.
func (c Constraint) Allows(v Version) bool {
	for _, alt := range c.alts {
		ok := true
		named := false
		for _, t := range alt {
			named = named || (t.v.Pre != "" && t.v.Major == v.Major && t.v.Minor == v.Minor && t.v.Patch == v.Patch)
			cmp := v.Compare(t.v)
			switch t.op {
			case ">=":
				ok = ok && cmp >= 0
			case ">":
				ok = ok && cmp > 0
			case "<=":
				ok = ok && cmp <= 0
			case "<":
				ok = ok && cmp < 0
			case "=":
				ok = ok && cmp == 0
			}
		}
		if ok && (v.Pre == "" || named) {
			return true
		}
	}
	return false
}
