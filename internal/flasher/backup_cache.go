package flasher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type backupCacheKey struct{}
type backupCache struct {
	mu    sync.Mutex
	seals map[string]string
}

// Cache lifetime is one operation. Any write, rename, size or metadata change
// invalidates the seal, including a writer restoring the original mtime.
func WithBackupCache(ctx context.Context) context.Context {
	if ctx.Value(backupCacheKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, backupCacheKey{}, &backupCache{seals: map[string]string{}})
}
func backupSeal(root string) (string, error) {
	m, e := LoadBackup(root)
	if e != nil {
		return "", e
	}
	mh, e := HashFile(filepath.Join(root, "BackupManifest.json"))
	if e != nil {
		return "", e
	}
	stats := []any{mh}
	for _, x := range m.Extents {
		path, e := safeFile(root, x.File)
		if e != nil {
			return "", e
		}
		st, e := os.Lstat(path)
		if e != nil {
			return "", e
		}
		raw := st.Sys().(*syscall.Stat_t)
		stats = append(stats, []any{x.File, raw.Dev, raw.Ino, raw.Size, raw.Mtim, raw.Ctim})
	}
	return Hash(stats), nil
}
func VerifyBackup(ctx context.Context, root string, device DeviceInfo, mapper ...string) error {
	cache, _ := ctx.Value(backupCacheKey{}).(*backupCache)
	if cache == nil {
		return verifyBackup(ctx, root, device, mapper...)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := root + Hash(device)
	seal, e := backupSeal(root)
	if e != nil {
		return e
	}
	if cache.seals[key] == seal {
		return ctx.Err()
	}
	if e = verifyBackup(ctx, root, device, mapper...); e != nil {
		return e
	}
	seal, e = backupSeal(root)
	if e == nil {
		cache.seals[key] = seal
	}
	return e
}
