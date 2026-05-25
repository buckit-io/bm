package deploy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/buckit-io/bm/internal/domain"
)

type patternFit struct {
	fits    bool
	pattern string
}

func renderVolumes(p DeployParams) string {
	hosts := deployHosts(p.Hosts)
	paths := storagePathsForMounts(p.Topology.SelectedMounts)
	if len(hosts) == 0 || len(paths) == 0 {
		return ""
	}

	scheme := "http"
	if p.TLS.Enabled() {
		scheme = "https"
	}

	pathFit := detectNumericPattern(paths)
	if len(hosts) == 1 {
		if pathFit.fits {
			return pathFit.pattern
		}
		return strings.Join(paths, " ")
	}

	hostFit := detectNumericPattern(hosts)
	switch {
	case hostFit.fits && pathFit.fits:
		return fmt.Sprintf("%s://%s:%d%s", scheme, hostFit.pattern, p.API.Port, pathFit.pattern)
	case hostFit.fits:
		parts := make([]string, 0, len(paths))
		for _, path := range paths {
			parts = append(parts, fmt.Sprintf("%s://%s:%d%s", scheme, hostFit.pattern, p.API.Port, path))
		}
		return strings.Join(parts, " ")
	case pathFit.fits:
		parts := make([]string, 0, len(hosts))
		for _, host := range hosts {
			parts = append(parts, fmt.Sprintf("%s://%s:%d%s", scheme, host, p.API.Port, pathFit.pattern))
		}
		return strings.Join(parts, " ")
	default:
		parts := make([]string, 0, len(hosts)*len(paths))
		for _, host := range hosts {
			for _, path := range paths {
				parts = append(parts, fmt.Sprintf("%s://%s:%d%s", scheme, host, p.API.Port, path))
			}
		}
		return strings.Join(parts, " ")
	}
}

func deployHosts(hosts []domain.HostRow) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		host := strings.TrimSpace(h.Hostname)
		if host != "" {
			out = append(out, host)
		}
	}
	return out
}

func detectNumericPattern(values []string) patternFit {
	list := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			list = append(list, trimmed)
		}
	}
	if len(list) == 0 {
		return patternFit{}
	}
	if len(list) == 1 {
		return patternFit{fits: true, pattern: list[0]}
	}

	prefix := longestCommonPrefix(list)
	suffix := longestCommonSuffix(list)
	middles := make([]string, 0, len(list))
	for _, value := range list {
		if len(prefix)+len(suffix) > len(value) {
			return patternFit{}
		}
		middle := value[len(prefix) : len(value)-len(suffix)]
		if middle == "" || !allDigits(middle) {
			return patternFit{}
		}
		middles = append(middles, middle)
	}

	width := len(middles[0])
	padded := strings.HasPrefix(middles[0], "0") && width > 1
	if padded {
		for _, middle := range middles {
			if len(middle) != width {
				return patternFit{}
			}
		}
	}

	nums := make([]int, 0, len(middles))
	seen := make(map[int]struct{}, len(middles))
	for _, middle := range middles {
		n, err := strconv.Atoi(middle)
		if err != nil {
			return patternFit{}
		}
		if _, ok := seen[n]; ok {
			return patternFit{}
		}
		seen[n] = struct{}{}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for i := 1; i < len(nums); i++ {
		if nums[i] != nums[i-1]+1 {
			return patternFit{}
		}
	}

	lo := strconv.Itoa(nums[0])
	hi := strconv.Itoa(nums[len(nums)-1])
	if padded {
		lo = fmt.Sprintf("%0*d", width, nums[0])
		hi = fmt.Sprintf("%0*d", width, nums[len(nums)-1])
	}
	return patternFit{
		fits:    true,
		pattern: fmt.Sprintf("%s{%s...%s}%s", prefix, lo, hi, suffix),
	}
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for i := 1; i < len(values); i++ {
		for !strings.HasPrefix(values[i], prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

func longestCommonSuffix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	suffix := values[0]
	for i := 1; i < len(values); i++ {
		for !strings.HasSuffix(values[i], suffix) {
			suffix = suffix[1:]
			if suffix == "" {
				return ""
			}
		}
	}
	return suffix
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
