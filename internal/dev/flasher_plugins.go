package dev

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A default plugin is upgraded only when its installed code still matches the
// previous defaults. Deleted and edited plugins remain the user's choice.
func refreshBuiltinPlugins(oldRoot, newRoot, oldData, newData, prefix, temp string) error {
	const defaults = "/usr/share/microstick/default-plugins"
	if !imageExists(oldRoot, defaults) {
		return nil
	}
	plugins, e := imageChildren(oldRoot, defaults)
	if e != nil {
		return e
	}
	for _, plugin := range plugins {
		if plugin.Mode&0170000 != 0040000 {
			continue
		}
		id := filepath.Base(plugin.Path)
		installed := prefix + "/" + id
		if !imageExists(oldData, installed) || !imageExists(newRoot, defaults+"/"+id) {
			continue
		}
		canonical, e := pluginFingerprint(oldRoot, plugin.Path)
		if e != nil {
			return e
		}
		current, e := pluginFingerprint(oldData, installed)
		if e != nil {
			return e
		}
		if !bytes.Equal(canonical, current) {
			continue
		}
		if e = migrateImageRoots(newRoot, newData, temp, map[string]string{defaults + "/" + id: installed}, false); e != nil {
			return e
		}
		state := installed + "/.state"
		if imageExists(oldData, state) {
			if e = migrateImageRoots(oldData, newData, temp, map[string]string{state: state}, false); e != nil {
				return e
			}
		}
	}
	return nil
}
func pluginFingerprint(image, root string) ([]byte, error) {
	var result bytes.Buffer
	nodes := 0
	var visit func(string, int) error
	visit = func(path string, depth int) error {
		nodes++
		if nodes > 10000 || depth > 64 {
			return errors.New("plugin traversal limit")
		}
		children, e := imageChildren(image, path)
		if e != nil {
			return e
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Path < children[j].Path })
		for _, n := range children {
			if filepath.Base(n.Path) == ".state" {
				continue
			}
			relative := strings.TrimPrefix(n.Path, root)
			fmt.Fprintf(&result, "%s:%o:%d:%d\n", relative, n.Mode, n.UID, n.GID)
			switch n.Mode & 0170000 {
			case 0040000:
				if e = visit(n.Path, depth+1); e != nil {
					return e
				}
			case 0100000:
				b, e := debugImage(image, fmt.Sprintf("cat <%d>", n.Inode), false)
				if e != nil {
					return e
				}
				result.WriteString(digest(b))
				result.WriteByte('\n')
			case 0120000:
				stat, e := imageStat(image, n.Path)
				if e != nil {
					return e
				}
				link := regexp.MustCompile(`Fast link dest: "(.*)"`).FindSubmatch(stat)
				if link != nil {
					result.Write(link[1])
				} else {
					b, e := debugImage(image, fmt.Sprintf("cat <%d>", n.Inode), false)
					if e != nil {
						return e
					}
					result.Write(b)
				}
				result.WriteByte('\n')
			default:
				return errors.New("unsupported plugin inode")
			}
		}
		return nil
	}
	if e := visit(root, 0); e != nil {
		return nil, e
	}
	return result.Bytes(), nil
}

func stagePluginRefresh(oldRoot, newRoot, oldData, temp string, refresh bool) error {
	var deleted strings.Builder
	const defaults = "/usr/share/microstick/default-plugins"
	if !imageExists(oldRoot, defaults) {
		return nil
	}
	plugins, e := imageChildren(oldRoot, defaults)
	if e != nil {
		return e
	}
	for _, p := range plugins {
		if p.Mode&0170000 != 0040000 {
			continue
		}
		id := filepath.Base(p.Path)
		current := "/plugins/" + id
		if !imageExists(oldData, current) {
			deleted.WriteString(id + "\n")
			continue
		}
		if !refresh || !imageExists(newRoot, defaults+"/"+id) {
			continue
		}
		original, e := pluginFingerprint(oldRoot, p.Path)
		if e != nil {
			return e
		}
		actual, e := pluginFingerprint(oldData, current)
		if e != nil {
			return e
		}
		if !bytes.Equal(original, actual) {
			continue
		}
		if e = migrateImageRoots(newRoot, newRoot, temp, map[string]string{defaults + "/" + id: "/usr/share/microstick/data-update/plugins/" + id}, false); e != nil {
			return e
		}
	}
	local := filepath.Join(temp, "deleted-default-plugins")
	if e = os.WriteFile(local, []byte(deleted.String()), 0600); e != nil {
		return e
	}
	if e = imagePut(newRoot, "/etc/microstick/deleted-default-plugins", local, 0100600); e != nil {
		return e
	}

	return nil
}
