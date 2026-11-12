package dev

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func prepareUpdateBase(images, state, bundle, old string) error {
	if _, e := InstalledFlasherRelease(old); e != nil {
		return e
	}
	value, e := imageDefault(old)
	if e != nil || !ValidIMEI(value) {
		return errors.New("installed personalization unavailable")
	}
	m, e := verifyManifest(images)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(bundle, 0700); e != nil {
		return e
	}
	for _, v := range m.Artifacts {
		if e = copyFile(filepath.Join(images, v.Path), filepath.Join(bundle, v.Path), 0600); e != nil {
			return e
		}
	}
	base := map[string]string{}
	for _, n := range []string{"boot.img", "rootfs.ext4"} {
		base[n], e = hashFile(filepath.Join(images, n))
		if e != nil {
			return e
		}
	}
	record := Installation{Version: 1, ID: digest([]byte(value))[:32], Created: time.Now().UTC().Format(time.RFC3339Nano), TAC: value[:8], IMEI: value, Base: base}
	if imageExists(old, "/usr/share/microstick/installation.json") {
		b, e := imageBytes(old, "/usr/share/microstick/installation.json")
		if e != nil {
			return e
		}
		var installed Installation
		if json.Unmarshal(b, &installed) != nil || installed.IMEI != value {
			return errors.New("installed record differs from identity")
		}
		record.ID = installed.ID
		record.Created = installed.Created
	}
	if e = writeJSONAtomic(state, record); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(bundle, "installation.json"), record, 0600); e != nil {
		return e
	}
	return finalize(bundle, Manifest{Version: 1, Personalized: true, InstallationID: record.ID, Base: base})
}
func preserveUpdateIdentity(old, image, temp string) error {
	defaults, e := imageBytes(old, defaultIMEIPath)
	if e != nil {
		return e
	}
	local := filepath.Join(temp, "update-identity")
	if e = os.WriteFile(local, defaults, 0600); e != nil {
		return e
	}
	if e = imagePut(image, defaultIMEIPath, local, 0100600); e != nil {
		return e
	}
	const auth = "/usr/share/microstick/default-plugins/web/default-auth.json"
	if imageExists(old, auth) && imageExists(image, filepath.Dir(auth)) {
		b, e := imageBytes(old, auth)
		if e != nil {
			return e
		}
		if e = os.WriteFile(local, b, 0600); e != nil {
			return e
		}
		if e = imagePut(image, auth, local, 0100600); e != nil {
			return e
		}
	}
	original, e := imageBytes(old, "/etc/shadow")
	if e != nil {
		return e
	}
	current, e := imageBytes(image, "/etc/shadow")
	if e != nil {
		return e
	}
	root := ""
	for _, row := range strings.Split(string(original), "\n") {
		if strings.HasPrefix(row, "root:") {
			root = row
		}
	}
	if len(strings.Split(root, ":")) != 9 {
		return errors.New("installed root credentials unavailable")
	}
	rows := strings.Split(string(current), "\n")
	count := 0
	for i, row := range rows {
		if strings.HasPrefix(row, "root:") {
			rows[i] = root
			count++
		}
	}
	if count != 1 {
		return errors.New("base root account unavailable")
	}
	if e = os.WriteFile(local, []byte(strings.Join(rows, "\n")), 0600); e != nil {
		return e
	}
	return imagePut(image, "/etc/shadow", local, 0100600)
}
