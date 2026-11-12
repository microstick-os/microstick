// Linux host integration tests of the production firmware. Every network tool
// used by shell adapters is replaced in a private fixture; no hardware is used.
package firmware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var external, workspace string

func TestMain(m *testing.M) {
	if os.Getenv("MICROSTICK_TEST_MOCK") == "1" {
		os.Exit(mockTool())
	}
	external, _ = filepath.Abs("../../buildroot-external")
	workspace = os.Getenv("MICROSTICK_WORKSPACE")
	os.Exit(m.Run())
}
func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0700); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func exists(p string) bool { _, e := os.Stat(p); return e == nil }
func js(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func object(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if e := json.Unmarshal([]byte(s), &v); e != nil {
		t.Fatalf("JSON: %v: %s", e, s)
	}
	return v
}
func equal(t *testing.T, a, b any) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %#v, want %#v", a, b)
	}
}
func contains(t *testing.T, s, part string) {
	t.Helper()
	if !strings.Contains(s, part) {
		t.Fatalf("missing %q in %s", part, s)
	}
}
func no(t *testing.T, b bool, why string) {
	t.Helper()
	if b {
		t.Fatal(why)
	}
}
func env(extra map[string]string) []string {
	v := map[string]string{}
	for _, s := range os.Environ() {
		p := strings.SplitN(s, "=", 2)
		v[p[0]] = p[1]
	}
	for k, s := range extra {
		v[k] = s
	}
	out := []string{}
	for k, s := range v {
		out = append(out, k+"="+s)
	}
	return out
}
func run(t *testing.T, e map[string]string, input, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Env = env(e)
	c.Stdin = strings.NewReader(input)
	var errout bytes.Buffer
	c.Stderr = &errout
	b, err := c.Output()
	if err != nil {
		return string(b), fmt.Errorf("%s: %w: %s", filepath.Base(name), err, errout.String())
	}
	return string(b), nil
}
func ok(t *testing.T, e map[string]string, input, name string, args ...string) string {
	t.Helper()
	b, err := run(t, e, input, name, args...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func overlay(name string) string { return filepath.Join(external, "board/msm8916/overlay", name) }
func compile(t *testing.T, name string, sources, packages, flags []string) string {
	t.Helper()
	if workspace == "" {
		t.Fatal("MICROSTICK_WORKSPACE must point to the external build workspace")
	}
	root := t.TempDir()
	inc := filepath.Join(root, "include/cjson")
	if e := os.MkdirAll(inc, 0755); e != nil {
		t.Fatal(e)
	}
	cjson := filepath.Join(workspace, "buildroot-output/build/cjson-1.7.19")
	if e := os.Symlink(filepath.Join(cjson, "cJSON.h"), filepath.Join(inc, "cJSON.h")); e != nil {
		t.Fatal(e)
	}
	args := []string{"-Wall", "-Wextra", "-Werror", "-O1", "-g", "-fsanitize=address,undefined", "-fno-omit-frame-pointer", "-fno-pie", "-no-pie", "-I" + filepath.Dir(inc), "-I" + filepath.Join(external, "package/microstick-core/src")}
	args = append(args, flags...)
	args = append(args, sources...)
	if name != "model" {
		args = append(args, filepath.Join(cjson, "cJSON.c"))
	}
	args = append(args, "-lm", "-o", filepath.Join(root, name))
	if len(packages) > 0 {
		a := append([]string{"--cflags", "--libs"}, packages...)
		args = append(args, strings.Fields(ok(t, nil, "", "pkg-config", a...))...)
	}
	ok(t, nil, "", "cc", args...)
	return filepath.Join(root, name)
}
func nativeEnv(e map[string]string) map[string]string {
	if e == nil {
		e = map[string]string{}
	}
	e["ASAN_OPTIONS"] = "detect_leaks=0:abort_on_error=1"
	e["UBSAN_OPTIONS"] = "halt_on_error=1"
	return e
}

type fixture struct {
	t          *testing.T
	root, r, e string
	env        map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, root: t.TempDir()}
	f.r = filepath.Join(f.root, "run")
	f.e = filepath.Join(f.root, "etc")
	bin := filepath.Join(f.root, "bin")
	for _, p := range []string{f.r, f.e, bin, filepath.Join(f.root, "sys/class/net/wwan0"), filepath.Join(f.root, "sys/class/net/wwan1")} {
		if e := os.MkdirAll(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ip", "mmcli", "logger", "udhcpc", "dnsmasq", "iw", "iptables", "iptables-restore", "iptables-save", "modprobe", "wpa_cli"} {
		if e := os.Symlink(self, filepath.Join(bin, n)); e != nil {
			t.Fatal(e)
		}
	}
	f.env = map[string]string{"MICROSTICK_TEST_MOCK": "1", "MICROSTICK_RUN": f.r, "MICROSTICK_ETC": f.e, "MICROSTICK_SYS": filepath.Join(f.root, "sys"), "MICROSTICK_IP": filepath.Join(bin, "ip"), "MICROSTICK_MMCLI": filepath.Join(bin, "mmcli"), "MICROSTICK_LIBRARY": overlay("usr/lib/microstick/network.sh"), "MICROSTICK_ROUTER": overlay("usr/libexec/microstick-router"), "MICROSTICK_FIREWALL": overlay("usr/libexec/microstick-firewall"), "MICROSTICK_ROUTER_DATA": overlay("usr/share/microstick/router"), "MICROSTICK_UPTIME": filepath.Join(f.root, "uptime"), "TEST_MODEM": filepath.Join(f.root, "modem.json"), "TEST_BEARER": filepath.Join(f.root, "bearer.json"), "PATH": bin + ":" + os.Getenv("PATH"), "WIFI_INTERFACE": "wlan0", "USB_INTERFACE": "usb0"}
	write(t, filepath.Join(f.e, "network.conf"), "USB_ADDRESS=192.168.68.1/24\nAP_ADDRESS=192.168.100.1/24\nLTE_METRIC=200\nWIFI_METRIC=100\n")
	write(t, filepath.Join(f.e, "lte.properties"), "apn=example,ip-type=ipv4\n")
	write(t, filepath.Join(f.r, "usb-iface"), "usb0")
	write(t, filepath.Join(f.r, "wifi-interface"), "wlan0")
	write(t, filepath.Join(f.r, "lan-mode"), "ap")
	write(t, filepath.Join(f.root, "uptime"), "100.0 0.0")
	f.bearer("wwan0", "static", "10.20.30.40", "yes", 0)
	f.modem("connected", "none", true)
	return f
}
func (f *fixture) bearer(iface, method, addr, connected string, duration int) {
	write(f.t, f.env["TEST_BEARER"], js(map[string]any{"bearer": map[string]any{"status": map[string]string{"connected": connected, "interface": iface}, "stats": map[string]int{"duration": duration}, "ipv4-config": map[string]any{"method": method, "address": addr, "prefix": "30", "gateway": "10.20.30.41", "dns": []string{"1.1.1.1"}, "mtu": "1428"}}}))
}
func (f *fixture) modem(state, unlock string, bearer bool) {
	b := []string{}
	if bearer {
		b = append(b, "/Bearer/0")
	}
	write(f.t, f.env["TEST_MODEM"], js(map[string]any{"modem": map[string]any{"generic": map[string]any{"state": state, "unlock-required": unlock, "bearers": b}}}))
}
func (f *fixture) shell(code string) (string, error) {
	return run(f.t, f.env, "", "sh", "-c", `. "$MICROSTICK_LIBRARY"; USB_ADDRESS=192.168.68.1/24; AP_ADDRESS=192.168.69.1/24; LTE_METRIC=200; LTE_ENABLED=yes; `+code)
}
func (f *fixture) calls() [][]string {
	var out [][]string
	b, _ := os.ReadFile(filepath.Join(f.r, "calls"))
	for _, s := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if s == "" {
			continue
		}
		var v []string
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			f.t.Fatal(e)
		}
		out = append(out, v)
	}
	return out
}
func (f *fixture) hasCall(want ...string) bool {
	for _, c := range f.calls() {
		if reflect.DeepEqual(c, want) {
			return true
		}
	}
	return false
}
func (f *fixture) router(data any, args ...string) (string, error) {
	if data != nil {
		p := filepath.Join(f.root, "request.json")
		write(f.t, p, js(data))
		args = append(args, p)
	}
	return run(f.t, f.env, "", overlay("usr/libexec/microstick-router"), args...)
}
func (f *fixture) routerOK(data any, args ...string) string {
	f.t.Helper()
	s, e := f.router(data, args...)
	if e != nil {
		f.t.Fatal(e, s)
	}
	return s
}
func mockTool() int {
	n := filepath.Base(os.Args[0])
	a := os.Args[1:]
	r := os.Getenv("MICROSTICK_RUN")
	file, e := os.OpenFile(filepath.Join(r, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	fmt.Fprintln(file, js(append([]string{n}, a...)))
	file.Close()
	has := func(s string) bool {
		for _, v := range a {
			if v == s {
				return true
			}
		}
		return false
	}
	prefix := func(v ...string) bool { return len(a) >= len(v) && reflect.DeepEqual(a[:len(v)], v) }
	printFile := func(key string) {
		b, e := os.ReadFile(os.Getenv(key))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
		}
		fmt.Print(string(b))
	}
	switch n {
	case "mmcli":
		if has("-L") {
			fmt.Print(`{"modem-list":["/Modem/0"]}`)
		} else if has("-b") {
			printFile("TEST_BEARER")
		} else if strings.Contains(strings.Join(a, " "), "--simple-connect=") {
			if s := os.Getenv("TEST_CONNECT_ERROR"); s != "" {
				fmt.Fprintln(os.Stderr, s)
				return 1
			}
		} else if has("-m") {
			printFile("TEST_MODEM")
		}
	case "ip":
		if prefix("-j", "-4", "neigh", "show") {
			s := os.Getenv("TEST_NEIGHBORS")
			if s == "" {
				s = "[]"
			}
			fmt.Print(s)
		} else if prefix("-j", "-4", "addr") {
			s := os.Getenv("TEST_WAN")
			if s == "" {
				s = "[]"
			}
			fmt.Print(s)
		} else if prefix("-4", "-o", "addr", "show") {
			s := os.Getenv("TEST_STA")
			if s == "" {
				s = "192.168.31.77/24"
			}
			fmt.Print("2: wlan0 inet " + s + " scope global wlan0")
		}
	case "dnsmasq":
		if has("--test") && os.Getenv("FAIL_DNS") != "" {
			return 1
		}
		if !has("--test") && os.Getenv("TEST_OWNER_PID") != "" {
			os.WriteFile(filepath.Join(r, "dnsmasq.pid"), []byte(os.Getenv("TEST_OWNER_PID")), 0600)
		}
	case "iptables-restore":
		b, _ := io.ReadAll(os.Stdin)
		os.WriteFile(filepath.Join(r, "restored"), b, 0600)
		if has("--test") && os.Getenv("FAIL_FIREWALL") != "" {
			return 1
		}
	case "iptables-save":
		fmt.Print("*filter\nCOMMIT\n")
	}
	return 0
}
