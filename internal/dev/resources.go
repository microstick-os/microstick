package dev

import (
	"errors"
	"fmt"
	"path/filepath"
)

type Resource struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
	Member string `json:"member,omitempty"`
}

// resourceFile downloads and verifies an entry from the resource catalog.
func resourceFile(p Paths, id string) (string, Resource, error) {
	var catalog map[string]Resource
	if e := readJSON(filepath.Join(p.Repo, "buildroot-external/board/msm8916/resources.json"), &catalog); e != nil {
		return "", Resource{}, e
	}
	v, ok := catalog[id]
	if !ok || filepath.Base(v.File) != v.File || len(v.SHA256) != 64 {
		return "", v, errors.New("invalid microstick resource")
	}
	path := filepath.Join(p.Work, "buildroot-downloads", v.File)
	if e := Fetch(path, v.URL, v.SHA256); e != nil {
		return "", v, fmt.Errorf("microstick resource %s: %w", id, e)
	}
	return path, v, nil
}
