package dev

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"microstick.dev/tools/internal/flasher"
)

func researchFixture(t *testing.T) (Paths, flasher.HardwareReport, []byte) {
	t.Helper()
	tree, e := flasher.DTBFromProperties(map[string]map[string][]byte{"/": {"compatible": []byte("qcom,msm8916\x00"), "qcom,board-id": {0, 0, 0, 1, 0, 0, 0, 0}}})
	if e != nil {
		t.Fatal(e)
	}
	v, e := flasher.ParseDTB(tree, "boot", 0)
	if e != nil {
		t.Fatal(e)
	}
	r := flasher.HardwareReport{Version: flasher.Schema, Device: flasher.DeviceInfo{Identity: flasher.Identity{Kind: "storage", Hardware: "synthetic-device-a"}, Geometry: flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": 64 << 20, "boot1": 4096, "boot2": 4096}}}, Variants: []flasher.DTVariant{v}}
	r.GPT.Partitions = []flasher.Partition{{Name: "boot", Range: flasher.Range{Area: "user", Offset: 1 << 20, Length: 8 << 20}}, {Name: "rootfs", Range: flasher.Range{Area: "user", Offset: 9 << 20, Length: 32 << 20}}}
	return Paths{Work: t.TempDir()}, r, tree
}

func TestResearchCaptureBindsAndSurvivesNewSessionOrUSBPath(t *testing.T) {
	p, r, tree := researchFixture(t)
	dir := filepath.Join(p.Work, "buildroot-private/live-device-trees")
	os.MkdirAll(dir, 0700)
	original := "usb:003-02.004"
	if e := writeJSONAtomic(filepath.Join(dir, digest([]byte(original))+".json"), LiveTree{USBPath: original, DTB: tree}); e != nil {
		t.Fatal(e)
	}
	got, e := ResearchTree(p, r, "@3-2.4", nil, nil)
	if e != nil || !bytes.Equal(got, tree) {
		t.Fatal("ADB capture not loaded after EDL", e)
	}
	os.RemoveAll(dir)
	reopened := Paths{Work: p.Work}
	got, e = ResearchTree(reopened, r, "@9-1", nil, nil)
	if e != nil || !bytes.Equal(got, tree) {
		t.Fatal("research lost across process/USB reconnect", e)
	}
	other := r
	other.Device.Identity.Hardware = "synthetic-device-b"
	if got, e = ResearchTree(reopened, other, "@9-1", nil, nil); e != nil || got != nil {
		t.Fatal("research crossed devices", e)
	}
	changed := r
	changed.GPT.Partitions = append([]flasher.Partition(nil), r.GPT.Partitions...)
	changed.GPT.Partitions[1].Range.Length -= 512
	if got, e = ResearchTree(reopened, changed, "@9-1", nil, nil); e != nil || got != nil {
		t.Fatal("stale storage layout reused", e)
	}
}

func TestResearchDoesNotCreateAnyRegistryState(t *testing.T) {
	p, r, tree := researchFixture(t)
	if e := SaveDeviceResearch(p, r, tree, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(p.Work, "board-registry")); !os.IsNotExist(e) {
		t.Fatal("research touched registry", e)
	}
	if _, e := ResearchTree(p, r, "@1-2", nil, nil); e != nil {
		t.Fatal(e)
	}
}

func TestBoundCaptureCannotBeReusedForAnotherBoardOnSamePort(t *testing.T) {
	p, r, tree := researchFixture(t)
	dir := filepath.Join(p.Work, "buildroot-private/live-device-trees")
	os.MkdirAll(dir, 0700)
	usb := "@1-3"
	if e := writeJSONAtomic(filepath.Join(dir, digest([]byte(usb))+".json"), LiveTree{USBPath: usb, DTB: tree}); e != nil {
		t.Fatal(e)
	}
	if _, e := ResearchTree(p, r, usb, nil, nil); e != nil {
		t.Fatal(e)
	}
	other := r
	other.Device.Identity.Hardware = "synthetic-device-b"
	if got, e := ResearchTree(p, other, usb, nil, nil); e != nil || got != nil {
		t.Fatal("bound capture leaked to another board", e)
	}
	// A new investigation on that port supersedes the old association.
	newer, e := flasher.DTBFromProperties(map[string]map[string][]byte{"/": {"compatible": []byte("qcom,msm8916\x00"), "model": []byte("another synthetic device\x00")}})
	if e != nil {
		t.Fatal(e)
	}
	if e = writeJSONAtomic(filepath.Join(dir, digest([]byte(usb))+".json"), LiveTree{USBPath: usb, DTB: newer}); e != nil {
		t.Fatal(e)
	}
	if got, e := ResearchTree(p, other, usb, nil, nil); e != nil || !bytes.Equal(got, newer) {
		t.Fatal("fresh capture not used", e)
	}
}

func TestFreshResearchSupersedesPreviousCaptureForSameDevice(t *testing.T) {
	p, r, old := researchFixture(t)
	if e := SaveDeviceResearch(p, r, old, nil); e != nil {
		t.Fatal(e)
	}
	newer, e := flasher.DTBFromProperties(map[string]map[string][]byte{"/": {"compatible": []byte("qcom,msm8916\x00"), "model": []byte("fresh synthetic research\x00")}})
	if e != nil {
		t.Fatal(e)
	}
	usb := "@2-5"
	dir := filepath.Join(p.Work, "buildroot-private/live-device-trees")
	os.MkdirAll(dir, 0700)
	if e = writeJSONAtomic(filepath.Join(dir, digest([]byte(usb))+".json"), LiveTree{USBPath: usb, DTB: newer}); e != nil {
		t.Fatal(e)
	}
	got, e := ResearchTree(p, r, usb, nil, nil)
	if e != nil || !bytes.Equal(got, newer) {
		t.Fatal("old capture overrode fresh research", e)
	}
}
