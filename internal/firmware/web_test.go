package firmware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func cross(t *testing.T, name string, args ...string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), name)
	args = append(args, "-o", binary)
	ok(t, nil, "", filepath.Join(workspace, "buildroot-output/host/bin/aarch64-buildroot-linux-musl-gcc"), args...)
	return binary
}
func qemuArgs(binary string) []string {
	sysroot := filepath.Join(workspace, "buildroot-output/host/aarch64-buildroot-linux-musl/sysroot")
	return []string{"-L", sysroot, "-E", "LD_LIBRARY_PATH=" + sysroot + "/lib:" + sysroot + "/usr/lib", binary}
}
func passwordHash(p, s string) string {
	mac := hmac.New(sha256.New, []byte(p))
	mac.Write([]byte(s))
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	out := append([]byte{}, u...)
	for i := 1; i < 120000; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for k := range out {
			out[k] ^= u[k]
		}
	}
	return hex.EncodeToString(out)
}

type webFixture struct {
	t                         *testing.T
	r, data, password, binary string
	port                      int
	process                   *exec.Cmd
	mu                        sync.Mutex
	requests                  []map[string]any
}

func newWeb(t *testing.T) *webFixture {
	t.Helper()
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	w := &webFixture{t: t, r: t.TempDir(), password: "temporary-password", port: port}
	w.data = filepath.Join(w.r, "data")
	write(t, filepath.Join(w.data, "auth.json"), js(map[string]string{"salt": "synthetic-test-salt", "hash": passwordHash(w.password, "synthetic-test-salt")}))
	write(t, filepath.Join(w.r, "device.json"), `{"hostname":"microstick","mdns_name":"microstick-2.local"}`)
	w.binary = cross(t, "web", "-Wall", "-Wextra", "-Werror", "-DWEB", "-DMICROSTICK_WEB_PORT="+strconv.Itoa(port), "-DMICROSTICK_WEB_UID=getuid()", "-DMICROSTICK_WEB_GID=getgid()", source("service.c"), "-lcjson", "-lcrypto", "-lmicrohttpd")
	server(t, filepath.Join(w.r, "core.sock"), func(v map[string]any) any {
		w.mu.Lock()
		w.requests = append(w.requests, v)
		w.mu.Unlock()
		return map[string]bool{"ok": true}
	})
	w.start()
	t.Cleanup(w.stop)
	return w
}
func (w *webFixture) start() {
	e := map[string]string{"MICROSTICK_PLUGIN_DATA": w.data, "MICROSTICK_PLUGIN_DIR": overlay("usr/share/microstick/default-plugins/web"), "MICROSTICK_CORE_SOCKET": filepath.Join(w.r, "core.sock"), "MICROSTICK_STATISTICS_SOCKET": filepath.Join(w.r, "core.sock"), "MICROSTICK_RUN": w.r}
	log, err := os.OpenFile(filepath.Join(w.r, "web.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		w.t.Fatal(err)
	}
	w.process = exec.Command(filepath.Join(workspace, "buildroot-output/host/bin/qemu-aarch64"), qemuArgs(w.binary)...)
	w.process.Env = env(e)
	w.process.Stdout = log
	w.process.Stderr = log
	if e := w.process.Start(); e != nil {
		w.t.Fatal(e)
	}
	log.Close()
	until(w.t, 5*time.Second, func() bool {
		c, e := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", w.port), 100*time.Millisecond)
		if e != nil {
			return false
		}
		c.Close()
		return true
	})
}
func (w *webFixture) stop() {
	if w.process != nil {
		w.process.Process.Signal(os.Interrupt)
		w.process.Wait()
		w.process = nil
	}
}
func (w *webFixture) call(path string, data any, cookie, csrf, host string, origin bool) (int, map[string]any, string) {
	w.t.Helper()
	if host == "" {
		host = fmt.Sprintf("127.0.0.1:%d", w.port)
	}
	method := http.MethodGet
	var body io.Reader
	if data != nil {
		method = http.MethodPost
		body = strings.NewReader(js(data))
	}
	req, e := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", w.port, path), body)
	if e != nil {
		w.t.Fatal(e)
	}
	req.Host = host
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Microstick-CSRF", csrf)
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
		if origin {
			req.Header.Set("Origin", "http://"+host)
		}
	}
	c := http.Client{Timeout: 10 * time.Second}
	resp, e := c.Do(req)
	if e != nil {
		w.t.Fatal(e)
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(resp.Body)
	if e != nil {
		w.t.Fatal(e)
	}
	return resp.StatusCode, object(w.t, string(b)), resp.Header.Get("Set-Cookie")
}
func (w *webFixture) login(remember bool) (string, string, string) {
	w.t.Helper()
	status, b, h := w.call("/api/login", map[string]any{"password": w.password, "remember": remember}, "", "", "", true)
	equal(w.t, status, 200)
	return strings.Split(h, ";")[0], b["csrf"].(string), h
}
func (w *webFixture) last() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.requests) == 0 {
		w.t.Fatal("no backend requests")
	}
	return w.requests[len(w.requests)-1]
}
func TestWebAuthentication(t *testing.T) {
	t.Run("remember_restart_logout_hash_only", func(t *testing.T) {
		w := newWeb(t)
		cookie, csrf, h := w.login(true)
		contains(t, h, "Max-Age=2592000")
		contains(t, h, "HttpOnly")
		s := read(t, filepath.Join(w.data, "sessions.json"))
		no(t, strings.Contains(s, strings.SplitN(cookie, "=", 2)[1]) || strings.Contains(s, w.password), "secret stored in sessions")
		w.stop()
		w.start()
		status, b, _ := w.call("/api/session", nil, cookie, "", "", true)
		equal(t, status, 200)
		no(t, b["csrf"] == csrf, "CSRF reused after restart")
		status, _, _ = w.call("/api/logout", map[string]any{}, cookie, b["csrf"].(string), "", true)
		equal(t, status, 200)
		w.stop()
		w.start()
		status, _, _ = w.call("/api/session", nil, cookie, "", "", true)
		equal(t, status, 401)
	})
	t.Run("short_and_expired", func(t *testing.T) {
		w := newWeb(t)
		cookie, _, h := w.login(false)
		contains(t, h, "Max-Age=1800")
		w.stop()
		w.start()
		s, _, _ := w.call("/api/session", nil, cookie, "", "", true)
		equal(t, s, 401)
		cookie, _, _ = w.login(true)
		w.stop()
		p := filepath.Join(w.data, "sessions.json")
		var rows []map[string]any
		if e := decode([]byte(read(t, p)), &rows); e != nil {
			t.Fatal(e)
		}
		rows[0]["expires"] = time.Now().Unix() - 1
		write(t, p, js(rows))
		w.start()
		s, _, _ = w.call("/api/session", nil, cookie, "", "", true)
		equal(t, s, 401)
	})
	t.Run("password_revokes_disk_tokens", func(t *testing.T) {
		w := newWeb(t)
		cookie, csrf, _ := w.login(true)
		other, _, _ := w.login(true)
		p := filepath.Join(w.data, "sessions.json")
		old := read(t, p)
		s, _, _ := w.call("/api/password", map[string]string{"current": w.password, "password": "another-temporary-password"}, cookie, csrf, "", true)
		equal(t, s, 200)
		s, _, _ = w.call("/api/session", nil, other, "", "", true)
		equal(t, s, 401)
		w.stop()
		write(t, p, old)
		w.start()
		s, _, _ = w.call("/api/session", nil, other, "", "", true)
		equal(t, s, 401)
	})
	t.Run("host_origin_csrf_socket_address", func(t *testing.T) {
		w := newWeb(t)
		cookie, csrf, _ := w.login(true)
		for _, v := range []struct {
			host string
			want int
		}{{"microstick.local", 200}, {"microstick-2.local", 200}, {"evil.local", 403}} {
			s, _, _ := w.call("/api/session", nil, cookie, "", fmt.Sprintf("%s:%d", v.host, w.port), true)
			equal(t, s, v.want)
		}
		uptime, _ := strconv.ParseFloat(strings.Fields(read(t, "/proc/uptime"))[0], 64)
		p := filepath.Join(w.r, "device.json")
		cfg := object(t, read(t, p))
		cfg["previous_mdns_name"] = "old.local"
		cfg["previous_valid_until"] = uptime + 120
		write(t, p, js(cfg))
		s, _, _ := w.call("/api/session", nil, cookie, "", fmt.Sprintf("old.local:%d", w.port), true)
		equal(t, s, 200)
		cfg["previous_valid_until"] = uptime - 1
		write(t, p, js(cfg))
		s, _, _ = w.call("/api/session", nil, cookie, "", fmt.Sprintf("old.local:%d", w.port), true)
		equal(t, s, 403)
		for _, origin := range []bool{false, true} {
			s, _, _ = w.call("/api/wifi_confirm", map[string]string{"id": "attempt"}, cookie, "", "", origin)
			equal(t, s, 403)
		}
		s, _, _ = w.call("/api/wifi_confirm", map[string]string{"id": "attempt", "local_address": "192.0.2.99", "method": "reboot"}, cookie, csrf, "", true)
		equal(t, s, 200)
		equal(t, w.last(), map[string]any{"id": "attempt", "local_address": "127.0.0.1", "method": "wifi_confirm"})
		s, _, _ = w.call("/api/statistics/config", map[string]bool{"wifi_persistence": true}, cookie, csrf, "", true)
		equal(t, s, 200)
		equal(t, w.last(), map[string]any{"method": "configure", "wifi_persistence": true})
		s, _, _ = w.call("/api/statistics/reset", map[string]string{"source": "wifi"}, cookie, csrf, "", true)
		equal(t, s, 200)
		equal(t, w.last(), map[string]any{"method": "reset_period", "source": "wifi"})
	})
}
