package flasher

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type trafficDevice struct {
	Writer
	read, written uint64
}

func (d *trafficDevice) Read(ctx context.Context, r Range, w io.Writer) error {
	d.read += r.Length
	return d.Writer.Read(ctx, r, w)
}
func (d *trafficDevice) Write(ctx context.Context, r Range, src io.Reader) error {
	d.written += r.Length
	return d.Writer.Write(ctx, r, src)
}
func TestOccupiedPayloadTrafficAndRetry(t *testing.T) {
	f := newFixture(t)
	data, e := os.ReadFile(filepath.Join(f.root, "fs.ext4"))
	if e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "rootfs.ext4"), data)
	runTest(t, "debugfs", "-w", "-R", "rm /state", filepath.Join(f.images, "rootfs.ext4"))
	if e = CreatePayload(testCtx, f.images, f.mapper); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(f.root, "backup")
	if _, e = CreateBackup(testCtx, f.d, f.report, backup, BackupOptions{Mapper: f.mapper, AllowFull: true}); e != nil {
		t.Fatal(e)
	}
	f.report.Resolution = Resolve(f.report, []HardwareProfile{fixtureProfile(f.report)})
	spy := &trafficDevice{Writer: f.d}
	plan, e := BuildInstallPlan(testCtx, spy, f.report, f.images, "", backup, f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	if spy.read > 65536 {
		t.Fatalf("plan scanned target: %d bytes", spy.read)
	}
	var useful uint64
	for _, op := range plan.Operations {
		useful += op.Range.Length
		if op.File == "data.ext4" {
			t.Fatal("userdata write")
		}
	}
	if useful >= uint64(len(data)) {
		t.Fatal("free space in payload")
	}
	journal := filepath.Join(f.root, "payload.jsonl")
	spy.read = 0
	var events []Progress
	if e = Execute(testCtx, spy, plan, f.images, backup, journal, Hash(plan), func(p Progress) { events = append(events, p) }, f.mapper); e != nil {
		t.Fatal(e)
	}
	assertPlanProgress(t, plan, events, 0)
	if spy.written != useful || spy.read > useful+2*(34+33)*512 {
		t.Fatalf("extra USB pass reads=%d writes=%d useful=%d", spy.read, spy.written, useful)
	}
	// Interrupted append is retained as raw evidence before the next retry.
	jf, e := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	jf.WriteString(`{"schema_version":`)
	jf.Close()

	before := spy.written
	spy.read = 0
	events = nil
	if e = Execute(testCtx, spy, plan, f.images, backup, journal, Hash(plan), func(p Progress) { events = append(events, p) }, f.mapper); e != nil {
		t.Fatal(e)
	}
	assertPlanProgress(t, plan, events, 2*useful)
	if spy.written != before || spy.read > plan.Operations[len(plan.Operations)-1].Range.Length+2*(34+33)*512 {
		t.Fatal("completed payload repeated")
	}

	retained, _ := filepath.Glob(journal + ".torn-*")
	if len(retained) != 1 {
		t.Fatal("torn journal evidence lost")
	}

	// Cancel after a durable first-range receipt, then resume without counting it
	// again or making the displayed work jump back to zero.
	journal = filepath.Join(f.root, "interrupted.jsonl")
	ctx, cancel := context.WithCancel(testCtx)
	e = Execute(ctx, spy, plan, f.images, backup, journal, Hash(plan), func(p Progress) {
		if p.Stage == "readback" && p.Done == p.Total {
			cancel()
		}
	}, f.mapper)
	if e != context.Canceled {
		t.Fatal("expected range-boundary cancellation", e)
	}
	events = nil
	if e = Execute(testCtx, spy, plan, f.images, backup, journal, Hash(plan), func(p Progress) { events = append(events, p) }, f.mapper); e != nil {
		t.Fatal(e)
	}
	assertPlanProgress(t, plan, events, 2*plan.Operations[0].Range.Length)
}
func TestBackupCacheDetectsMutation(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(f.root, "backup")
	m, e := CreateBackup(testCtx, f.d, f.report, root, BackupOptions{Mapper: f.mapper, AllowFull: true})
	if e != nil {
		t.Fatal(e)
	}
	ctx := WithBackupCache(testCtx)
	if e = VerifyBackup(ctx, root, f.report.Device, f.mapper); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, m.Extents[0].File)
	st, _ := os.Stat(path)
	file, e := os.OpenFile(path, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	file.WriteAt([]byte{99}, 0)
	file.Close()
	os.Chtimes(path, st.ModTime(), st.ModTime())
	if VerifyBackup(ctx, root, f.report.Device, f.mapper) == nil {
		t.Fatal("cached verification hid a modified backup")
	}
}
