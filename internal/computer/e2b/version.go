package e2b

import (
	"strconv"
	"strings"
)

// VersionBelow reports whether TOFI version v is older than min. Versions look
// like v0.1.5 or v0.1.5-rc.3 (a release candidate is older than its release). A
// version that cannot be read is below any minimum, except when it equals it:
// the check fails closed. An empty min is never above anything.
func VersionBelow(v, min string) bool {
	if min == "" {
		return false
	}
	if v == min {
		return false
	}
	a, aok := parseVersion(v)
	b, bok := parseVersion(min)
	if !aok || !bok {
		return true
	}
	for i := 0; i < 3; i++ {
		if a.core[i] != b.core[i] {
			return a.core[i] < b.core[i]
		}
	}
	switch {
	case a.pre == "" && b.pre == "":
		return false
	case a.pre == "":
		return false // a release is newer than its candidate
	case b.pre == "":
		return true
	}
	return comparePre(a.pre, b.pre) < 0
}

type semver struct {
	core [3]int
	pre  string
}

func parseVersion(v string) (semver, bool) {
	var out semver
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		out.pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out.core[i] = n
	}
	return out, true
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, xerr := strconv.Atoi(as[i])
		y, yerr := strconv.Atoi(bs[i])
		switch {
		case xerr == nil && yerr == nil:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return len(as) - len(bs)
}
