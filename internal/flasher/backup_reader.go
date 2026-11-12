package flasher

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
)

// A verified backup is a virtual disk: only free ext4 blocks and unpartitioned
// gaps read as zero. Native allocation verification proves that every used
// filesystem block is present before a backup is accepted for installation or
// restore; a self-declared 'complete' bit is insufficient.
type backupReader struct {
	root     string
	manifest BackupManifest
}

func (b backupReader) Info(ctx context.Context) (DeviceInfo, error) {
	return b.manifest.Device, ctx.Err()
}
func (b backupReader) Read(ctx context.Context, r Range, w io.Writer) error {
	if !r.Valid(b.manifest.Device.Geometry) {
		return errors.New("backup virtual read bounds")
	}
	end := r.Offset + r.Length
	extents := append([]Extent(nil), b.manifest.Extents...)
	sortExtents(extents)
	pos := r.Offset
	for _, x := range extents {
		if x.Range.Area != r.Area || x.Range.Offset+x.Range.Length <= pos || x.Range.Offset >= end {
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if pos < x.Range.Offset {
			n := min(end, x.Range.Offset) - pos
			if _, e := io.CopyN(w, zeroReaderInternal{}, int64(n)); e != nil {
				return e
			}
			pos += n
		}
		if pos >= end {
			break
		}
		take := min(end-pos, x.Range.Length-(pos-x.Range.Offset))
		p, e := safeFile(b.root, x.File)
		if e != nil {
			return e
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		_, e = io.CopyN(w, io.NewSectionReader(f, int64(pos-x.Range.Offset), int64(take)), int64(take))
		f.Close()
		if e != nil {
			return e
		}
		pos += take
	}
	if pos < end {
		_, e := io.CopyN(w, zeroReaderInternal{}, int64(end-pos))
		return e
	}
	return nil
}

type zeroReaderInternal struct{}

func (zeroReaderInternal) Read(b []byte) (int, error) { clear(b); return len(b), nil }
func sortExtents(xs []Extent) {
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].Range.Area != xs[j].Range.Area {
			return xs[i].Range.Area < xs[j].Range.Area
		}
		return xs[i].Range.Offset < xs[j].Range.Offset
	})
}
