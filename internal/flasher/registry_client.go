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
	root     string
	url      string
	keys     map[string]ed25519.PublicKey
	http     *http.Client
	mu       sync.Mutex
	snapshot *RegistrySnapshot
	warning  string
	etag     string
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
			c.warning = fmt.Sprintf("Cached catalog could not be read: %v", e)
			return c, nil
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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
			e = c.installLocked(ctx, raw)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.installLocked(ctx, raw)
}

func (c *RegistryClient) ImportFile(path string) error {
	raw, e := readRegistryFile(path)
	if e != nil {
		return e
	}
	return c.Import(raw)
}
func (c *RegistryClient) installLocked(ctx context.Context, raw []byte) error {
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
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			break
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	current := c.snapshot
	if existing, err := readRegistryFile(filepath.Join(c.root, "current.json")); err == nil {
		if cached, err := VerifyRegistry(existing, c.keys); err == nil {
			current = cached
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
