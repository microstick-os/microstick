package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"microstick.dev/tools/internal/l10n"
)

type USBConnection struct {
	Path      string
	Transport string
	State     string
	serial    string
}

func (d USBConnection) Ready() error {
	switch d.State {
	case "device", "":
		return nil
	case "unauthorized":
		return errors.New(l10n.Text("ui.adb_unauthorized"))
	default:
		return errors.New(l10n.Text("ui.adb_offline"))
	}
}

func (a adbHost) devices(ctx context.Context) ([]USBConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	text, e := a.text(ctx, "host:devices-l")
	if errors.Is(e, syscall.ECONNREFUSED) {
		if binary, err := exec.LookPath("adb"); err == nil {
			start, stop := context.WithTimeout(ctx, 3*time.Second)
			e = exec.CommandContext(start, binary, "start-server").Run()
			stop()
			if e != nil {
				return nil, fmt.Errorf("ADB server could not start: %w", e)
			}
			text, e = a.text(ctx, "host:devices-l")
		} else {
			return nil, nil
		} // ADB is optional for EDL-only operation.
	}
	if e != nil {
		return nil, e
	}
	var out []USBConnection
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var usb string
		for _, field := range fields[2:] {
			if strings.HasPrefix(field, "usb:") {
				usb = strings.TrimPrefix(field, "usb:")
				break
			}
		}
		if usb == "" {
			devpath, err := a.text(ctx, "host-serial:"+fields[0]+":get-devpath")
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			if strings.HasPrefix(devpath, "usb:") {
				usb = strings.TrimPrefix(strings.TrimSpace(devpath), "usb:")
			}
		}
		if usb != "" {
			out = append(out, USBConnection{Path: "@" + usb, Transport: "adb", State: fields[1], serial: fields[0]})
		}
	}
	return out, nil
}

func edlConnections(root string) ([]USBConnection, error) {
	paths, e := filepath.Glob(filepath.Join(root, "*"))
	if e != nil {
		return nil, e
	}
	var out []USBConnection
	for _, path := range paths {
		vendor, e := os.ReadFile(filepath.Join(path, "idVendor"))
		if e != nil {
			continue
		}
		product, e := os.ReadFile(filepath.Join(path, "idProduct"))
		if e != nil {
			continue
		}
		if strings.TrimSpace(string(vendor)) == "05c6" && strings.TrimSpace(string(product)) == "9008" {
			out = append(out, USBConnection{Path: "@" + filepath.Base(path), Transport: "edl"})
		}
	}
	return out, nil
}

// USBConnections only enumerates transports. It reads no device tree or storage
// and needs no compiled helper, firmware download or programmer upload.
func USBConnections(ctx context.Context) ([]USBConnection, error) {
	return usbConnections(ctx, "/sys/bus/usb/devices", adbHost{"127.0.0.1:5037"})
}
func usbConnections(ctx context.Context, sysfs string, a adbHost) ([]USBConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	edl, e := edlConnections(sysfs)
	if e != nil {
		return nil, e
	}
	adb, e := a.devices(ctx)
	if e != nil && (len(edl) == 0 || ctx.Err() == context.Canceled) {
		return nil, e
	}
	out := append(edl, adb...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
