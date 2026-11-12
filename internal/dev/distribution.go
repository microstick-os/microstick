package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Distribution struct {
	Version  string  `json:"version"`
	Platform string  `json:"platform"`
	Files    []Entry `json:"files"`
}

// Release files are immutable inputs. Backups, receipts and own calibration
// stay in a separate writable workspace.
func OpenDistribution(root string) (Paths, error) {
	p, e := paths(root, true)
	if e != nil {
		return p, e
	}
	var d Distribution
	if e = readJSON(filepath.Join(p.Repo, "DISTRIBUTION.json"), &d); e != nil {
		return p, e
	}
	if d.Version == "" || d.Platform != runtime.GOOS+"/"+runtime.GOARCH || len(d.Files) == 0 {
		return p, errors.New("release does not support this computer")
	}
	for _, v := range d.Files {
		path, e := safeJoin(p.Repo, v.Path)
		if e != nil {
			return p, e
		}
		st, e := os.Lstat(path)
		if e != nil || !st.Mode().IsRegular() {
			return p, fmt.Errorf("release file missing or invalid: %s", v.Path)
		}
	}
	if e = verifyEntries(p.Repo, d.Files); e != nil {
		return p, e
	}
	if _, e = verifyManifest(filepath.Join(p.Repo, "images")); e != nil {
		return p, e
	}
	var hardware Manifest
	if e = readJSON(filepath.Join(p.Repo, "hardware/MANIFEST.json"), &hardware); e != nil {
		return p, e
	}
	if e = verifyEntries(filepath.Join(p.Repo, "hardware"), hardware.Artifacts); e != nil {
		return p, e
	}
	p.Distribution = p.Repo
	p.Hardware = filepath.Join(p.Repo, "hardware")
	p.Artifacts = filepath.Join(p.Repo, "images")
	return p, nil
}
