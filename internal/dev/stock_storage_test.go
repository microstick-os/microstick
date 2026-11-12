package dev

import (
	"bytes"
	"encoding/json"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"testing"
)

func TestSeparateDataPreparationPreservePasswordsAndRetry(t *testing.T) {
	p := testPaths(t)
	cache := p.Work
	p.Work = t.TempDir()
	if e := copyFile(filepath.Join(cache, "buildroot-downloads/lk2nd-msm8916-23.1.img"), filepath.Join(p.Work, "buildroot-downloads/lk2nd-msm8916-23.1.img"), 0600); e != nil {
		t.Fatal(e)
	}
	s := flasher.StorageRecipe{Recipe: flasher.StockRecipe, Source: "stock", BootMode: "lk2nd-23.1+512KiB", DataMode: "format", EraseConfirmed: true,
		Boot: flasher.Partition{Name: "boot", GUID: "00000001-1111-2222-3333-444444444444", Range: flasher.Range{Area: "user", Offset: 1 << 20, Length: 16 << 20}},
		Root: flasher.Partition{Name: "system", GUID: "00000002-1111-2222-3333-444444444444", Range: flasher.Range{Area: "user", Offset: 17 << 20, Length: 800 << 20}},
		Data: flasher.Partition{Name: "userdata", GUID: "00000003-1111-2222-3333-444444444444", Range: flasher.Range{Area: "user", Offset: 1 << 30, Length: (32 << 20) + 512}}}
	id, e := NewInstallationUUID()
	if e != nil {
		t.Fatal(e)
	}
	o := WizardOptions{InstallationID: id, SSID: DefaultSSID(id), APPassword: "fixture-initial-password", RootPassword: "fixture-root-password", RootPARTUUID: s.Root.GUID, Storage: &s, RequestBinding: "varied-unit"}
	output := filepath.Join(p.Work, "initial")
	record := filepath.Join(p.Work, "record.json")
	if e = PrepareWizard(p, p.Artifacts, record, output, o); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(output, "data.ext4")); !os.IsNotExist(e) {
		t.Fatal("userdata payload created", e)
	}
	root := filepath.Join(output, "rootfs.ext4")
	st, e := os.Stat(root)
	if e != nil || uint64(st.Size()) >= s.Root.Range.Length {
		t.Fatal("system expanded to capacity", e)
	}
	b, e := imageBytes(root, "/etc/microstick/storage.json")
	if e != nil {
		t.Fatal(e)
	}
	var conf map[string]any
	if json.Unmarshal(b, &conf) != nil || conf["mode"] != "format" || conf["partuuid"] != s.Data.GUID || conf["installation"] != id {
		t.Fatal("first boot contract incorrect")
	}
	if imageExists(root, "/data/plugins") {
		t.Fatal("hidden /data seeded")
	}
	boot, e := os.ReadFile(filepath.Join(output, "boot.img"))
	if e != nil {
		t.Fatal(e)
	}
	prov, vs, e := flasher.BootDTBs(boot)
	if e != nil || prov.Mode != "lk2nd-23.1" || len(vs) != 1 || !bytes.Contains(boot[flasher.LK2ndOffset:], []byte(s.Root.GUID)) {
		t.Fatal("boot/PARTUUID changed", e)
	}
	if e = PrepareWizard(p, p.Artifacts, record, output, o); e != nil {
		t.Fatal("retry", e)
	}
	o.RequestBinding = "different-unit"
	if PrepareWizard(p, p.Artifacts, record, output, o) == nil {
		t.Fatal("binding change accepted")
	}
	o.RequestBinding = "varied-unit"
	oldData := testExt4(t, p.Work, "existing-data.ext4")
	before, _ := hashFile(oldData)
	s.Source = "microstick"
	s.DataMode = "preserve"
	s.EraseConfirmed = false
	o.OldRootfs = root
	o.OldData = oldData
	o.APPassword = "new-selected-password"
	o.InstallationID = ""
	kept := filepath.Join(p.Work, "reinstall")
	if e = PrepareWizard(p, p.Artifacts, filepath.Join(p.Work, "next.json"), kept, o); e != nil {
		t.Fatal(e)
	}
	after, _ := hashFile(oldData)
	if before != after {
		t.Fatal("existing userdata modified")
	}
	if _, e = os.Stat(filepath.Join(kept, "data.ext4")); !os.IsNotExist(e) {
		t.Fatal("preserve reuploads userdata")
	}
	if !imageExists(filepath.Join(kept, "rootfs.ext4"), "/etc/microstick/data-auth.json") {
		t.Fatal("password delta not staged")
	}
	b, e = imageBytes(filepath.Join(kept, "rootfs.ext4"), "/etc/microstick/storage.json")
	if e != nil || !bytes.Contains(b, []byte(`"mode":"preserve"`)) {
		t.Fatal("preserve contract", e)
	}
	s.Root.Range.Length = 4096
	if PrepareWizard(p, p.Artifacts, filepath.Join(p.Work, "small.json"), filepath.Join(p.Work, "small"), o) == nil {
		t.Fatal("oversized root accepted")
	}
}
