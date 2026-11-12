package firmware

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIsolatedNetwork(t *testing.T) {
	switch os.Getenv("MICROSTICK_NS_ROLE") {
	case "inside":
		isolatedServer(t)
		return
	case "client":
		isolatedClient(t)
		return
	}
	collector := compile(t, "statistics", []string{filepath.Join(external, "package/microstick-statistics/src/daemon.c"), filepath.Join(external, "package/microstick-statistics/src/model.c"), filepath.Join(external, "package/microstick-statistics/src/nft.c")}, []string{"libmnl", "libnftnl"}, nil)
	mdns := cross(t, "mdns", "-Wall", "-Wextra", "-Werror", filepath.Join(external, "tests/mdns_server_test.c"), "-lavahi-core", "-lavahi-common")
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	r := t.TempDir()
	c := exec.Command("unshare", "--user", "--map-root-user", "--net", "--uts", self, "-test.run=^TestIsolatedNetwork$", "-test.v")
	c.Env = env(map[string]string{"MICROSTICK_NS_ROLE": "inside", "MICROSTICK_NS_ROOT": r, "MICROSTICK_NS_COLLECTOR": collector, "MICROSTICK_NS_MDNS": mdns})
	out, e := c.CombinedOutput()
	if e != nil {
		logs, _ := filepath.Glob(filepath.Join(r, "*.log"))
		var s strings.Builder
		for _, p := range logs {
			b, _ := os.ReadFile(p)
			s.Write(b)
		}
		t.Fatalf("isolated network: %v\n%s\n%s", e, out, s.String())
	}
	t.Log(strings.TrimSpace(string(out)))
}
func isolatedServer(t *testing.T) {
	r := os.Getenv("MICROSTICK_NS_ROOT")
	core, data, runtime := filepath.Join(r, "core"), filepath.Join(r, "data"), filepath.Join(r, "runtime")
	for _, p := range []string{core, data, runtime} {
		if e := os.MkdirAll(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	ok(t, nil, "", "ip", "link", "set", "lo", "up")
	write(t, filepath.Join(core, "lan-mode"), "sta")
	write(t, filepath.Join(core, "wifi-interface"), "lo")
	write(t, filepath.Join(core, "time-synced"), read(t, "/proc/sys/kernel/random/boot_id"))
	write(t, filepath.Join(core, "wifi-status"), "wpa_state=COMPLETED\nssid=test\nbssid=02:00:00:00:00:01\n")
	e := nativeEnv(map[string]string{"MICROSTICK_RUN": core, "MICROSTICK_PLUGIN_DATA": data, "MICROSTICK_PLUGIN_RUNTIME": runtime})
	var process *exec.Cmd
	launch := func() {
		log, err := os.OpenFile(filepath.Join(r, "collector.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		process = exec.Command(os.Getenv("MICROSTICK_NS_COLLECTOR"))
		process.Env = env(e)
		process.Stdout = log
		process.Stderr = log
		if err = process.Start(); err != nil {
			t.Fatal(err)
		}
		log.Close()
		until(t, 5*time.Second, func() bool { return exists(filepath.Join(runtime, "api.sock")) })
	}
	stop := func() {
		if process != nil {
			process.Process.Signal(os.Interrupt)
			if err := process.Wait(); err != nil {
				t.Error(err)
			}
			process = nil
		}
	}
	launch()
	t.Cleanup(stop)
	api := func(m string, fields map[string]any) map[string]any {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["method"] = m
		return rpc(t, filepath.Join(runtime, "api.sock"), fields)
	}
	traffic := func() {
		c, err := net.Dial("udp4", "127.0.0.1:19999")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for i := 0; i < 10; i++ {
			c.Write(make([]byte, 1000))
		}
		time.Sleep(3200 * time.Millisecond)
	}
	saved := func() float64 {
		s := api("status", nil)["wifi"].(map[string]any)["saved"].(map[string]any)["rx"].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	traffic()
	names, _ := filepath.Glob(filepath.Join(data, "wifi-*.bin"))
	equal(t, len(names), 0)
	equal(t, api("configure", map[string]any{"wifi_persistence": true})["ok"], true)
	equal(t, saved(), float64(0))
	traffic()
	total := saved()
	no(t, total <= 0, "Wi-Fi traffic not counted")
	lte := api("status", nil)["lte"].(map[string]any)
	equal(t, lte["rx"], "0")
	equal(t, lte["tx"], "0")
	stop()
	launch()
	equal(t, saved(), total)
	equal(t, api("configure", map[string]any{"wifi_persistence": false})["ok"], true)
	traffic()
	equal(t, saved(), total)
	equal(t, api("configure", map[string]any{"wifi_persistence": true})["ok"], true)
	equal(t, saved(), total)
	traffic()
	no(t, saved() <= total, "resume did not count")
	write(t, filepath.Join(core, "lan-mode"), "ap")
	time.Sleep(200 * time.Millisecond)
	write(t, filepath.Join(core, "lan-mode"), "sta")
	time.Sleep(200 * time.Millisecond)
	before := saved()
	time.Sleep(3200 * time.Millisecond)
	equal(t, saved(), before)
	equal(t, api("reset_period", map[string]any{"source": "wifi"})["ok"], true)
	equal(t, saved(), float64(0))
	history := api("history", map[string]any{"source": "wifi", "period": "day"})
	no(t, len(history["buckets"].([]any)) == 0 && api("status", nil)["clock_synced"] == true, "archive empty")
	equal(t, api("configure", map[string]any{"wifi_persistence": "true"})["error"], "invalid_statistics_request")
	equal(t, api("nonsense", nil)["error"], "invalid_statistics_request")
	stop()
	for _, v := range [][3]string{{"ms-sta", "ms-client", "198.18.0.1/24"}, {"wwan0", "ms-lte-client", "198.19.0.1/24"}} {
		ok(t, nil, "", "ip", "link", "add", v[0], "type", "veth", "peer", "name", v[1])
		ok(t, nil, "", "ip", "addr", "add", v[2], "dev", v[0])
		ok(t, nil, "", "ip", "link", "set", v[0], "up")
	}
	self, _ := os.Executable()
	child := exec.Command("unshare", "--net", self, "-test.run=^TestIsolatedNetwork$", "-test.v")
	child.Env = env(map[string]string{"MICROSTICK_NS_ROLE": "client"})
	var clientOut strings.Builder
	child.Stdout = &clientOut
	child.Stderr = &clientOut
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill() })
	until(t, 5*time.Second, func() bool { return exists(filepath.Join(r, "client-ready")) })
	for _, iface := range []string{"ms-client", "ms-lte-client"} {
		ok(t, nil, "", "ip", "link", "set", iface, "netns", strconv.Itoa(child.Process.Pid))
	}
	for k, v := range map[string]string{"usb-iface": "usb0", "wifi-interface": "ms-sta", "lan-mode": "sta", "lte-iface": "wwan0"} {
		write(t, filepath.Join(core, k), v)
	}
	write(t, filepath.Join(core, "lan-effective.json.tmp"), js(baseLAN()))
	write(t, filepath.Join(data, "firewall.json"), `{"rules":[],"services":[{"name":"test","enabled":true,"protocol":"both","port":"8000","source":""}]}`)
	bin := filepath.Join(r, "bin")
	write(t, filepath.Join(bin, "modprobe"), "#!/bin/sh\nexit 0\n")
	ok(t, map[string]string{"MICROSTICK_RUN": core, "MICROSTICK_ETC": data, "MICROSTICK_ROUTER_DATA": overlay("usr/share/microstick/router"), "PATH": bin + ":" + os.Getenv("PATH")}, "", overlay("usr/libexec/microstick-firewall"), "ms-sta")
	for _, port := range []int{80, 8000, 8002} {
		l, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() { defer c.Close(); b := make([]byte, 1); n, _ := c.Read(b); c.Write(b[:n]) }()
			}
		}()
	}
	for _, port := range []int{8000, 8002} {
		c, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		go func() {
			b := make([]byte, 2048)
			for {
				n, peer, err := c.ReadFrom(b)
				if err != nil {
					return
				}
				c.WriteTo(b[:n], peer)
			}
		}()
	}
	mdns := start(t, nil, filepath.Join(workspace, "buildroot-output/host/bin/qemu-aarch64"), qemuArgs(os.Getenv("MICROSTICK_NS_MDNS"))...)
	_ = mdns
	time.Sleep(1500 * time.Millisecond)
	write(t, filepath.Join(r, "server-ready"), "")
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err, clientOut.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("client timed out")
	}
	t.Log("PASS: collector opt-in/source isolation/restart/pause/resume/reconnect/reset/archive and TCP/UDP firewall plus target Avahi packets")
}
func isolatedClient(t *testing.T) {
	r := os.Getenv("MICROSTICK_NS_ROOT")
	write(t, filepath.Join(r, "client-ready"), "")
	until(t, 5*time.Second, func() bool { _, e := net.InterfaceByName("ms-client"); return e == nil })
	for _, v := range [][2]string{{"lo", ""}, {"ms-client", "198.18.0.2/24"}, {"ms-lte-client", "198.19.0.2/24"}} {
		if v[1] != "" {
			ok(t, nil, "", "ip", "addr", "add", v[1], "dev", v[0])
		}
		ok(t, nil, "", "ip", "link", "set", v[0], "up")
	}
	until(t, 6*time.Second, func() bool { return exists(filepath.Join(r, "server-ready")) })
	for _, v := range []struct {
		address string
		port    int
		want    bool
	}{{"198.18.0.1", 80, true}, {"198.18.0.1", 8000, true}, {"198.18.0.1", 8002, false}, {"198.19.0.1", 8000, false}} {
		c, e := net.DialTimeout("tcp4", fmt.Sprintf("%s:%d", v.address, v.port), 600*time.Millisecond)
		passed := false
		if e == nil {
			c.SetDeadline(time.Now().Add(600 * time.Millisecond))
			c.Write([]byte("x"))
			b := make([]byte, 1)
			n, _ := c.Read(b)
			passed = n == 1 && b[0] == 'x'
			c.Close()
		}
		equal(t, passed, v.want)
	}
	for _, v := range []struct {
		port int
		want bool
	}{{8000, true}, {8002, false}} {
		c, e := net.Dial("udp4", fmt.Sprintf("198.18.0.1:%d", v.port))
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(600 * time.Millisecond))
		c.Write([]byte("test"))
		b := make([]byte, 1024)
		n, _ := c.Read(b)
		equal(t, string(b[:n]) == "test", v.want)
		c.Close()
	}
	query := make([]byte, 12)
	binary.BigEndian.PutUint16(query, 0x1234)
	binary.BigEndian.PutUint16(query[4:], 1)
	query = append(query, []byte("\x0amicrostick\x05local\x00\x00\x01\x00\x01")...)
	c, e := net.Dial("udp4", "198.18.0.1:5353")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write(query)
	b := make([]byte, 4096)
	n, e := c.Read(b)
	if e != nil {
		t.Fatal(e)
	}
	no(t, n < 12 || binary.BigEndian.Uint16(b[6:]) == 0, "no mDNS answer")
	contains(t, string(b[:n]), string(net.ParseIP("198.18.0.1").To4()))
}
