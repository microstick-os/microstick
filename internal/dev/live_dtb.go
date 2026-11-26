package dev

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"microstick.dev/tools/boards"
	"microstick.dev/tools/internal/flasher"
	"microstick.dev/tools/internal/l10n"
)

const liveTreeLimit = 8 << 20

type LiveTree struct {
	USBPath string `json:"usb_path"`
	DTB     []byte `json:"dtb"`
}
type adbHost struct{ address string }

// Cancellation closes an established socket as well as an in-flight dial.
// Each operation renews the idle deadline within the caller's total budget.
type adbConnection struct {
	net.Conn
	ctx  context.Context
	stop func() bool
}

func (c *adbConnection) deadline() error {
	if e := c.ctx.Err(); e != nil {
		return e
	}
	d := time.Now().Add(10 * time.Second)
	if end, ok := c.ctx.Deadline(); ok && end.Before(d) {
		d = end
	}
	return c.Conn.SetDeadline(d)
}
func (c *adbConnection) Read(b []byte) (int, error) {
	if e := c.deadline(); e != nil {
		return 0, e
	}
	n, e := c.Conn.Read(b)
	if c.ctx.Err() != nil {
		e = c.ctx.Err()
	} else if d, ok := c.ctx.Deadline(); e != nil && ok && !time.Now().Before(d) {
		e = context.DeadlineExceeded
	}
	return n, e
}
func (c *adbConnection) Write(b []byte) (int, error) {
	if e := c.deadline(); e != nil {
		return 0, e
	}
	n, e := c.Conn.Write(b)
	if c.ctx.Err() != nil {
		e = c.ctx.Err()
	} else if d, ok := c.ctx.Deadline(); e != nil && ok && !time.Now().Before(d) {
		e = context.DeadlineExceeded
	}
	return n, e
}
func (c *adbConnection) Close() error { c.stop(); return c.Conn.Close() }

func (a adbHost) connect(ctx context.Context, service string) (net.Conn, error) {
	raw, e := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", a.address)
	if e != nil {
		return nil, e
	}
	c := &adbConnection{Conn: raw, ctx: ctx, stop: context.AfterFunc(ctx, func() { raw.Close() })}
	if e = adbService(c, service); e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}
func adbService(c io.ReadWriter, service string) error {
	if len(service) > 65535 {
		return errors.New("ADB service too long")
	}
	if _, e := fmt.Fprintf(c, "%04x%s", len(service), service); e != nil {
		return e
	}
	var status [4]byte
	if _, e := io.ReadFull(c, status[:]); e != nil {
		return e
	}
	if string(status[:]) != "OKAY" {
		return errors.New("ADB service unavailable")
	}
	return nil
}
func (a adbHost) text(ctx context.Context, service string) (string, error) {
	c, e := a.connect(ctx, service)
	if e != nil {
		return "", e
	}
	defer c.Close()
	var b [4]byte
	if _, e = io.ReadFull(c, b[:]); e != nil {
		return "", e
	}
	n, e := strconv.ParseUint(string(b[:]), 16, 32)
	if e != nil || n > 1<<20 {
		return "", errors.New("invalid ADB response")
	}
	out := make([]byte, n)
	_, e = io.ReadFull(c, out)
	return string(out), e
}

type adbEntry struct {
	name string
	mode uint32
}

type adbSync struct{ net.Conn }

