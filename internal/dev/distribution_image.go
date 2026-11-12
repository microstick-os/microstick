package dev

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Rebuild the filesystem from a clean tar stream. Deleting private files from
// an ext4 copy would leave their old bytes in unallocated storage.
func publicRootfsTar(p Paths, source, destination string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.Create(destination)
	if e != nil {
		return e
	}
	defer out.Close()
	tr, tw := tar.NewReader(in), tar.NewWriter(out)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("invalid rootfs archive path")
		}
		if privateReleaseFile(name) {
			continue
		}
		var replacement []byte
		if name == "etc/shadow" {
			b, e := io.ReadAll(io.LimitReader(tr, 1<<20))
			if e != nil {
				return e
			}
			rows := strings.Split(string(b), "\n")
			for i, row := range rows {
				fields := strings.Split(row, ":")
				if len(fields) == 9 {
					fields[1] = "!"
					rows[i] = strings.Join(fields, ":")
				} else if row != "" {
					return errors.New("malformed shadow in base image")
				}
			}
			replacement = []byte(strings.Join(rows, "\n"))
		}
		if name == "etc/machine-id" {
			replacement = []byte{}
		}
		if name == "usr/share/microstick/build.json" {
			var metadata map[string]any
			decoder := json.NewDecoder(io.LimitReader(tr, 1<<20))
			if e = decoder.Decode(&metadata); e != nil {
				return e
			}
			delete(metadata, "device_only")
			delete(metadata, "device_id")
			replacement, e = json.MarshalIndent(metadata, "", "  ")
			if e != nil {
				return e
			}
			replacement = append(replacement, '\n')
		}
		for _, file := range []string{"network.conf", "usb.conf", "lte.properties", "ttl.conf", "wpa_supplicant.conf"} {
			if name == "etc/microstick/"+file {
				replacement, e = os.ReadFile(filepath.Join(p.External, "board/msm8916/overlay", name))
				if e != nil {
					return e
				}
			}
		}
		if replacement != nil {
			h.Size = int64(len(replacement))
		}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if replacement != nil {
			_, e = io.Copy(tw, bytes.NewReader(replacement))
		} else {
			_, e = io.Copy(tw, tr)
		}
		if e != nil {
			return e
		}
	}
	return tw.Close()
}

func privateReleaseFile(name string) bool {
	for _, prefix := range []string{"data/", "root/", "home/", "var/log/", "var/lib/microstick/", "usr/share/microstick/hardware/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, file := range []string{"etc/microstick/hostapd.conf", "etc/microstick/imei.json", "etc/microstick/storage.json", "etc/microstick/data-partuuid", "etc/microstick/data-auth.json", "usr/share/microstick/default-imei.json", "usr/share/microstick/installation.json", installedReleasePath[1:], "usr/share/microstick/hardware-assets.json", "usr/share/microstick/modem-guard.sha256", "var/lib/systemd/random-seed"} {
		if name == file {
			return true
		}
	}
	base := path.Base(name)
	return strings.HasPrefix(name, "lib/firmware/modem.") || strings.HasPrefix(name, "lib/firmware/wcnss.") || base == "WCNSS_qcom_wlan_nv.bin" || base == "default-auth.json" || base == "authorized_keys" || strings.HasPrefix(base, "ssh_host_") || strings.HasSuffix(base, "_host_key") || base == "credentials.json" || base == "sessions.json"
}
