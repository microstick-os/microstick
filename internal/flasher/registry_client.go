package flasher

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type RegistryClient struct {
	root           string
	url            string
	keys           map[string]ed25519.PublicKey
	http           *http.Client
	mu             sync.Mutex
	snapshot       *RegistrySnapshot
	warning        string
	automatic      sync.Once
	automaticError error
	etag           string
}

func NewRegistryClient(work string) (*RegistryClient, error) {
	keys, e := RegistryKeys()
	if e != nil {
		return nil, e
	}
	return openRegistryClient(filepath.Join(work, "board-registry"), RegistryURL, keys, &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 4 {
			return errors.New("invalid registry redirect")
		}
		return nil
	}})
}
func openRegistryClient(root, url string, keys map[string]ed25519.PublicKey, client *http.Client) (*RegistryClient, error) {
	s, e := BuiltinRegistry()
	if e != nil {
		return nil, e
	}
	c := &RegistryClient{root: root, url: url, keys: keys, http: client, snapshot: s}
	raw, e := readRegistryFile(filepath.Join(root, "current.json"))
	if e == nil {
		s, e = VerifyRegistry(raw, keys)
		if e != nil {
			return nil, fmt.Errorf("cached registry invalid: %w", e)
		}
		s.source = "cache"
		c.snapshot = s
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return c, nil
}
func readRegistryFile(path string) ([]byte, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: e}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > RegistryLimit {
		return nil, errors.New("invalid registry file")
	}
	return io.ReadAll(io.LimitReader(f, RegistryLimit+1))
}
func (c *RegistryClient) Current() *RegistrySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot
}
func (c *RegistryClient) Warning() string { c.mu.Lock(); defer c.mu.Unlock(); return c.warning }
func (c *RegistryClient) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if e != nil {
		return e
	}
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}
	response, e := c.http.Do(req)
	if e != nil {
		c.warning = "Registry update unavailable; using " + c.snapshot.Source() + ": " + e.Error()
		return e
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified && c.etag != "" {
		c.warning = ""
		return nil
	}
	if response.StatusCode != http.StatusOK {
		e = fmt.Errorf("registry HTTP %d", response.StatusCode)
	} else {
		var raw []byte
		raw, e = io.ReadAll(io.LimitReader(response.Body, RegistryLimit+1))
		if e == nil {
			e = c.installLocked(raw)
		}
	}
	if e != nil {
		c.warning = "Registry update rejected; using " + c.snapshot.Source() + ": " + e.Error()
		return e
	}
	c.warning = ""
	if tag := response.Header.Get("ETag"); len(tag) <= 1024 {
		c.etag = tag
	}
	return nil
}
func (c *RegistryClient) Import(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.installLocked(raw)
}

func (c *RegistryClient) ImportFile(path string) error {
	raw, e := readRegistryFile(path)
	if e != nil {
		return e
	}
	return c.Import(raw)
}
func (c *RegistryClient) installLocked(raw []byte) error {
	next, e := VerifyRegistry(raw, c.keys)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(c.root, 0700); e != nil {
		return e
	}
	fd, e := syscall.Open(filepath.Join(c.root, ".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer syscall.Close(fd)
	if e = syscall.Flock(fd, syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	current := c.snapshot
	if existing, err := readRegistryFile(filepath.Join(c.root, "current.json")); err == nil {
		current, e = VerifyRegistry(existing, c.keys)
		if e != nil {
			return e
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if next.Sequence() < current.Sequence() {
		return errors.New("registry rollback rejected")
	}
	if next.Sequence() == current.Sequence() {
		if next.Digest() != current.Digest() {
			return errors.New("registry sequence has conflicting contents")
		}
		c.snapshot = current
		return nil
	}
	// Retain revocations even if a later publication accidentally omits a tombstone.
	for h, reason := range current.payload.Revoked {
		if next.payload.Revoked[h] != reason {
			return errors.New("registry drops a profile revocation")
		}
	}
	for _, b := range current.payload.Boards {
		if b.Status == "blocked" {
			found := false
			for _, n := range next.payload.Boards {
				if n.ID == b.ID {
					found = true
					if n.Revision <= b.Revision && n.Status != "blocked" {
						return errors.New("blocked board requires a newer reviewed revision")
					}
				}
			}
			if !found {
				return errors.New("registry drops a blocked board")
			}
		}
	}
	if e = writeRegistryAtomic(c.root, raw); e != nil {
		return e
	}
	next.source = "cache"
	c.snapshot = next
	return nil
}
func writeRegistryAtomic(root string, raw []byte) error {
	f, e := os.CreateTemp(root, ".snapshot-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), filepath.Join(root, "current.json")); e != nil {
		return e
	}
	return syncDir(root)
}

// Ensure checks the registry once per process. Explicit Refresh remains available.
func (c *RegistryClient) Ensure(ctx context.Context) error {
	c.automatic.Do(func() { c.automaticError = c.Refresh(ctx) })
	return c.automaticError
}