func (a adbHost) sync(ctx context.Context, serial string) (*adbSync, error) {
	c, e := a.connect(ctx, "host:transport:"+serial)
	if e != nil {
		return nil, e
	}
	if e = adbService(c, "sync:"); e != nil {
		c.Close()
		return nil, e
	}
	return &adbSync{c}, nil
}
func (s *adbSync) request(operation, remote string) error {
	if len(remote) > 4096 {
		return errors.New("ADB path too long")
	}
	header := make([]byte, 8)
	copy(header, operation)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(remote)))
	_, e := s.Write(append(header, []byte(remote)...))
	return e
}
func (s *adbSync) failure(n uint32) error {
	if n > 4096 {
		return errors.New("invalid ADB failure response")
	}
	if _, e := io.CopyN(io.Discard, s, int64(n)); e != nil {
		return e
	}
	return errors.New("ADB denied the device-tree request")
}
func (s *adbSync) list(remote string) ([]adbEntry, error) {
	if e := s.request("LIST", remote); e != nil {
		return nil, e
	}
	var out []adbEntry
	for len(out) < 16384 {
		var h [20]byte
		if _, e := io.ReadFull(s, h[:8]); e != nil {
			return nil, e
		}
		if string(h[:4]) == "FAIL" {
			return nil, s.failure(binary.LittleEndian.Uint32(h[4:]))
		}
		if _, e := io.ReadFull(s, h[8:]); e != nil {
			return nil, e
		}
		if string(h[:4]) == "DONE" {
			return out, nil
		}
		if string(h[:4]) != "DENT" {
			return nil, errors.New("ADB cannot list live device tree")
		}
		n := binary.LittleEndian.Uint32(h[16:])
		if n == 0 || n > 255 {
			return nil, errors.New("invalid live DT filename")
		}
		b := make([]byte, n)
		if _, e := io.ReadFull(s, b); e != nil {
			return nil, e
		}
		name := string(b)
		if name == "." || name == ".." {
			continue
		}
		if strings.ContainsAny(name, "/\x00") {
			return nil, errors.New("invalid live DT filename")
		}
		out = append(out, adbEntry{name, binary.LittleEndian.Uint32(h[4:])})
	}
	return nil, errors.New("too many live DT entries")
}
func (s *adbSync) read(remote string) ([]byte, error) {
	if e := s.request("RECV", remote); e != nil {
		return nil, e
	}
	out := []byte{}
	for {
		var h [8]byte
		if _, e := io.ReadFull(s, h[:]); e != nil {
			return nil, e
		}
		n := binary.LittleEndian.Uint32(h[4:])
		if string(h[:4]) == "FAIL" {
			return nil, s.failure(n)
		}
		if string(h[:4]) == "DONE" {
			return out, nil
		}
		if string(h[:4]) != "DATA" || n > 65536 || len(out)+int(n) > liveTreeLimit {
			return nil, errors.New("live DT read failed or exceeds limit")
		}
		start := len(out)
		out = append(out, make([]byte, n)...)
		if _, e := io.ReadFull(s, out[start:]); e != nil {
			return nil, e
		}
	}
}
func readLiveTree(ctx context.Context, a adbHost, serial string, emit ...flasher.Emit) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	progress := func(nodes, properties int) {
		if len(emit) > 0 && emit[0] != nil {
			emit[0](flasher.Progress{Stage: "active-dtb", Done: uint64(properties), Message: fmt.Sprintf(l10n.Text("ui.reading_device_tree_progress"), nodes, properties)})
		}
	}
	progress(0, 0)
	s, e := a.sync(ctx, serial)
	if e != nil {
		return nil, e
	}
	b, e := s.read("/sys/firmware/fdt")
	s.Close()
	if e == nil && flasher.ValidateActiveDTB(b) == nil {
		progress(1, 1)
		return b, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s, e = a.sync(ctx, serial)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	props := map[string]map[string][]byte{}
	total, properties := 0, 0
	var walk func(string, int) error
	walk = func(rel string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if depth > 64 || len(props) >= 8192 {
			return errors.New("live DT hierarchy limit")
		}
		remote := "/proc/device-tree"
		if rel != "/" {
			remote += rel
		}
		entries, e := s.list(remote)
		if e != nil {
			return e
		}
		props[rel] = map[string][]byte{}
		for _, entry := range entries {
			name := path.Join(rel, entry.name)
			switch entry.mode & 0170000 {
			case 0040000:
				if e = walk(name, depth+1); e != nil {
					return e
				}
			case 0100000:
				b, e := s.read("/proc/device-tree" + name)
				if e != nil {
					return e
				}
				total += len(b)
				properties++
				if total > liveTreeLimit || properties > 65536 {
					return errors.New("live DT total size limit")
				}
				props[rel][entry.name] = b
				progress(len(props), properties)
			default:
				return errors.New("unsupported live DT entry type")
			}
		}
		return nil
	}
	if e := walk("/", 0); e != nil {
		return nil, e
	}
	return flasher.DTBFromProperties(props)
}

