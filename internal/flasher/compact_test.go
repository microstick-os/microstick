package flasher

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDirtyJournalBackupKeepsOriginalBytesAndVerifiesCoverage(t *testing.T) {
	f := newFixture(t)
	r := f.report.GPT.Partitions[2].Range
	// debugfs updates the metadata checksum when setting needs_recovery.
	image := filepath.Join(f.root, "fs.ext4")
	runTest(t, "debugfs", "-w", "-R", "feature needs_recovery", image)
	data, e := os.ReadFile(image)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.d.Write(testCtx, r, bytes.NewReader(data)); e != nil {
		t.Fatal(e)
	}
	original, e := HashRange(testCtx, f.d, r)
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(f.root, "dirty-journal-backup")
	m, e := CreateBackup(testCtx, f.d, f.report, root, BackupOptions{Mapper: f.mapper, AllowFull: true})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, region := range m.Regions {
		if region.Range == r {
			found = region.Mode == "ext4-journal"
		}
	}
	if !found || !m.FreeBlocksOmitted {
		t.Fatal("dirty journal forced a full copy", m.Defects)
	}
	after, e := HashRange(testCtx, f.d, r)
	if e != nil || after != original {
		t.Fatal("original device repaired during backup", e)
	}
	if e = VerifyBackup(testCtx, root, f.report.Device, f.mapper); e != nil {
		t.Fatal(e)
	}
	// Removing a used block must fail even with matching manifest/file hashes.
	for i := range m.Regions {
		region := &m.Regions[i]
		if region.Range != r {
			continue
		}
		x := region.Allocated[len(region.Allocated)-1]
		region.Allocated = region.Allocated[:len(region.Allocated)-1]
		var kept []Extent
		for _, v := range m.Extents {
			if v.Range != x {
				kept = append(kept, v)
			}
		}
		m.Extents = kept
	}
	if e = SaveJSON(filepath.Join(root, "BackupManifest.json"), m); e != nil {
		t.Fatal(e)
	}
	if VerifyBackup(testCtx, root, f.report.Device, f.mapper) == nil {
		t.Fatal("omitted occupied blocks accepted after journal replay")
	}
}

func TestBackupReportsProgressInsideLargePartition(t *testing.T) {
	f := newFixture(t)
	var updates []Progress
	_, e := CreateBackup(testCtx, f.d, f.report, filepath.Join(f.root, "progress-backup"), BackupOptions{Mapper: f.mapper, AllowFull: true, FullPartitions: true, Emit: func(p Progress) {
		if p.Stage == "backup" {
			updates = append(updates, p)
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	if len(updates) < 8 {
		t.Fatal("progress only arrived at partition boundaries", len(updates))
	}
	for i, p := range updates {
		if i > 0 && p.Done < updates[i-1].Done {
			t.Fatal("backup progress moved backwards")
		}
	}
	last := updates[len(updates)-1]
	if last.Done != last.Total {
		t.Fatal("missing final byte count")
	}
}
