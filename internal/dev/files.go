package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"microstick.dev/tools/internal/flasher"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Paths struct {
	Repo, Work, External, Hardware, Output, Host, Target, Artifacts, Distribution string
	BoardProfile                                                                  *flasher.HardwareProfile
}
type Entry struct {
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
	Role   string `json:"role,omitempty"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	type plain Entry
	value := struct {
		*plain
		Source json.RawMessage `json:"source"`
	}{plain: (*plain)(e)}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if len(value.Source) > 0 {
		if err := json.Unmarshal(value.Source, &e.Source); err != nil {
			e.Source = string(value.Source)
		}
	}
	return nil
}

type Manifest struct {
	Version        int               `json:"schema_version"`
	Personalized   bool              `json:"personalized"`
	HardwareTested bool              `json:"hardware_tested"`
	Flashed        bool              `json:"flashed"`
	Device         string            `json:"device,omitempty"`
	InstallationID string            `json:"installation_id,omitempty"`
	Base           map[string]string `json:"base,omitempty"`
	Capacity       int64             `json:"rootfs_capacity_bytes,omitempty"`
	Allocated      int64             `json:"rootfs_allocated_bytes,omitempty"`
	Artifacts      []Entry           `json:"artifacts"`
}

func paths(repo string, requireWorkspace bool) (Paths, error) {
	var p Paths
	var err error
	p.Repo, err = filepath.Abs(repo)
	if err != nil {
		return p, err
	}
	p.Repo, err = filepath.EvalSymlinks(p.Repo)
	if err != nil {
		return p, err
	}
	p.External = filepath.Join(p.Repo, "buildroot-external")
	p.Work = os.Getenv("MICROSTICK_WORKSPACE")
	if p.Work == "" && !requireWorkspace {
		return p, nil
	}
	if !filepath.IsAbs(p.Work) {
		return p, errors.New("set MICROSTICK_WORKSPACE to an absolute directory outside the source repository")
	}
	p.Work = filepath.Clean(p.Work)
	if resolved, e := filepath.EvalSymlinks(p.Work); e == nil {
		p.Work = resolved
	}
	if within(p.Work, p.Repo) {
		return p, errors.New("workspace must be outside the source repository")
	}
	p.Hardware = filepath.Join(p.Work, "hardware/current")
	if hardware := os.Getenv("MICROSTICK_HARDWARE"); hardware != "" {
		if !filepath.IsAbs(hardware) {
			return p, errors.New("MICROSTICK_HARDWARE must be an absolute external bundle directory")
		}
		p.Hardware, err = privatePath(p.Repo, hardware)
		if err != nil {
			return p, err
		}
	}
	p.Output = filepath.Join(p.Work, "buildroot-output")
	p.Host = filepath.Join(p.Output, "host")
	p.Target = filepath.Join(p.Output, "target")
	p.Artifacts = filepath.Join(p.Work, "artifacts/buildroot-msm8916")
	return p, nil
}
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func safeJoin(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("invalid relative path")
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	if !within(path, root) {
		return "", errors.New("path escapes its root")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil && !within(resolved, root) {
		return "", errors.New("symlink escapes its root")
	}
	return path, nil
}
func readJSON(path string, value any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(value)
}
func writeJSON(path string, value any, mode fs.FileMode) error {
	data, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if e != nil {
		return e
	}
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	_, e = f.Write(append(data, '\n'))
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	return e
}
func hashFile(path string) (string, error) { return flasher.HashFile(path) }

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func copyFile(source, destination string, mode fs.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(destination), 0755); e != nil {
		return e
	}
	src, e := os.Open(source)
	if e != nil {
		return e
	}
	defer src.Close()
	dst, e := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if e != nil {
		return e
	}
	if e = dst.Chmod(mode); e == nil {
		_, e = io.Copy(dst, src)
	}
	ce := dst.Close()
	if e == nil {
		e = ce
	}
	return e
}
func files(root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(names)
	return names, err
}
func entry(root, name string) (Entry, error) {
	path, e := safeJoin(root, name)
	if e != nil {
		return Entry{}, e
	}
	st, e := os.Stat(path)
	if e != nil {
		return Entry{}, e
	}
	h, e := hashFile(path)
	return Entry{Path: name, Bytes: st.Size(), SHA256: h}, e
}
func verifyEntries(root string, entries []Entry) error {
	seen := map[string]bool{}
	for _, expected := range entries {
		if seen[expected.Path] {
			return fmt.Errorf("duplicate manifest path %s", expected.Path)
		}
		seen[expected.Path] = true
		actual, e := entry(root, expected.Path)
		if e != nil {
			return e
		}
		if actual.Bytes != expected.Bytes || actual.SHA256 != expected.SHA256 {
			return fmt.Errorf("size/checksum mismatch: %s", expected.Path)
		}
	}
	return nil
}
func checksumSet(root string) error {
	names, e := files(root)
	if e != nil {
		return e
	}
	var out strings.Builder
	for _, n := range names {
		if n == "SHA256SUMS" {
			continue
		}
		h, e := hashFile(filepath.Join(root, n))
		if e != nil {
			return e
		}
		fmt.Fprintf(&out, "%s  %s\n", h, n)
	}
	return os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(out.String()), 0644)
}
func finalize(root string, m Manifest) error {
	names, e := files(root)
	if e != nil {
		return e
	}
	m.Artifacts = nil
	for _, n := range names {
		if n == "MANIFEST.json" || n == "SHA256SUMS" {
			continue
		}
		v, e := entry(root, n)
		if e != nil {
			return e
		}
		m.Artifacts = append(m.Artifacts, v)
	}
	if e = writeJSON(filepath.Join(root, "MANIFEST.json"), m, 0644); e != nil {
		return e
	}
	return checksumSet(root)
}
func command(directory string, input []byte, name string, args ...string) ([]byte, error) {
	return commandContext(context.Background(), directory, input, name, args...)
}
func commandContext(ctx context.Context, directory string, input []byte, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.WaitDelay = 100 * time.Millisecond
	c.Dir = directory
	if input != nil {
		c.Stdin = strings.NewReader(string(input))
	}
	out, e := c.Output()
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if x, ok := e.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s failed: %s", filepath.Base(name), strings.TrimSpace(string(x.Stderr)))
		}
		return nil, e
	}
	return out, nil
}
func mustCommand(directory string, input []byte, name string, args ...string) ([]byte, error) {
	return command(directory, input, name, args...)
}
