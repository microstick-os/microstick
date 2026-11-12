package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"time"
)

type FlasherRelease struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Published string `json:"published_at"`
}

const installedReleasePath = "/usr/share/microstick/installed-release.json"

func containsMicrostick(b []byte) bool { return bytes.Contains(b, []byte("microstick-web:")) }

// Release identity binds the verified base payload. An optional manifest-listed
// release.json supplies a public version/date; legacy local bundles use mtime.
func ReadFlasherRelease(root string) (FlasherRelease, error) {
	m, e := verifyManifest(root)
	if e != nil {
		return FlasherRelease{}, e
	}
	hashes := map[string]string{}
	for _, v := range m.Artifacts {
		if v.Path == "boot.img" || v.Path == "rootfs.ext4" {
			hashes[v.Path] = v.SHA256
		}
	}
	b, _ := json.Marshal(hashes)
	id := digest(b)
	r := FlasherRelease{ID: id, Version: "local-" + id[:12]}
	st, e := os.Stat(filepath.Join(root, "MANIFEST.json"))
	if e != nil {
		return r, e
	}
	r.Published = st.ModTime().UTC().Format(time.RFC3339Nano)
	for _, v := range m.Artifacts {
		if v.Path == "release.json" {
			var metadata FlasherRelease
			if e = readJSON(filepath.Join(root, v.Path), &metadata); e != nil {
				return r, e
			}
			if metadata.Version == "" {
				return r, errors.New("release version missing")
			}
			if _, e = time.Parse(time.RFC3339Nano, metadata.Published); e != nil {
				return r, e
			}
			r.Version = metadata.Version
			r.Published = metadata.Published
		}
	}
	return r, nil
}
func InstalledFlasherRelease(image string) (FlasherRelease, error) {
	marker, e := imageBytes(image, "/etc/passwd")
	if e != nil || !containsMicrostick(marker) {
		return FlasherRelease{}, errors.New("microstick ещё не установлена")
	}
	if !imageExists(image, installedReleasePath) {
		return FlasherRelease{Version: "legacy"}, nil
	}
	b, e := imageBytes(image, installedReleasePath)
	if e != nil {
		return FlasherRelease{Version: "legacy"}, nil
	}
	var r FlasherRelease
	e = json.Unmarshal(b, &r)
	return r, e
}
func CheckFlasherUpdate(installed, next FlasherRelease) error {
	if installed.ID == next.ID {
		return errors.New("этот релиз уже установлен; обновление не требуется")
	}
	if installed.ID != "" {
		old, e := time.Parse(time.RFC3339Nano, installed.Published)
		if e != nil {
			return e
		}
		fresh, e := time.Parse(time.RFC3339Nano, next.Published)
		if e != nil {
			return e
		}
		if !fresh.After(old) {
			return errors.New("комплект содержит более старый релиз; для возврата используйте откат")
		}
	}
	return nil
}

func ProbeInstalledRelease(ctx context.Context, d flasher.Reader, r flasher.HardwareReport, mapper string) (FlasherRelease, error) {
	for _, part := range r.GPT.Partitions {
		if part.Name == "rootfs" || part.Name == "system" {
			marker, e := flasher.ReadExt4File(ctx, d, part.Range, mapper, "/etc/passwd")
			if e != nil || !containsMicrostick(marker) {
				return FlasherRelease{}, errors.New("microstick ещё не установлена; выберите установку")
			}
			b, e := flasher.ReadExt4File(ctx, d, part.Range, mapper, installedReleasePath)
			if errors.Is(e, flasher.ErrExt4FileMissing) {
				return FlasherRelease{Version: "legacy"}, nil
			}
			if e != nil {
				return FlasherRelease{}, e
			}
			var v FlasherRelease
			e = json.Unmarshal(b, &v)
			return v, e
		}
	}
	return FlasherRelease{}, errors.New("системный раздел не найден")
}
