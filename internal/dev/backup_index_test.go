package dev

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"microstick.dev/tools/internal/flasher"
)

type backupListReader struct{ info flasher.DeviceInfo }

func (d backupListReader) Info(ctx context.Context) (flasher.DeviceInfo, error) {
	return d.info, ctx.Err()
}
func (backupListReader) Read(context.Context, flasher.Range, io.Writer) error {
	return errors.New("list must not read device payload")
}

func TestRollbackFiltersOwnDeviceWhileLocalManagerListsAll(t *testing.T) {
	p := Paths{Work: t.TempDir()}
	info := flasher.DeviceInfo{Identity: flasher.Identity{Kind: "simulator", Hardware: "own-unit"}, Geometry: flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": 1 << 20}}}
	for _, name := range []string{"own", "foreign", "geometry", "incomplete"} {
		m := flasher.BackupManifest{Version: flasher.Schema, Complete: true, Device: info}
		switch name {
		case "foreign":
			m.Device.Identity.Hardware = "other-unit-same-model-and-geometry"
		case "geometry":
			m.Device.Geometry = flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": 2 << 20}}
		case "incomplete":
			m.Complete = false
		}
		path := filepath.Join(p.Work, "backups", name)
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
		if e := flasher.SaveJSON(filepath.Join(path, "BackupManifest.json"), m); e != nil {
			t.Fatal(e)
		}
	}
	all, e := ListBackupSummaries(p)
	if e != nil || len(all) != 4 {
		t.Fatal("local manager omitted copies", all, e)
	}
	own, e := FlasherBackups(context.Background(), p, backupListReader{info})
	if e != nil || len(own) != 1 || filepath.Base(own[0]) != "own" {
		t.Fatal("rollback accepted foreign/incomplete copy", own, e)
	}
}

func TestBackupDeviceLabelsAreStableAndPrivate(t *testing.T) {
	root := t.TempDir()
	labels := make([]string, 3)
	for i, name := range []string{"first-copy", "renamed-copy", "other-device"} {
		path := filepath.Join(root, name)
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
		id := "private-device-a"
		if i == 2 {
			id = "private-device-b"
		}
		m := flasher.BackupManifest{Version: flasher.Schema, Complete: true, Device: flasher.DeviceInfo{
			Identity: flasher.Identity{Kind: "storage", Hardware: id},
			Geometry: flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": uint64(i+1) << 20}},
		}}
		if e := flasher.SaveJSON(filepath.Join(path, "BackupManifest.json"), m); e != nil {
			t.Fatal(e)
		}
		item, e := SummarizeBackup(path)
		if e != nil {
			t.Fatal(e)
		}
		labels[i] = item.DeviceLabel
		if !strings.HasPrefix(item.DeviceLabel, "Модем ") || strings.Contains(item.DeviceLabel, id) {
			t.Fatal("missing/private device tag")
		}
	}
	if labels[0] != labels[1] || labels[0] == labels[2] {
		t.Fatal("device tags depend on the copy or merge different identities")
	}
	for _, id := range []flasher.Identity{{}, {Kind: "firehose"}, {Kind: "storage", Hardware: " "}} {
		if backupDeviceLabel(id) != "Устройство неизвестно" {
			t.Fatal("invented a device tag without identity")
		}
	}
}
