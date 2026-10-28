// Package resource reads Linux memory accounting without guessing host capacity.
package resource

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

type Memory struct {
	Source      string
	Used, Limit uint64
}

func read(f fs.FS, name string) (string, error) {
	file, err := f.Open(strings.TrimPrefix(name, "/"))
	if err != nil {
		return "", err
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if len(b) > 1<<20 {
		return "", fmt.Errorf("resource: cgroup metadata too large")
	}
	return strings.TrimSpace(string(b)), err
}
func unescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// CgroupMemory selects the most pressured finite limit along the visible
// membership hierarchy, using usage and limit from the same cgroup. Ancestor
// accounting can include sibling processes. Hidden ancestors cannot be inferred.
func CgroupMemory(f fs.FS) (Memory, bool, error) {
	membership, err := read(f, "/proc/self/cgroup")
	if errors.Is(err, fs.ErrNotExist) {
		return Memory{}, false, nil
	}
	if err != nil {
		return Memory{}, false, err
	}
	mounts, err := read(f, "/proc/self/mountinfo")
	if err != nil {
		return Memory{}, false, err
	}
	v2, v1 := "", ""
	for _, line := range strings.Split(membership, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] == "" {
			v2 = parts[2]
		}
		for _, c := range strings.Split(parts[1], ",") {
			if c == "memory" {
				v1 = parts[2]
			}
		}
	}
	var best Memory
	found := false
	scanner := bufio.NewScanner(strings.NewReader(mounts))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), " - ", 2)
		if len(parts) != 2 {
			continue
		}
		left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(left) < 5 || len(right) < 3 {
			continue
		}
		group, limitFile, usageFile, source := "", "", "", ""
		if right[0] == "cgroup2" && v2 != "" {
			group = v2
			limitFile = "memory.max"
			usageFile = "memory.current"
			source = "cgroup_v2"
		}
		if right[0] == "cgroup" && v1 != "" {
			for _, c := range strings.Split(right[2], ",") {
				if c == "memory" {
					group = v1
					limitFile = "memory.limit_in_bytes"
					usageFile = "memory.usage_in_bytes"
					source = "cgroup_v1"
				}
			}
		}
		if group == "" {
			continue
		}
		root, mount := path.Clean(unescape(left[3])), path.Clean(unescape(left[4]))
		group = path.Clean(group)
		relative := ""
		if group == root {
			relative = ""
		} else if root == "/" {
			relative = strings.TrimPrefix(group, "/")
		} else if strings.HasPrefix(group, root+"/") {
			relative = strings.TrimPrefix(group, root+"/")
		} else if group == "/" {
			relative = ""
		} else {
			continue
		}
		current := path.Join(mount, relative)
		for n := 0; n < 256; n++ {
			if n == 255 && current != mount {
				return Memory{}, false, fmt.Errorf("resource: cgroup hierarchy too deep")
			}
			raw, err := read(f, path.Join(current, limitFile))
			if errors.Is(err, fs.ErrNotExist) {
				if current == mount {
					break
				}
			} else if err != nil {
				return Memory{}, false, err
			} else if raw != "max" {
				limit, err := strconv.ParseUint(raw, 10, 64)
				if err != nil || limit == 0 {
					return Memory{}, false, fmt.Errorf("resource: invalid memory limit")
				}
				if source != "cgroup_v1" || limit < 1<<60 {
					raw, err := read(f, path.Join(current, usageFile))
					if err != nil {
						return Memory{}, false, err
					}
					used, err := strconv.ParseUint(raw, 10, 64)
					if err != nil {
						return Memory{}, false, fmt.Errorf("resource: invalid memory usage")
					}
					if !found || float64(used)/float64(limit) > float64(best.Used)/float64(best.Limit) {
						best = Memory{source, used, limit}
						found = true
					}
				}
			}
			if current == mount {
				break
			}
			parent := path.Dir(current)
			if parent == current || !strings.HasPrefix(parent+"/", mount+"/") {
				return Memory{}, false, fmt.Errorf("resource: cgroup mount escape")
			}
			current = parent
		}
	}
	if err := scanner.Err(); err != nil {
		return Memory{}, false, err
	}
	return best, found, nil
}