// CaptureLiveTree reads only the selected USB device's DT interfaces. It does
// not request root, issue shell commands, reboot or change USB configuration.
func CaptureLiveTree(ctx context.Context, p Paths, usbPath string, emit ...flasher.Emit) (*LiveTree, error) {
	a := adbHost{"127.0.0.1:5037"}
	return captureLiveTree(ctx, p, usbPath, a, emit...)
}
func captureLiveTree(ctx context.Context, p Paths, usbPath string, a adbHost, emit ...flasher.Emit) (*LiveTree, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	found, e := a.devices(ctx)
	if e != nil {
		return nil, e
	}
	selected := found[:0]
	for _, device := range found {
		if usbPath == "" || usbPath == device.Path {
			selected = append(selected, device)
		}
	}
	found = selected
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) > 1 {
		return nil, errors.New("Select a USB connection before reading its active device tree")
	}
	if e = found[0].Ready(); e != nil {
		return nil, e
	}
	b, e := readLiveTree(ctx, a, found[0].serial, emit...)
	if e != nil {
		return nil, fmt.Errorf("Active device tree could not be read: %w", e)
	}
	if e = flasher.ValidateActiveDTB(b); e != nil {
		return nil, fmt.Errorf("Active device tree is invalid: %w", e)
	}
	live := &LiveTree{found[0].Path, b}
	dir := filepath.Join(p.Work, "buildroot-private/live-device-trees")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if e = writeJSONAtomic(filepath.Join(dir, digest([]byte(live.USBPath))+".json"), live); e != nil {
		return nil, e
	}
	return live, nil
}
func LoadLiveTree(p Paths, usbPath string) (*LiveTree, error) {
	if usbPath == "" {
		return nil, nil
	}
	var live LiveTree
	e := readJSON(filepath.Join(p.Work, "buildroot-private/live-device-trees", digest([]byte(usbPath))+".json"), &live)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if live.USBPath != usbPath || len(live.DTB) > liveTreeLimit {
		return nil, errors.New("saved live DT connection differs")
	}
	return &live, nil
}

// DetectAutomaticProfile uses the common runtime inputs without a model entry.
func DetectAutomaticProfile(p Paths, r flasher.HardwareReport) (flasher.HardwareProfile, error) {
	var inputs Manifest
	if e := readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &inputs); e != nil {
		return flasher.HardwareProfile{}, e
	}
	if e := verifyEntries(p.Hardware, inputs.Artifacts); e != nil {
		return flasher.HardwareProfile{}, e
	}
	raw, e := boards.Files.ReadFile("bundles/linux17.json")
	if e != nil {
		return flasher.HardwareProfile{}, e
	}
	var runtime baseline
	if e = json.Unmarshal(raw, &runtime); e != nil {
		return flasher.HardwareProfile{}, e
	}
	base, e := os.ReadFile(filepath.Join(p.Hardware, "device-tree/boot-image.dtb"))
	if e != nil {
		return flasher.HardwareProfile{}, e
	}
	kernel, e := hashFile(filepath.Join(p.Hardware, "boot/kernel.gz"))
	if e != nil {
		return flasher.HardwareProfile{}, e
	}
	if kernel != runtime.Files["boot/kernel.gz"].SHA256 {
		return flasher.HardwareProfile{}, errors.New("Runtime kernel differs from the Linux #17 bundle")
	}
	if flasher.HashBytes(base) != flasher.OriginalLinux17DTB {
		return flasher.HardwareProfile{}, errors.New("Automatic mapping requires the common Linux #17 DT template")
	}
	mods := map[string]string{}
	prefix := filepath.Join(p.Hardware, "kernel/kernel-and-modules-tree/lib/modules", release)
	selected, e := ModuleSelection(prefix)
	if e != nil {
		return flasher.HardwareProfile{}, e
	}
	for _, name := range selected {
		h, e := hashFile(filepath.Join(prefix, name))
		if e != nil {
			return flasher.HardwareProfile{}, e
		}
		if h != runtime.Files["kernel/kernel-and-modules-tree/lib/modules/"+release+"/"+name].SHA256 {
			return flasher.HardwareProfile{}, fmt.Errorf("Runtime module differs from the Linux #17 bundle: %s", name)
		}
		mods[name] = h
	}
	mba, e := hashFile(filepath.Join(p.Hardware, "firmware/dsp/mba.mbn"))
	if e != nil {
		return flasher.HardwareProfile{}, e
	}
	if mba != "bcd6100886b10d0d36ce17b1ae70fdc4fd429ab3a0337e0b7cef9aa9b04b60c4" {
		return flasher.HardwareProfile{}, errors.New("Runtime MBA differs from its resource hash")
	}
	return flasher.AutomaticProfile(r, flasher.AutomaticInputs{ActiveDTB: r.ActiveDTB, BaseDTB: base, Bundle: map[string]string{"kernel": kernel, "modules": flasher.Hash(mods), "mba": mba}})
}

// LiveUSBConnections lists selectable ADB USB transports without reading a tree.
func LiveUSBConnections(ctx context.Context) ([]string, error) {
	devices, e := (adbHost{"127.0.0.1:5037"}).devices(ctx)
	if e != nil {
		return nil, e
	}
	var paths []string
	for _, device := range devices {
		paths = append(paths, device.Path)
	}
	return paths, nil
}
