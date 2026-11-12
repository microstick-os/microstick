package dev

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var errImageMissing = errors.New("ext4 path does not exist")

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// debugImage captures diagnostics privately: native tools sometimes return zero
// even after an error. Callers verify actual inode/content/metadata readback.
func debugImage(image, request string, write bool) ([]byte, error) {
	args := []string{}
	if write {
		args = append(args, "-w")
	}
	args = append(args, "-R", request, image)
	c := exec.Command("debugfs", args...)
	var errout bytes.Buffer
	c.Stderr = &errout
	b, e := c.Output()
	if e != nil {
		return nil, errors.New("native ext4 operation failed")
	}
	s := errout.String()
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for _, line := range lines {
		if line != "" && !strings.HasPrefix(line, "debugfs ") {
			if strings.Contains(line, "File not found by ext2_lookup") {
				return nil, errImageMissing
			}
			return nil, errors.New("native ext4 operation rejected")
		}
	}
	return b, nil
}
func dq(s string) (string, error) {
	if strings.ContainsAny(s, "\x00\n\r") || len(s) > 4096 {
		return "", errors.New("unsupported filesystem name")
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, nil
}

type fsNode struct {
	Inode          uint64
	Mode, UID, GID uint64
	Size           uint64
	Path           string
}

var inodeRE = regexp.MustCompile(`Inode:\s+(\d+)`)
var modeRE = regexp.MustCompile(`Mode:\s+0([0-7]+)`)
var ownerRE = regexp.MustCompile(`User:\s+(\d+)\s+Group:\s+(\d+)`)

func imageStat(image, path string) ([]byte, error) {
	q, e := dq(path)
	if e != nil {
		return nil, e
	}
	return debugImage(image, "stat "+q, false)
}
func imageExists(image, path string) bool {
	b, e := imageStat(image, path)
	return e == nil && inodeRE.Match(b)
}
func imageChildren(image, path string) ([]fsNode, error) {
	q, e := dq(path)
	if e != nil {
		return nil, e
	}
	b, e := debugImage(image, "ls -p "+q, false)
	if e != nil {
		return nil, e
	}
	out := []fsNode{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		p := strings.Split(line, "/")
		if len(p) < 7 || p[0] != "" {
			return nil, errors.New("unsupported directory entry")
		}
		if p[1] == "0" || p[5] == "." || p[5] == ".." {
			continue
		}
		vals := make([]uint64, 4)
		for i, s := range p[1:5] {
			base := 10
			if i == 1 {
				base = 8
			}
			n, e := strconv.ParseUint(s, base, 64)
			if e != nil {
				return nil, e
			}
			vals[i] = n
		}
		if strings.ContainsAny(p[5], "\x00\n\r") || p[5] == "" {
			return nil, errors.New("unsafe migration name")
		}
		n := fsNode{Inode: vals[0], Mode: vals[1], UID: vals[2], GID: vals[3], Path: strings.TrimSuffix(path, "/") + "/" + p[5]}
		if p[6] != "" {
			n.Size, e = strconv.ParseUint(p[6], 10, 64)
			if e != nil {
				return nil, e
			}
		}
		out = append(out, n)
	}
	return out, nil
}
func removeImageTree(image, path string, depth int) error {
	if depth > 128 {
		return errors.New("migration depth limit")
	}
	if !imageExists(image, path) {
		return nil
	}
	b, e := imageStat(image, path)
	if e != nil {
		return e
	}
	q, e := dq(path)
	if e != nil {
		return e
	}
	if bytes.Contains(b, []byte("Type: directory")) {
		children, e := imageChildren(image, path)
		if e != nil {
			return e
		}
		for _, c := range children {
			if e = removeImageTree(image, c.Path, depth+1); e != nil {
				return e
			}
		}
		_, e = debugImage(image, "rmdir "+q, true)
		return e
	}
	_, e = debugImage(image, "rm "+q, true)
	return e
}
func imagePut(image, path, local string, mode int) error {
	if e := removeImageTree(image, path, 0); e != nil {
		return e
	}
	p, e := dq(path)
	if e != nil {
		return e
	}
	l, e := dq(local)
	if e != nil {
		return e
	}
	if _, e = debugImage(image, "write "+l+" "+p, true); e != nil {
		return e
	}
	for _, field := range []string{fmt.Sprintf("mode 0%o", mode), "uid 0", "gid 0"} {
		if _, e = debugImage(image, "set_inode_field "+p+" "+field, true); e != nil {
			return e
		}
	}
	expected, e := os.ReadFile(local)
	if e != nil {
		return e
	}
	b, e := debugImage(image, "cat "+p, false)
	if e != nil {
		return e
	}
	if !bytes.Equal(expected, b) {
		return errors.New("ext4 content readback mismatch")
	}
	return nil
}

var attrRE = regexp.MustCompile(`^\s+([A-Za-z0-9_.-]+) \([0-9]+\)`)

func copyAttributes(src, dst, spec, path, dir string) error {
	b, e := debugImage(src, "ea_list "+spec, false)
	if e != nil {
		return e
	}
	q, e := dq(path)
	if e != nil {
		return e
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" || strings.TrimSpace(line) == "Extended attributes:" {
			continue
		}
		m := attrRE.FindStringSubmatch(line)
		if m == nil {
			return errors.New("unsupported extended attribute")
		}
		p := filepath.Join(dir, "attribute")
		l, _ := dq(p)
		if _, e = debugImage(src, "ea_get -f "+l+" "+spec+" "+m[1], false); e != nil {
			return e
		}
		want, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if _, e = debugImage(dst, "ea_set -f "+l+" "+q+" "+m[1], true); e != nil {
			return e
		}
		if _, e = debugImage(dst, "ea_get -f "+l+" "+q+" "+m[1], false); e != nil {
			return e
		}
		got, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(want, got) {
			return errors.New("extended attribute readback mismatch")
		}
		os.Remove(p)
	}
	return nil
}

