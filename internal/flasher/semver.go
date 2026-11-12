package flasher

import (
	"errors"
	"regexp"
	"strings"
)

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func compareVersions(a, b string) (int, error) {
	parse := func(s string) ([]string, error) {
		m := semverPattern.FindStringSubmatch(s)
		if m == nil {
			return nil, errors.New("invalid semantic version: " + s)
		}
		for _, part := range strings.Split(m[4], ".") {
			if numericIdentifier(part) && len(part) > 1 && part[0] == '0' {
				return nil, errors.New("invalid numeric prerelease")
			}
		}
		return m, nil
	}
	x, e := parse(a)
	if e != nil {
		return 0, e
	}
	y, e := parse(b)
	if e != nil {
		return 0, e
	}
	numeric := func(a, b string) int {
		if len(a) < len(b) {
			return -1
		}
		if len(a) > len(b) {
			return 1
		}
		return strings.Compare(a, b)
	}
	for i := 1; i <= 3; i++ {
		if c := numeric(x[i], y[i]); c != 0 {
			return c, nil
		}
	}
	if x[4] == y[4] {
		return 0, nil
	}
	if x[4] == "" {
		return 1, nil
	}
	if y[4] == "" {
		return -1, nil
	}
	xs, ys := strings.Split(x[4], "."), strings.Split(y[4], ".")
	for i := 0; i < len(xs) && i < len(ys); i++ {
		xn, yn := numericIdentifier(xs[i]), numericIdentifier(ys[i])
		var c int
		switch {
		case xn && yn:
			c = numeric(xs[i], ys[i])
		case xn:
			c = -1
		case yn:
			c = 1
		default:
			c = strings.Compare(xs[i], ys[i])
		}
		if c != 0 {
			return c, nil
		}
	}
	if len(xs) < len(ys) {
		return -1, nil
	}
	return 1, nil
}

func CheckEngineReleaseVersion(version string) error {
	c, e := compareVersions(version, EngineVersion)
	if e != nil {
		return e
	}
	if c < 0 {
		return errors.New("release version is below the implemented flasher compatibility version")
	}
	return nil
}

func numericIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
