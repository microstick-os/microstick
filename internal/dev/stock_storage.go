package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"microstick.dev/tools/internal/flasher"
)

func WizardLK2nd(p Paths) (string, error) {
	if p.Distribution != "" {
		path := filepath.Join(p.Distribution, "resources/lk2nd.img")
		h, e := hashFile(path)
		if e == nil && h != flasher.LK2ndSHA256 {
			e = errors.New("release boot loader checksum mismatch")
		}
		return path, e
	}
	path := filepath.Join(p.Work, "buildroot-downloads/lk2nd-msm8916-23.1.img")
	return path, Fetch(path, flasher.LK2ndURL, flasher.LK2ndSHA256)
}

// expandExt4 works on staged regular files only. ext4 uses whole 4KiB blocks;
// a possible final partial block is zero padding inside the unchanged partition.
func expandExt4(image string, capacity uint64) error {
	st, e := os.Lstat(image)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || capacity < uint64(st.Size()) || capacity > 1<<44 || capacity%512 != 0 {
		return errors.New("image does not fit existing partition")
	}
	if _, e = command("", nil, "e2fsck", "-fn", image); e != nil {
		return errors.New("staged ext4 is not clean")
	}
	// Explicit sectors avoid dependence on the filesystem block size.
	if _, e = command("", nil, "resize2fs", image, fmt.Sprint(capacity/4096*8)+"s"); e != nil {
		return errors.New("offline ext4 expansion failed")
	}
	if e = os.Truncate(image, int64(capacity)); e != nil {
		return e
	}
	_, e = command("", nil, "e2fsck", "-fn", image)
	return e
}
func prepareStockStorage(p Paths, bundle, temp string, o WizardOptions, auth []byte) error {
	s := o.Storage
	// Geometry validation is also performed against the actual device by flasher.
	if s.Recipe != flasher.StockRecipe || s.Root.GUID != o.RootPARTUUID || (s.DataMode == "format" && !s.EraseConfirmed) {
		return errors.New("unconfirmed stock storage")
	}
	root := filepath.Join(bundle, "rootfs.ext4")
	local := filepath.Join(temp, "storage-conf")
	if e := os.WriteFile(local, []byte(s.Data.GUID+"\n"), 0600); e != nil {
		return e
	}
	if e := imagePut(root, "/etc/microstick/data-partuuid", local, 0100600); e != nil {
		return e
	}
	// No plugin may fall back to data hidden under a failed mount.
	if e := removeImageTree(root, "/data", 0); e != nil {
		return e
	}
	if _, e := debugImage(root, "mkdir /data", true); e != nil {
		return e
	}
	if _, e := debugImage(root, "set_inode_field /data mode 040000", true); e != nil {
		return e
	}
	// /data remains on the device. Only a small one-time transaction is staged
	// in system; no userdata image is created, even for a preserve reinstall.
	var record Installation
	if e := readJSON(filepath.Join(bundle, "installation.json"), &record); e != nil {
		return e
	}
	seed := digest([]byte(record.ID + o.RequestBinding))[:32]
	uuid := seed[:8] + "-" + seed[8:12] + "-" + seed[12:16] + "-" + seed[16:20] + "-" + seed[20:]
	transaction := digest([]byte(record.ID + o.RequestBinding + fmt.Sprint(o.Release) + o.SSID))[:32]
	contract := map[string]any{"transaction": transaction, "schema_version": 1, "installation": record.ID, "mode": s.DataMode, "partuuid": s.Data.GUID, "bytes": s.Data.Range.Length, "fs_uuid": uuid}
	b, e := json.Marshal(contract)
	if e != nil {
		return e
	}
	if e = os.WriteFile(local, append(b, '\n'), 0600); e != nil {
		return e
	}
	if e = imagePut(root, "/etc/microstick/storage.json", local, 0100600); e != nil {
		return e
	}
	// Never migrate a previous installation's first-boot checkpoints.
	if e = removeImageTree(root, "/var/lib/microstick/first-boot", 0); e != nil {
		return e
	}
	if e = removeImageTree(root, "/etc/microstick/data-auth.json", 0); e != nil {
		return e
	}
	if o.Storage.DataMode == "preserve" {
		if e = stagePluginRefresh(o.OldRootfs, root, o.OldData, temp, o.Update); e != nil {
			return e
		}
		if !o.Update {
			if e = os.WriteFile(local, auth, 0600); e != nil {
				return e
			}
			if e = imagePut(root, "/etc/microstick/data-auth.json", local, 0100600); e != nil {
				return e
			}
		}
	}
	lk, e := WizardLK2nd(p)
	if e != nil {
		return e
	}
	prefix, e := os.ReadFile(lk)
	if e != nil {
		return e
	}
	bootPath := filepath.Join(bundle, "boot.img")
	inner, e := os.ReadFile(bootPath)
	if e != nil {
		return e
	}
	composite, e := flasher.CompositeBoot(prefix, inner, s.Boot.Range.Length)
	if e != nil {
		return e
	}
	return os.WriteFile(bootPath, composite, 0600)
}