// MigrateUserData copies only the documented durable roots. It never imports
// passwd/group/shadow. Source is opened read-only, and unsupported inode types
// or metadata fail the staged preparation instead of silently dropping data.
func MigrateUserData(src, dst, temp string) error {
	roots := map[string]string{}
	for _, path := range []string{"/etc/microstick", "/data", "/var/lib/microstick", "/etc/dropbear", "/root/.ssh"} {
		roots[path] = path
	}
	return migrateImageRoots(src, dst, temp, roots, true)
}
func migrateImageRoots(src, dst, temp string, roots map[string]string, requireMicrostick bool) error {
	if _, e := command("", nil, "e2fsck", "-fn", src); e != nil {
		return errors.New("old rootfs is not clean; migration requires a reliable source")
	}
	if requireMicrostick {
		marker, e := imageBytes(src, "/etc/passwd")
		if e != nil || !containsMicrostick(marker) {
			return errors.New("migration source is not a known microstick rootfs")
		}
	}
	var e error
	var sourcePrefix, targetPrefix string
	seen := map[uint64]string{}
	links := map[uint64]int{}
	nodes := 0
	var copyNode func(fsNode, int) error
	copyNode = func(n fsNode, depth int) error {
		originalPath := n.Path
		n.Path = targetPrefix + strings.TrimPrefix(n.Path, sourcePrefix)
		nodes++
		if depth > 128 || nodes > 100000 {
			return errors.New("migration traversal limit")
		}
		kind := n.Mode & 0170000
		if kind != 0100000 && kind != 0040000 && kind != 0120000 {
			return errors.New("unsupported persistent inode type")
		}
		spec := fmt.Sprintf("<%d>", n.Inode)
		path, e := dq(n.Path)
		if e != nil {
			return e
		}
		stat, e := debugImage(src, "stat "+spec, false)
		if e != nil {
			return e
		}
		if bytes.Contains(stat, []byte("Flags: 0x")) {
			flagRE := regexp.MustCompile(`Flags: 0x([0-9a-fA-F]+)`)
			m := flagRE.FindSubmatch(stat)
			if m != nil {
				flags, _ := strconv.ParseUint(string(m[1]), 16, 64)
				if flags&^uint64(0x80000) != 0 {
					return errors.New("unsupported persistent inode flags")
				}
			}
		}
		if kind == 0040000 {
			if _, e = debugImage(dst, "mkdir "+path, true); e != nil {
				return e
			}
			children, e := imageChildren(src, originalPath)
			if e != nil {
				return e
			}
			for _, child := range children {
				if e = copyNode(child, depth+1); e != nil {
					return e
				}
			}
		} else if old, ok := seen[n.Inode]; ok {
			q, _ := dq(old)
			if _, e = debugImage(dst, "ln "+q+" "+path, true); e != nil {
				return e
			}
			links[n.Inode]++
			if _, e = debugImage(dst, fmt.Sprintf("set_inode_field %s links_count %d", q, links[n.Inode]), true); e != nil {
				return e
			}
			return nil
		} else if kind == 0120000 {
			m := regexp.MustCompile(`Fast link dest: "(.*)"`).FindSubmatch(stat)
			var target string
			if m != nil {
				target = string(m[1])
			} else {
				b, e := debugImage(src, "cat "+spec, false)
				if e != nil {
					return e
				}
				target = string(b)
			}
			q, e := dq(target)
			if e != nil {
				return e
			}
			if _, e = debugImage(dst, "symlink "+path+" "+q, true); e != nil {
				return e
			}
		} else {
			local := filepath.Join(temp, "migration-file")
			q, _ := dq(local)
			if _, e = debugImage(src, "dump "+spec+" "+q, false); e != nil {
				return e
			}
			st, e := os.Stat(local)
			if e != nil || uint64(st.Size()) != n.Size {
				return errors.New("migration file size mismatch")
			}
			want, e := hashFile(local)
			if e != nil {
				return e
			}
			if _, e = debugImage(dst, "write "+q+" "+path, true); e != nil {
				return e
			}
			b, e := debugImage(dst, "cat "+path, false)
			if e != nil {
				return e
			}
			h := sha256.Sum256(b)
			if want != hex.EncodeToString(h[:]) {
				return errors.New("migration file readback mismatch")
			}
			os.Remove(local)
		}
		if kind != 0040000 {
			seen[n.Inode] = n.Path
			links[n.Inode] = 1
		}
		for _, field := range []string{fmt.Sprintf("mode 0%o", n.Mode), fmt.Sprintf("uid %d", n.UID), fmt.Sprintf("gid %d", n.GID)} {
			if _, e = debugImage(dst, "set_inode_field "+path+" "+field, true); e != nil {
				return e
			}
		}
		// debugfs's @ prefix accepts exact seconds; preserve ext4 extra timestamp
		// bits as separate fields when present in a large inode.
		for _, field := range []string{"atime", "mtime", "ctime", "crtime"} {
			m := regexp.MustCompile(`(?m)^\s*` + field + `:\s+0x([0-9a-fA-F]+)(?::([0-9a-fA-F]+))?`).FindSubmatch(stat)
			if m == nil {
				continue
			}
			seconds, e := strconv.ParseUint(string(m[1]), 16, 32)
			if e != nil {
				return e
			}
			if _, e = debugImage(dst, fmt.Sprintf("set_inode_field %s %s @%d", path, field, seconds), true); e != nil {
				return e
			}
			if len(m[2]) > 0 {
				extra, _ := strconv.ParseUint(string(m[2]), 16, 32)
				if _, e = debugImage(dst, fmt.Sprintf("set_inode_field %s %s_extra %d", path, field, extra), true); e != nil {
					return e
				}
			}
		}
		if e = copyAttributes(src, dst, spec, n.Path, temp); e != nil {
			return e
		}
		actual, e := imageStat(dst, n.Path)
		if e != nil {
			return e
		}
		mode := modeRE.FindSubmatch(actual)
		owner := ownerRE.FindSubmatch(actual)
		if mode == nil || owner == nil {
			return errors.New("migration metadata missing")
		}
		m, _ := strconv.ParseUint(string(mode[1]), 8, 64)
		u, _ := strconv.ParseUint(string(owner[1]), 10, 64)
		g, _ := strconv.ParseUint(string(owner[2]), 10, 64)
		if m != n.Mode&07777 || u != n.UID || g != n.GID {
			return errors.New("migration metadata readback mismatch")
		}
		return nil
	}
	for _, root := range sortedKeys(roots) {
		sourcePrefix, targetPrefix = root, roots[root]
		if b, lookupErr := imageStat(src, root); lookupErr != nil {
			if errors.Is(lookupErr, errImageMissing) {
				continue
			}
			return lookupErr
		} else if !inodeRE.Match(b) {
			return errors.New("persistent root lookup did not return an inode")
		}
		parent := filepath.Dir(root)
		parts := strings.Split(strings.TrimPrefix(filepath.Dir(targetPrefix), "/"), "/")
		current := ""
		for _, p := range parts {
			current += "/" + p
			if !imageExists(dst, current) {
				q, _ := dq(current)
				if _, e = debugImage(dst, "mkdir "+q, true); e != nil {
					return e
				}
			}
		}
		children, e := imageChildren(src, parent)
		if e != nil {
			return e
		}
		found := false
		for _, n := range children {
			if n.Path == root {
				found = true
				if n.Mode&0170000 != 0040000 {
					return errors.New("persistent root must be a directory")
				}
				if e = removeImageTree(dst, targetPrefix, 0); e != nil {
					return e
				}
				if e = copyNode(n, 0); e != nil {
					return e
				}
				break
			}
		}
		if !found {
			return errors.New("persistent root traversal failed")
		}
	}
	if _, e = command("", nil, "e2fsck", "-fn", dst); e != nil {
		return errors.New("migrated rootfs failed verification")
	}
	return nil
}
