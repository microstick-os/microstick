package firmware

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func source(n string) string { return filepath.Join(external, "package/microstick-core/src", n) }
func start(t *testing.T, e map[string]string, binary string, args ...string) *exec.Cmd {
	t.Helper()
	log, err := os.CreateTemp(t.TempDir(), "process-")
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(binary, args...)
	c.Env = env(nativeEnv(e))
	c.Stdout = log
	c.Stderr = log
	if err = c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			c.Process.Kill()
			<-done
		}
		log.Close()
		b, _ := os.ReadFile(log.Name())
		if strings.Contains(string(b), "Sanitizer") || strings.Contains(string(b), "runtime error:") {
			t.Errorf("native sanitizer: %s", b)
		}
	})
	return c
}
func until(t *testing.T, seconds time.Duration, fn func() bool) {
	t.Helper()
	end := time.Now().Add(seconds)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition did not settle")
}
func rpc(t *testing.T, path string, v any) map[string]any {
	t.Helper()
	c, e := net.DialTimeout("unixpacket", path, 2*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	var b []byte
	if raw, ok := v.([]byte); ok {
		b = raw
	} else {
		b = []byte(js(v))
	}
	if _, e = c.Write(b); e != nil {
		t.Fatal(e)
	}
	b = make([]byte, 32768)
	n, e := c.Read(b)
	if e != nil {
		t.Fatal(e)
	}
	return object(t, string(b[:n]))
}
func server(t *testing.T, path string, fn func(map[string]any) any) {
	t.Helper()
	s, e := net.Listen("unixpacket", path)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := s.Accept()
			if e != nil {
				return
			}
			b := make([]byte, 32768)
			n, e := c.Read(b)
			if e == nil {
				var v map[string]any
				if jsonErr := decode(b[:n], &v); jsonErr == nil {
					c.Write([]byte(js(fn(v))))
				}
			}
			c.Close()
		}
	}()
	t.Cleanup(func() { s.Close(); <-done })
}
func TestCoreJobs(t *testing.T) {
	binary := compile(t, "core", []string{source("service.c")}, nil, nil)
	r := t.TempDir()
	sock := filepath.Join(r, "socket")
	for _, n := range []string{"router.sock", "network.sock"} {
		server(t, filepath.Join(r, n), func(v map[string]any) any {
			switch v["method"] {
			case "lan_config":
				return map[string]any{"config": map[string]any{"usb": map[string]any{}}}
			case "firewall_config":
				return map[string]any{"rules": []any{}}
			case "clients":
				return map[string]any{"clients": []any{}, "page": 0, "total": 0}
			default:
				return map[string]any{"wifi": "ap", "api_version": 1}
			}
		})
	}
	action := filepath.Join(r, "action")
	write(t, action, `#!/bin/sh
if jq -e '.ssid=="crash"' "$2" >/dev/null; then exit 1; fi
while [ ! -e "$MICROSTICK_RUN/release" ]; do sleep .02; done
printf '%s\n' '{"ok":true,"message":"connected"}' > "$MICROSTICK_RUN/result.json"
`)
	start(t, map[string]string{"MICROSTICK_CORE_SOCKET": sock, "MICROSTICK_RUN": r, "MICROSTICK_ACTION": action}, binary, "--daemon")
	until(t, 3*time.Second, func() bool { return exists(sock) })
	call := func(m string) map[string]any { return rpc(t, sock, map[string]string{"method": m}) }
	equal(t, call("status")["wifi"], "ap")
	no(t, call("lan_config")["config"] == nil, "LAN proxy failed")
	equal(t, len(call("firewall_config")["rules"].([]any)), 0)
	equal(t, call("clients")["page"], float64(0))
	equal(t, rpc(t, sock, map[string]string{"method": "clients", "page": "../secret"})["error"], "invalid_page")
	equal(t, rpc(t, sock, []byte("{broken"))["error"], "unknown_method")
	equal(t, call("shell")["error"], "unknown_method")
	no(t, rpc(t, sock, map[string]string{"method": "connect", "ssid": "a;$(id)", "password": "synthetic"})["operation"] == nil, "job did not start")
	equal(t, call("scan")["error"], "busy")
	equal(t, call("status")["api_version"], float64(1))
	equal(t, call("operation")["busy"], true)
	write(t, filepath.Join(r, "release"), "")
	var result map[string]any
	until(t, 4*time.Second, func() bool { result = call("operation"); return result["busy"] == false })
	equal(t, result["result"].(map[string]any)["ok"], true)
	no(t, exists(filepath.Join(r, "request.json")), "request retained")
	callConnect := rpc(t, sock, map[string]string{"method": "connect", "ssid": "crash"})
	no(t, callConnect["operation"] == nil, "failed child did not start")
	until(t, 4*time.Second, func() bool { result = call("operation"); return result["busy"] == false })
	equal(t, result["result"].(map[string]any)["ok"], false)
}
func TestSSIDAndRecovery(t *testing.T) {
	tool := compile(t, "wifi", []string{source("ssid.c")}, []string{"glib-2.0"}, []string{"-DWIFI_TOOL", "-lcrypto"})
	guard := compile(t, "guard", []string{filepath.Join(external, "tests/wifi_guard_test.c"), source("event.c")}, []string{"gio-unix-2.0", "libcurl"}, nil)
	t.Run("exact_scan_config", func(t *testing.T) {
		for _, raw := range []string{"Русская сеть", " leading trailing ", `literal\x41"quote`, "Дом 🏠"} {
			var encoded strings.Builder
			for _, b := range []byte(raw) {
				if b >= 33 && b <= 126 && b != 92 {
					encoded.WriteByte(b)
				} else {
					fmt.Fprintf(&encoded, "\\x%02x", b)
				}
			}
			out := ok(t, nativeEnv(nil), "BSS 02:12:34:56:78:90(on wlan0)\n\tsignal: -42.5 dBm\n\tSSID: "+encoded.String()+"\n\tRSN:\n", tool, "scan")
			var networks []map[string]any
			if e := decode([]byte(out), &networks); e != nil {
				t.Fatal(e)
			}
			equal(t, networks[0]["ssid"], raw)
			equal(t, networks[0]["ssid_hex"], fmt.Sprintf("%x", raw))
			networks[0]["password"] = "password123"
			out = ok(t, nativeEnv(nil), js(networks[0]), tool, "config")
			contains(t, out, "ssid="+fmt.Sprintf("%x", raw))
			no(t, strings.Contains(out, "password123"), "password exposed")
		}
	})
	t.Run("binary_validation", func(t *testing.T) {
		contains(t, ok(t, nativeEnv(nil), `{"ssid_hex":"0061ff","security":"open"}`, tool, "config"), "ssid=0061ff")
		for _, s := range []string{"", "abz0", "f", strings.Repeat("ab", 33)} {
			_, e := run(t, nativeEnv(nil), js(map[string]string{"ssid": "", "ssid_hex": s, "security": "open"}), tool, "config")
			no(t, e == nil, "bad SSID accepted")
		}
		_, e := run(t, nativeEnv(nil), js(map[string]string{"ssid": strings.Repeat("я", 17), "security": "open"}), tool, "config")
		no(t, e == nil, "overlong SSID accepted")
	})
	t.Run("candidate_durable_profile", func(t *testing.T) {
		f := newFixture(t)
		f.env["MICROSTICK_WIFI_TOOL"] = tool
		write(t, filepath.Join(f.e, "network.conf"), "WIFI_MODE=ap\n")
		write(t, filepath.Join(f.e, "wpa_supplicant.conf"), "saved-private-profile\n")
		p := filepath.Join(f.root, "request.json")
		write(t, p, `{"ssid":"Русская сеть","password":"password123","security":"wpa2"}`)
		ok(t, nativeEnv(f.env), "", "sh", overlay("usr/libexec/microstick-action"), "connect", p)
		equal(t, read(t, filepath.Join(f.e, "wpa_supplicant.conf")), "saved-private-profile\n")
		contains(t, read(t, filepath.Join(f.e, "network.conf")), "WIFI_MODE=ap")
		no(t, !exists(filepath.Join(f.r, "wifi-stage")) || !exists(filepath.Join(f.r, "wifi-candidate.conf")), "candidate missing")
	})
	t.Run("guard_state", func(t *testing.T) { f := newFixture(t); ok(t, nativeEnv(f.env), "", guard, "state") })
	t.Run("real_http_probes", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/ok":
				w.WriteHeader(204)
			case "/ms":
				fmt.Fprint(w, "Microsoft Connect Test")
			case "/portal":
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(302)
			default:
				w.WriteHeader(500)
			}
		}))
		defer s.Close()
		for _, v := range []struct{ first, second, want string }{{"/ok", "/broken", "1"}, {"/broken", "/ms", "1"}, {"/portal", "/broken", "2"}, {"/broken", "/broken", "0"}} {
			out := ok(t, nativeEnv(map[string]string{"MICROSTICK_CONNECTIVITY_URL1": s.URL + v.first, "MICROSTICK_CONNECTIVITY_URL2": s.URL + v.second}), "", guard, "probe")
			equal(t, strings.TrimSpace(out), v.want)
		}
	})
}
func TestStatisticsModelAndTime(t *testing.T) {
	t.Run("production_model", func(t *testing.T) {
		binary := compile(t, "model", []string{filepath.Join(external, "tests/statistics_model_test.c"), filepath.Join(external, "package/microstick-statistics/src/model.c")}, nil, []string{"-I" + filepath.Join(external, "package/microstick-statistics/src")})
		ok(t, nativeEnv(nil), "", binary, t.TempDir())
	})
	t.Run("ntp_readiness", func(t *testing.T) {
		r := t.TempDir()
		script := filepath.Join(r, "time-event")
		write(t, script, strings.ReplaceAll(read(t, overlay("usr/libexec/microstick-time-event")), "/run/microstick", r))
		e := map[string]string{"stratum": "16"}
		ok(t, e, "", script, "periodic")
		no(t, exists(filepath.Join(r, "time-synced")), "unsynced periodic trusted")
		ok(t, e, "", script, "step")
		equal(t, read(t, filepath.Join(r, "time-synced")), read(t, "/proc/sys/kernel/random/boot_id"))
		ok(t, e, "", script, "unsync")
		no(t, exists(filepath.Join(r, "time-synced")), "unsync retained marker")
		e["stratum"] = "3"
		ok(t, e, "", script, "stratum")
		no(t, !exists(filepath.Join(r, "time-synced")), "sync marker missing")
	})
}
