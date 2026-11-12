package flasher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func canonicalStockProfile(p HardwareProfile) bool {
	return p.Recipe == StockRecipe && canonicalNativeProfile(p)
}
func canonicalNativeProfile(p HardwareProfile) bool {
	catalog, e := Catalog()
	if e != nil {
		return false
	}
	for _, known := range catalog {
		if p.ID == known.ID && known.Qualified && p.Recipe == known.Recipe && Hash(p) == Hash(known) {
			return true
		}
	}
	return false
}
func buildStockPlan(ctx context.Context, d Reader, r HardwareReport, profile HardwareProfile, images, quick, backup string, mapper ...string) (InstallPlan, error) {
	p := InstallPlan{Version: Schema, Device: r.Device, Profile: profile.ID, Recipe: StockRecipe, Mode: "install", qualification: &profile}
	if !canonicalStockProfile(profile) {
		return p, ErrUnsupported
	}
	fresh := r
	fresh.Resolution = Resolve(r, []HardwareProfile{profile})
	if e := checkDevice(ctx, d, r.Device); e != nil {
		return p, e
	}
	var e error
	if e = requireMatched(fresh); e != nil {
		return p, e
	}
	var att ImageAttestation
	if e = readJSON(filepath.Join(images, "HardwareInputs.json"), &att); e != nil {
		return p, e
	}
	if att.Version != Schema || !EqualDevice(att.Device, r.Device) || att.Profile != profile.ID || !CompatibleBundle(profile, att.Bundle) || att.Storage == nil {
		return p, errors.New("аппаратные входы/storage не совпадают")
	}
	if e = bindBootFirmware(ctx, d, fresh, profile, att, images, &p); e != nil {
		return p, e
	}
	s, e := StockStorage(fresh, att.Storage.DataMode == "format", att.Storage.EraseConfirmed)
	if e != nil {
		return p, e
	}
	if Hash(s) != Hash(att.Storage) {
		return p, errors.New("storage mapping изменён")
	}
	p.Storage = &s
	p.InstallationSHA256, e = HashFile(filepath.Join(images, "Preparation.json"))
	if e != nil {
		return p, e
	}
	if p.InstallationSHA256 != att.ReceiptSHA256 {
		return p, errors.New("installation receipt изменён")
	}
	for _, name := range []string{"boot.img", "rootfs.ext4", "Payload.json"} {
		h, e := HashFile(filepath.Join(images, name))
		if e != nil {
			return p, e
		}
		if h != att.Images[name] {
			return p, errors.New("подготовленный образ изменён")
		}
	}
	if e = verifyStockImages(images, s, profile.Bundle["dtb"]); e != nil {
		return p, e
	}
	return buildInstallPlan(ctx, d, fresh, images, quick, backup, p, mapper...)
}
func verifyStockImages(images string, s StorageRecipe, dtb string) error {
	b, e := os.ReadFile(filepath.Join(images, "boot.img"))
	if e != nil {
		return e
	}
	provenance, vs, e := BootDTBs(b)
	if e != nil {
		return e
	}
	if provenance.Mode != "lk2nd-23.1" || len(vs) != 1 || vs[0].SHA256 != dtb {
		return errors.New("неверный составной boot/DTB")
	}
	return nil
}
func validateStockPlan(p InstallPlan) error {
	if p.PayloadVersion == 1 {
		return validatePayloadTargets(p)
	}
	n := 3
	if p.BootFirmware != nil && p.BootFirmware.Upgrade {
		n += 2
	}
	if p.Storage == nil || p.Storage.Validate(p.Device.Geometry) != nil || requireHash(p.InstallationSHA256) != nil || len(p.Operations) != n || len(p.Protected) == 0 {
		return errors.New("неполный stock plan")
	}
	s := p.Storage
	if s.DataMode == "format" && p.BackupWaived {
		return errors.New("очистка userdata требует бэкапа и в quick mode")
	}
	for i, part := range []Partition{s.Data, s.Root, s.Boot} {
		index := i
		if i == 2 {
			index = len(p.Operations) - 1
		}
		op := p.Operations[index]
		want := []string{"data.ext4", "rootfs.ext4", "boot.img"}[i]
		if op.File != want || op.Range.Area != part.Range.Area || op.Range.Offset != part.Range.Offset || op.Range.Length > part.Range.Length || i < 2 && op.Range.Length != part.Range.Length {
			return errors.New("stock plan выходит за boot/system/userdata или меняет порядок")
		}
	}
	expected := protection(p.Guards, p.Operations)
	if Hash(expected) != Hash(p.Protected) {
		return errors.New("неполное покрытие critical ranges")
	}
	return nil
}
func verifyStockExecution(ctx context.Context, d Reader, p InstallPlan, images string) error {
	if p.qualification == nil || !canonicalStockProfile(*p.qualification) {
		return ErrUnsupported
	}
	gpt, e := ReadGPT(ctx, d, p.Device)
	if e != nil {
		return e
	}
	fresh := HardwareReport{Device: p.Device, GPT: gpt}
	if !supportedLayout(fresh, "stock") {
		return errors.New("исходная разметка изменилась")
	}
	if p.Storage.Source == "microstick" {
		fresh.Boot.Mode = "lk2nd-23.1"
	}
	s, e := StockStorage(fresh, p.Storage.DataMode == "format", p.Storage.EraseConfirmed)
	if e != nil {
		return e
	}
	if Hash(s) != Hash(p.Storage) {
		return errors.New("storage изменён перед записью")
	}
	h, e := HashFile(filepath.Join(images, "Preparation.json"))
	if e != nil {
		return e
	}
	if h != p.InstallationSHA256 {
		return errors.New("installation receipt изменён перед записью")
	}
	return verifyStockImages(images, s, p.qualification.Bundle["dtb"])
}
