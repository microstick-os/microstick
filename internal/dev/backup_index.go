package dev

import (
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupSummary struct {
	Name, Path  string
	DeviceLabel string
	Created     time.Time
	Bytes       uint64
	Complete    bool
}

// ListBackupSummaries uses metadata and file lengths only. No USB connection or
// payload checksum is required to browse complete or legacy backups.
func ListBackupSummaries(p Paths) ([]BackupSummary, error) {
	names, e := filepath.Glob(filepath.Join(p.Work, "backups/*/BackupManifest.json"))
	if e != nil {
		return nil, e
	}
	var result []BackupSummary
	for _, name := range names {
		item, e := SummarizeBackup(filepath.Dir(name))
		if e == nil {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Created.After(result[j].Created) })
	return result, nil
}

// SummarizeBackup reads only manifest metadata and regular-file lengths.
// Complete means creation finished; ownership and integrity require preflight.
func SummarizeBackup(root string) (BackupSummary, error) {
	item := BackupSummary{Name: filepath.Base(root), Path: root}
	m, e := flasher.LoadBackup(root)
	if e != nil {
		return item, e
	}
	st, e := os.Stat(filepath.Join(root, "BackupManifest.json"))
	if e != nil {
		return item, e
	}
	item.Created, item.Complete = st.ModTime(), m.Complete
	item.DeviceLabel = backupDeviceLabel(m.Device.Identity)
	if parsed, e := time.Parse("20060102T150405.000000000Z", item.Name[lenPrefix(item.Name, "flasher-"):]); e == nil {
		item.Created = parsed
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return item, e
	}
	for _, entry := range entries {
		if info, e := entry.Info(); e == nil && info.Mode().IsRegular() {
			item.Bytes += uint64(info.Size())
		}
	}
	return item, nil
}

// A display tag from saved identity only; it never authorizes a restore.
func backupDeviceLabel(id flasher.Identity) string {
	switch id.Kind {
	case "storage", "sahara", "simulator":
		if strings.TrimSpace(id.Hardware) != "" {
			return "Модем " + strings.ToUpper(flasher.Hash(id)[:8])
		}
	}
	return "Устройство неизвестно"
}

func lenPrefix(s, p string) int {
	if len(s) >= len(p) && s[:len(p)] == p {
		return len(p)
	}
	return 0
}
