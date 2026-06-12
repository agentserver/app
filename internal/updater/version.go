package updater

import (
	"fmt"
	"strconv"
	"strings"
)

type parsedVersion struct {
	major int
	minor int
	patch int
}

func CompareVersions(a, b string) (int, error) {
	av, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{{av.major, bv.major}, {av.minor, bv.minor}, {av.patch, bv.patch}} {
		if pair[0] > pair[1] {
			return 1, nil
		}
		if pair[0] < pair[1] {
			return -1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) (parsedVersion, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return parsedVersion{}, fmt.Errorf("version %q must be MAJOR.MINOR.PATCH", v)
	}
	nums := [3]int{}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return parsedVersion{}, fmt.Errorf("version %q contains invalid component %q", v, part)
		}
		nums[i] = n
	}
	return parsedVersion{major: nums[0], minor: nums[1], patch: nums[2]}, nil
}
