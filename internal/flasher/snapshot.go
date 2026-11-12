package flasher

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// SnapshotReader reuses saved bytes during preparation. Omitted free blocks in
// covered filesystems read as zero; only uncovered gaps use the open device.
type SnapshotReader struct {
	Reader
	Root     string
	Manifest BackupManifest
}

func (s *SnapshotReader) SecureBoot(ctx context.Context) (string, error) {
	p, ok := s.Reader.(SecureBootReader)
	if !ok {
		return "", errors.New("hardware Secure Boot evidence unavailable")
	}
	return p.SecureBoot(ctx)
}

func (s *SnapshotReader) Info(ctx context.Context) (DeviceInfo, error) {
	if e := ctx.Err(); e != nil {
		return DeviceInfo{}, e
	}
	return s.Manifest.Device, nil
}

func OpenSnapshot(d Reader, root string) (*SnapshotReader, error) {
	m, e := LoadBackup(root)
	if e != nil {
		return nil, e
	}
	if !m.Complete {
		return nil, errors.New("incomplete snapshot")
	}
	return &SnapshotReader{d, root, m}, nil
}
func (s *SnapshotReader) Read(ctx context.Context, r Range, w io.Writer) error {
	if !r.Valid(s.Manifest.Device.Geometry) {
		return errors.New("snapshot bounds")
	}
	// Free blocks belong to the saved filesystem, not to a later partially
	// installed one. Return zero for omitted blocks, never fetch live bytes.
	for _, region := range s.Manifest.Regions {
		if r.Area == region.Range.Area && r.Offset >= region.Range.Offset && r.Offset+r.Length <= region.Range.Offset+region.Range.Length {
			return (backupReader{s.Root, s.Manifest}).Read(ctx, r, w)
		}
	}
	pos := r.Offset
	end := pos + r.Length
	for pos < end {
		if e := ctx.Err(); e != nil {
			return e
		}
		found := false
		for _, x := range s.Manifest.Extents {
			if x.Range.Area != r.Area || pos < x.Range.Offset || pos >= x.Range.Offset+x.Range.Length {
				continue
			}
			n := min(end-pos, x.Range.Offset+x.Range.Length-pos)
			path, e := safeFile(s.Root, x.File)
			if e != nil {
				return e
			}
			f, e := os.Open(filepath.Clean(path))
			if e != nil {
				return e
			}
			_, e = io.CopyN(w, io.NewSectionReader(f, int64(pos-x.Range.Offset), int64(n)), int64(n))
			f.Close()
			if e != nil {
				return e
			}
			pos += n
			found = true
			break
		}
		if !found {
			return s.Reader.Read(ctx, Range{r.Area, pos, end - pos}, w)
		}
	}
	return nil
}
func protection(guards []Extent, ops []Operation) []Extent {
	out := []Extent{}
	for _, x := range guards {
		overlap := false
		for _, op := range ops {
			a, b := x.Range, op.Range
			if a.Area == b.Area && a.Offset < b.Offset+b.Length && b.Offset < a.Offset+a.Length {
				overlap = true
				break
			}
		}
		if !overlap {
			out = append(out, x)
		}
	}
	return out
}

// MaterializeSnapshot recreates a sparse local view from saved occupied ranges.
// Omitted blocks stay holes; preparation never fetches them from USB.
func MaterializeSnapshot(ctx context.Context, s *SnapshotReader, r Range, f *os.File) error {
	if e := f.Truncate(int64(r.Length)); e != nil {
		return e
	}
	for _, x := range s.Manifest.Extents {
		if x.Range.Area != r.Area || x.Range.Offset < r.Offset || x.Range.Offset+x.Range.Length > r.Offset+r.Length {
			continue
		}
		if _, e := f.Seek(int64(x.Range.Offset-r.Offset), io.SeekStart); e != nil {
			return e
		}
		if e := s.Read(ctx, x.Range, f); e != nil {
			return e
		}
	}
	return nil
}
