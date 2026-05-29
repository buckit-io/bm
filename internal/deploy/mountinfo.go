package deploy

import (
	"strconv"
	"strings"
)

type mountInfoEntry struct {
	MountPoint string
	Source     string
	FSType     string
}

// parseMountInfo parses /proc/self/mountinfo into the mountpoint, source and
// filesystem type the wizard needs. The kernel escapes whitespace as octal
// sequences (e.g. \040), so we unescape those fields before returning.
func parseMountInfo(body string) []mountInfoEntry {
	var out []mountInfoEntry
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		leftFields := strings.Fields(left)
		rightFields := strings.Fields(right)
		if len(leftFields) < 5 || len(rightFields) < 3 {
			continue
		}
		out = append(out, mountInfoEntry{
			MountPoint: unescapeMountInfoField(leftFields[4]),
			Source:     unescapeMountInfoField(rightFields[1]),
			FSType:     rightFields[0],
		})
	}
	return out
}

// parseMountSizes parses `df --output=target,size` output into a mountpoint ->
// sizeBytes map. It is intentionally permissive: any line whose last column
// parses as an integer is accepted.
func parseMountSizes(body string) map[string]int64 {
	out := make(map[string]int64)
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		size, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		if err != nil {
			continue
		}
		mountPoint := strings.Join(fields[:len(fields)-1], " ")
		out[mountPoint] = size
	}
	return out
}

func unescapeMountInfoField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	replacer := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	)
	return replacer.Replace(s)
}
