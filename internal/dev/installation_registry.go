package dev

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func installationNumbers(roots ...string) (map[string]bool, error) {
	used := map[string]bool{}
	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("installation registry contains a symlink")
			}
			if d.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			var state Installation
			if e = json.Unmarshal(data, &state); e != nil {
				return errors.New("malformed local installation record")
			}
			if state.IMEI != "" {
				if !ValidIMEI(state.IMEI) {
					return errors.New("invalid local installation record")
				}
				used[state.IMEI] = true
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	return used, nil
}
func uniqueIMEI(tac string, used map[string]bool, generate func(string) (string, error)) (string, error) {
	for i := 0; i < 256; i++ {
		value, e := generate(tac)
		if e != nil {
			return "", e
		}
		if !used[value] {
			return value, nil
		}
	}
	return "", errors.New("cannot reserve an unused local installation identity")
}

type installationReservations struct {
	Version int               `json:"schema_version"`
	Records map[string]string `json:"records"`
}

func writeJSONAtomic(path string, value any) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".record-")
	if e != nil {
		return e
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	if e = writeJSON(name, value, 0600); e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
