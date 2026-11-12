package flasher

import (
	"context"
	"io"
	"path/filepath"
	"testing"
)

type measuredReader struct {
	Reader
	bytes, requests uint64
}

func (d *measuredReader) Read(ctx context.Context, r Range, w io.Writer) error {
	d.bytes += r.Length
	d.requests++
	return d.Reader.Read(ctx, r, w)
}
func TestBackupDoesNotReadAllDeviceBytesTwice(t *testing.T) {
	f := newFixture(t)
	d := &measuredReader{Reader: f.d}
	ranges, e := Coverage(f.report)
	if e != nil {
		t.Fatal(e)
	}
	var total uint64
	for _, r := range ranges {
		total += r.Length
	}
	root := filepath.Join(f.root, "single-read-backup")
	if _, e = CreateBackup(testCtx, d, f.report, root, BackupOptions{Mapper: f.mapper, AllowFull: true, FullPartitions: true}); e != nil {
		t.Fatal(e)
	}
	if d.bytes < total || d.bytes > total+64<<10 {
		t.Fatal("redundant full-device read", d.bytes, total)
	}
	snapshot, e := OpenSnapshot(d, root)
	if e != nil {
		t.Fatal(e)
	}
	before := d.bytes
	for _, r := range ranges {
		if _, e = HashRange(testCtx, snapshot, r); e != nil {
			t.Fatal(e)
		}
	}
	if d.bytes != before {
		t.Fatal("preparation snapshot reread the device")
	}
}
