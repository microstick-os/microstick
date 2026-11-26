package dev

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"microstick.dev/tools/internal/flasher"
)

type adbRequests struct {
	sync.Mutex
	entries []string
}

func (r *adbRequests) add(s string) { r.Lock(); defer r.Unlock(); r.entries = append(r.entries, s) }
func (r *adbRequests) snapshot() []string {
	r.Lock()
	defer r.Unlock()
	return append([]string(nil), r.entries...)
}
func fakeADB(t *testing.T, fdt []byte, hierarchy ...map[string]map[string][]byte) (adbHost, *adbRequests) {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	requests := &adbRequests{}
	tree := map[string]map[string][]byte{"/": {"compatible": []byte("qcom,msm8916\x00"), "zero": {}}, "/chosen": {"bootargs": []byte("private\x00")}}
	if len(hierarchy) > 0 {
		tree = hierarchy[0]
	}
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				service := func() string {
					var size [4]byte
					if _, e := io.ReadFull(c, size[:]); e != nil {
						return ""
					}
					var n int
					fmt.Sscanf(string(size[:]), "%x", &n)
					if n < 0 || n > 4096 {
						return ""
					}
					b := make([]byte, n)
					io.ReadFull(c, b)
					return string(b)
				}
				initial := service()
				if initial == "host:devices-l" {
					body := "synthetic device usb:3-2.4 product:synthetic\n"
					fmt.Fprintf(c, "OKAY%04x%s", len(body), body)
					return
				}
				if initial != "host:transport:synthetic" {
					return
				}
				requests.add("CONNECT")
				c.Write([]byte("OKAY"))
				if service() != "sync:" {
					return
				}
				c.Write([]byte("OKAY"))
				for {
					var header [8]byte
					if _, e := io.ReadFull(c, header[:]); e != nil {
						return
					}
					n := binary.LittleEndian.Uint32(header[4:])
					if n > 4096 {
						return
					}
					b := make([]byte, n)
					io.ReadFull(c, b)
					remote := string(b)
					requests.add(string(header[:4]) + " " + remote)
					packet := func(id string, b []byte) {
						h := make([]byte, 8)
						copy(h, id)
						binary.LittleEndian.PutUint32(h[4:], uint32(len(b)))
						c.Write(append(h, b...))
					}
					if string(header[:4]) == "RECV" {
						if remote == "/sys/firmware/fdt" {
							if fdt == nil {
								packet("FAIL", []byte("not found"))
								return
							}
							packet("DATA", fdt)
							packet("DONE", nil)
							continue
						}
						rel := strings.TrimPrefix(remote, "/proc/device-tree")
						v, ok := tree[filepath.Dir(rel)][filepath.Base(rel)]
						if !ok {
							packet("FAIL", []byte("unknown"))
							return
						}
						packet("DATA", v)
						packet("DONE", nil)
						continue
					}
					if string(header[:4]) != "LIST" {
						return
					}
					type entry struct {
						name string
						mode uint32
					}
					var entries []entry
					rel := strings.TrimPrefix(remote, "/proc/device-tree")
					if rel == "" {
						rel = "/"
					}
					properties, ok := tree[rel]
					if !ok {
						return
					}
					for name := range properties {
						entries = append(entries, entry{name, 0100444})
					}
					for child := range tree {
						if child != "/" && filepath.Dir(child) == rel {
							entries = append(entries, entry{filepath.Base(child), 0040555})
						}
					}
					for _, entry := range entries {
						h := make([]byte, 20)
						copy(h, "DENT")
						binary.LittleEndian.PutUint32(h[4:], entry.mode)
						binary.LittleEndian.PutUint32(h[16:], uint32(len(entry.name)))
						c.Write(append(h, []byte(entry.name)...))
					}
					h := make([]byte, 20)
					copy(h, "DONE")
					c.Write(h)
				}
			}()
		}
	}()
	return adbHost{ln.Addr().String()}, requests
}
func TestLiveDeviceTreeReadUsesSyncWithoutRootOrShell(t *testing.T) {
	a, _ := fakeADB(t, nil)
	b, e := readLiveTree(context.Background(), a, "synthetic")
	if e != nil {
		t.Fatal(e)
	}
	v, e := flasher.ParseDTB(b, "active", 0)
	if e != nil || len(v.Compatible) != 1 || v.Compatible[0] != "qcom,msm8916" {
		t.Fatal("rootless tree reconstruction", e)
	}
	a, _ = fakeADB(t, b)
	direct, e := readLiveTree(context.Background(), a, "synthetic")
	if e != nil || !bytes.Equal(b, direct) {
		t.Fatal("direct FDT path", e)
	}
}
func TestSavedLiveDeviceTreeIsBoundToUSBPath(t *testing.T) {
	p := Paths{Work: t.TempDir()}
	dir := filepath.Join(p.Work, "buildroot-private/live-device-trees")
	os.MkdirAll(dir, 0700)
	live := LiveTree{USBPath: "@3-2.4", DTB: []byte("synthetic")}
	writeJSONAtomic(filepath.Join(dir, digest([]byte(live.USBPath))+".json"), live)
	got, e := LoadLiveTree(p, live.USBPath)
	if e != nil || got == nil || !bytes.Equal(got.DTB, live.DTB) {
		t.Fatal(e)
	}
	if got, e = LoadLiveTree(p, "@3-2.5"); e != nil || got != nil {
		t.Fatal("different connection selected captured DT", e)
	}
	live.USBPath = "@3-2.5"
	writeJSONAtomic(filepath.Join(dir, digest([]byte("@3-2.4"))+".json"), live)
	if _, e = LoadLiveTree(p, "@3-2.4"); e == nil || !strings.Contains(e.Error(), "connection") {
		t.Fatal("mismatched record accepted", e)
	}
}

func TestLargeRootlessTreeUsesOneSyncConnectionAndRetainsAllProperties(t *testing.T) {
	tree := map[string]map[string][]byte{"/": {"compatible": []byte("qcom,msm8916\x00")}}
	for node := 0; node < 250; node++ {
		properties := map[string][]byte{}
		for property := 0; property < 12; property++ {
			properties[fmt.Sprintf("property-%d", property)] = []byte(fmt.Sprintf("value-%d-%d\x00", node, property))
		}
		tree[fmt.Sprintf("/node-%03d", node)] = properties
	}
	a, requests := fakeADB(t, nil, tree)
	events := 0
	var last uint64
	data, e := readLiveTree(context.Background(), a, "synthetic", func(p flasher.Progress) { events++; last = p.Done })
	if e != nil {
		t.Fatal(e)
	}
	expected, e := flasher.DTBFromProperties(tree)
	if e != nil || !bytes.Equal(data, expected) {
		t.Fatal("tree data incomplete", e)
	}
	connections := 0
	for _, request := range requests.snapshot() {
		if request == "CONNECT" {
			connections++
		}
	}
	if connections != 2 {
		t.Fatal("transport reopened per property", connections)
	}
	if events < 3000 || last != 3001 {
		t.Fatal("progress incomplete", events, last)
	}
}
