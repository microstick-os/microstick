package flasher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// PayloadManifest maps occupied ext4 blocks to file offsets, independent of
// partition capacity. The saved descriptor is included in preparation receipts.
type PayloadExtent struct {
	Offset uint64 `json:"offset"`
	Length uint64 `json:"length"`
	SHA256 string `json:"sha256"`
}
type PayloadManifest struct {
	Version    int             `json:"schema_version"`
	RootBytes  uint64          `json:"root_bytes"`
	RootSHA256 string          `json:"root_sha256"`
	Extents    []PayloadExtent `json:"extents"`
}

func hashSection(f *os.File, off, n uint64) (string, error) {
	h := sha256.New()
	copied, e := io.CopyN(h, io.NewSectionReader(f, int64(off), int64(n)), int64(n))
	localHashBytes.Add(uint64(copied))
	return hex.EncodeToString(h.Sum(nil)), e
}
func CreatePayload(ctx context.Context, images, mapper string) error {
	path := filepath.Join(images, "rootfs.ext4")
	d, e := OpenFiles(map[string]string{"user": path}, "local-payload", false)
	if e != nil {
		return e
	}
	defer d.Close()
	info, e := d.Info(ctx)
	if e != nil {
		return e
	}
	n := info.Geometry.Areas["user"]
	rs, e := AllocationMap(ctx, d, Range{"user", 0, n}, mapper)
	if e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h, e := HashFile(path)
	if e != nil {
		return e
	}
	m := PayloadManifest{Version: 1, RootBytes: n, RootSHA256: h}
	for _, r := range rs {
		for off := r.Offset; off < r.Offset+r.Length; {
			length := min(uint64(4<<20), r.Offset+r.Length-off)
			h, e := hashSection(f, off, length)
			if e != nil {
				return e
			}
			m.Extents = append(m.Extents, PayloadExtent{off, length, h})
			off += length
		}
	}
	return SaveJSON(filepath.Join(images, "Payload.json"), m)
}
func validatePayloadTargets(p InstallPlan) error {
	if p.PayloadVersion != 1 || requireHash(p.PayloadSHA256) != nil || requireHash(p.InstallationSHA256) != nil || len(p.Targets) != 2 || len(p.Operations) < 2 || p.Operations[len(p.Operations)-1].File != "boot.img" {
		return errors.New("incomplete payload targets/receipt")
	}
	root, boot := p.Targets[0], p.Targets[1]
	wantRoot := "rootfs"
	if p.Recipe == StockRecipe {
		wantRoot = "system"
		if p.Storage == nil || p.Storage.Validate(p.Device.Geometry) != nil {
			return errors.New("invalid storage")
		}
	}
	if root.Name != wantRoot || boot.Name != "boot" || root.GUID == "" || boot.GUID == "" {
		return errors.New("payload target roles")
	}
	if e := validateRanges([]Range{root.Range, boot.Range}, p.Device.Geometry); e != nil {
		return e
	}
	rootCount := 0
	for i, op := range p.Operations {
		if op.File == "rootfs.ext4" {
			if op.Range.Area != root.Range.Area || op.Range.Offset < root.Range.Offset || op.FileOffset != op.Range.Offset-root.Range.Offset || op.FileOffset > root.Range.Length || op.Range.Length > root.Range.Length-op.FileOffset || op.Range.Length > 4<<20 {
				return errors.New("root payload outside actual partition")
			}
			rootCount++
		} else if op.File == "boot.img" {
			if i != len(p.Operations)-1 || op.FileOffset != 0 || op.Range.Area != boot.Range.Area || op.Range.Offset != boot.Range.Offset || op.Range.Length > boot.Range.Length {
				return errors.New("boot payload mapping")
			}
		} else if op.File == BootTZFile || op.File == BootHYPFile {
			if p.BootFirmware == nil || !p.BootFirmware.Upgrade || op.FileOffset != 0 || i < len(p.Operations)-3 || i >= len(p.Operations)-1 {
				return errors.New("unexpected firmware payload")
			}
		} else {
			return errors.New("unexpected payload file")
		}
	}
	if rootCount == 0 || p.Operations[0].File != "rootfs.ext4" || p.Operations[0].FileOffset != 0 {
		return errors.New("missing root superblock")
	}
	if Hash(protection(p.Guards, p.Operations)) != Hash(p.Protected) || len(p.Protected) == 0 {
		return errors.New("payload GPT protection")
	}
	return nil
}

func verifyPayloadDescriptor(p InstallPlan, images string) error {
	if p.PayloadVersion != 1 || p.Mode == "restore" {
		return nil
	}
	h, e := HashFile(filepath.Join(images, "Payload.json"))
	if e != nil {
		return e
	}
	if h != p.PayloadSHA256 {
		return errors.New("payload descriptor changed")
	}
	var desc PayloadManifest
	if e := readJSON(filepath.Join(images, "Payload.json"), &desc); e != nil {
		return e
	}
	if desc.Version != 1 {
		return errors.New("payload version")
	}
	if len(p.Targets) == 0 {
		return errors.New("payload target missing")
	}
	root := p.Targets[0]
	index := 0
	for _, op := range p.Operations {
		if op.File != "rootfs.ext4" {
			continue
		}
		if index >= len(desc.Extents) {
			return errors.New("extra root extent")
		}
		x := desc.Extents[index]
		if op.FileOffset != x.Offset || op.Range != (Range{root.Range.Area, root.Range.Offset + x.Offset, x.Length}) || op.SHA256 != x.SHA256 {
			return errors.New("payload extent/receipt mismatch")
		}
		index++
	}
	if index != len(desc.Extents) {
		return errors.New("missing root extents")
	}
	h, e = HashFile(filepath.Join(images, "rootfs.ext4"))
	if e != nil {
		return e
	}
	if h != desc.RootSHA256 {
		return errors.New("root payload changed")
	}
	return nil
}
