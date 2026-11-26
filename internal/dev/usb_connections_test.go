package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"microstick.dev/tools/internal/flasher"
)

func adbRequest(c io.Reader) string {
	var size [4]byte
	if _, e := io.ReadFull(c, size[:]); e != nil {
		return ""
	}
	var n int
	if _, e := fmt.Sscanf(string(size[:]), "%x", &n); e != nil || n > 65535 {
		return ""
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(c, b); e != nil {
		return ""
	}
	return string(b)
}
func serveADB(t *testing.T, serve func(net.Conn)) adbHost {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() { stop := context.AfterFunc(ctx, func() { c.Close() }); defer stop(); defer c.Close(); serve(c) }()
		}
	}()
	return adbHost{ln.Addr().String()}
}

func TestUSBDiscoveryEnumeratesBothTransportsWithoutCapture(t *testing.T) {
	sysfs := t.TempDir()
	path := filepath.Join(sysfs, "2-4.3")
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "idVendor"), []byte("05c6\n"), 0600)
	os.WriteFile(filepath.Join(path, "idProduct"), []byte("9008\n"), 0600)
	var requests atomic.Int32
	a := serveADB(t, func(c net.Conn) {
		if adbRequest(c) != "host:devices-l" {
			t.Error("discovery requested a transport or tree")
			return
		}
		requests.Add(1)
		body := "phone unauthorized usb:1-2\nboard device usb:3-1 product:synthetic\n"
		fmt.Fprintf(c, "OKAY%04x%s", len(body), body)
	})
	got, e := usbConnections(context.Background(), sysfs, a)
	if e != nil || len(got) != 3 {
		t.Fatal(got, e)
	}
	if got[0].Path != "@1-2" || got[0].Ready() == nil || got[1].Transport != "edl" || got[2].Transport != "adb" || requests.Load() != 1 {
		t.Fatal(got, requests.Load())
	}
}

func TestADBTotalDeadlineAndCancellationCloseBlockedSockets(t *testing.T) {
	for _, phase := range []string{"discovery", "transport", "sync", "list", "read"} {
		for _, kind := range []string{"cancel", "deadline"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				blocked := make(chan struct{}, 1)
				a := serveADB(t, func(c net.Conn) {
					first := adbRequest(c)
					if phase != "discovery" {
						if !strings.HasPrefix(first, "host:transport:") {
							return
						}
						c.Write([]byte("OKAY"))
						if phase != "transport" {
							if adbRequest(c) != "sync:" {
								return
							}
							if phase != "sync" {
								c.Write([]byte("OKAY"))
								var h [8]byte
								io.ReadFull(c, h[:])
								var b [4096]byte
								c.Read(b[:])
							}
						}
					}
					blocked <- struct{}{}
					io.Copy(io.Discard, c)
				})
				ctx, cancel := context.WithCancel(context.Background())
				if kind == "deadline" {
					ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				}
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var e error
					if phase == "discovery" {
						_, e = a.devices(ctx)
					} else {
						var s *adbSync
						s, e = a.sync(ctx, "synthetic")
						if e == nil {
							defer s.Close()
							if phase == "list" {
								_, e = s.list("/proc/device-tree")
							} else {
								_, e = s.read("/sys/firmware/fdt")
							}
						}
					}
					done <- e
				}()
				select {
				case <-blocked:
				case <-time.After(time.Second):
					t.Fatal("request did not block")
				}
				started := time.Now()
				if kind == "cancel" {
					cancel()
				}
				select {
				case e := <-done:
					expected := context.Canceled
					if kind == "deadline" {
						expected = context.DeadlineExceeded
					}
					if !errors.Is(e, expected) {
						t.Fatal("lost context cause", e)
					}
				case <-time.After(time.Second):
					t.Fatal("blocked socket ignored cancellation")
				}
				if time.Since(started) > time.Second {
					t.Fatal("cancellation exceeded bound")
				}
			})
		}
	}
}

func TestCaptureLiveTreePersistsOnlySelectedUSBConnection(t *testing.T) {
	a, _ := fakeADB(t, nil)
	p := Paths{Work: t.TempDir()}
	var progress int
	live, e := captureLiveTree(context.Background(), p, "@3-2.4", a, func(p flasher.Progress) { progress++ })
	if e != nil || live == nil || progress < 2 {
		t.Fatal("capture", e, progress)
	}
	saved, e := LoadLiveTree(p, "@3-2.4")
	if e != nil || saved == nil || string(saved.DTB) != string(live.DTB) {
		t.Fatal("capture not retained", e)
	}
	if other, e := captureLiveTree(context.Background(), p, "@3-2.5", a); e != nil || other != nil {
		t.Fatal("wrong device captured", e)
	}
}
