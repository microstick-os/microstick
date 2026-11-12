package dev

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"microstick.dev/tools/internal/flasher"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Resource struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
	Member string `json:"member,omitempty"`
}

// resourceFile downloads and verifies an entry from the resource catalog.
func resourceFile(p Paths, id string) (string, Resource, error) {
	if p.BoardProfile != nil {
		r, ok := p.BoardProfile.Resource(id)
		if !ok {
			return "", Resource{}, errors.New("board resource is unavailable: " + id)
		}
		v := Resource{URL: r.URL, SHA256: r.SHA256, File: r.File, Member: r.Member}
		path := filepath.Join(p.Work, "buildroot-downloads", r.SHA256+"-"+r.File)
		legacy := filepath.Join(p.Work, "buildroot-downloads", r.File)
		if h, e := hashFile(legacy); e == nil && h == r.SHA256 {
			path = legacy
		}
		if e := fetchBoardResource(path, r); e != nil {
			return "", v, e
		}
		if r.Bytes > 0 {
			st, e := os.Stat(path)
			if e != nil || st.Size() != r.Bytes {
				return "", v, errors.New("board resource size differs")
			}
		}
		return path, v, nil
	}
	var catalog map[string]Resource
	if e := readJSON(filepath.Join(p.Repo, "boards/resources.json"), &catalog); e != nil {
		if !os.IsNotExist(e) {
			return "", Resource{}, e
		}
		s, e := flasher.Catalog()
		if e != nil {
			return "", Resource{}, e
		}
		r, ok := s[0].Resource(id)
		if !ok {
			return "", Resource{}, errors.New("resource unavailable")
		}
		catalog = map[string]Resource{id: {URL: r.URL, SHA256: r.SHA256, File: r.File, Member: r.Member}}
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

func fetchBoardResource(path string, r flasher.BoardResource) error {
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 4 {
			return errors.New("invalid board resource redirect")
		}
		return nil
	}}
	return fetchBoardResourceWithClient(path, r, client)
}

func fetchBoardResourceWithClient(path string, r flasher.BoardResource, client *http.Client) error {
	if st, e := os.Lstat(path); e == nil {
		if !st.Mode().IsRegular() || r.Bytes > 0 && st.Size() != r.Bytes {
			return errors.New("invalid cached board resource")
		}
		h, e := hashFile(path)
		if e != nil {
			return e
		}
		if h != r.SHA256 {
			return errors.New("cached board resource checksum differs")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	response, e := client.Get(r.URL)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("board resource HTTP %d", response.StatusCode)
	}
	limit := r.Bytes
	if limit <= 0 || limit > 1<<30 {
		return errors.New("invalid board resource size")
	}
	if response.ContentLength >= 0 && response.ContentLength != limit {
		return errors.New("board resource length differs")
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".resource-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(response.Body, limit+1))
	if e == nil && (n != limit || hex.EncodeToString(h.Sum(nil)) != r.SHA256) {
		e = errors.New("board resource size or checksum differs")
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

func boardResourceBytes(p Paths, id string) ([]byte, error) {
	path, r, e := resourceFile(p, id)
	if e != nil {
		return nil, e
	}
	var data []byte
	if r.Member == "" {
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		data, e = io.ReadAll(io.LimitReader(f, 16<<20+1))
		if e != nil {
			return nil, e
		}
	} else {
		z, e := zip.OpenReader(path)
		if e != nil {
			return nil, e
		}
		defer z.Close()
		found := false
		for _, entry := range z.File {
			if entry.Name != r.Member {
				continue
			}
			if found || entry.UncompressedSize64 > 16<<20 {
				return nil, errors.New("invalid board resource member")
			}
			found = true
			f, e := entry.Open()
			if e != nil {
				return nil, e
			}
			data, e = io.ReadAll(io.LimitReader(f, 16<<20+1))
			f.Close()
			if e != nil {
				return nil, e
			}
		}
		if !found {
			return nil, errors.New("board resource member missing")
		}
	}
	if len(data) > 16<<20 {
		return nil, errors.New("board resource exceeds limit")
	}
	if p.BoardProfile != nil {
		r, _ := p.BoardProfile.Resource(id)
		if r.ResultSHA256 != "" && digest(data) != r.ResultSHA256 {
			return nil, errors.New("board resource member checksum differs")
		}
	}
	return data, nil
}
