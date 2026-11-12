package firmware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetwork(t *testing.T) {
	t.Run("static_bearer", func(t *testing.T) {
		f := newFixture(t)
		_, e := f.shell(`apply_bearer "$TEST_BEARER"`)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range [][]string{{"ip", "link", "set", "dev", "wwan0", "mtu", "1428"}, {"ip", "-4", "addr", "replace", "10.20.30.40/30", "dev", "wwan0"}, {"ip", "-4", "route", "replace", "default", "via", "10.20.30.41", "dev", "wwan0", "onlink", "metric", "200", "proto", "186"}} {
			if !f.hasCall(c...) {
				t.Fatal("missing command", c)
			}
		}
		equal(t, read(t, filepath.Join(f.r, "lte-dns")), "nameserver 1.1.1.1\n")
	})
	t.Run("interface_change", func(t *testing.T) {
		f := newFixture(t)
		write(t, filepath.Join(f.r, "lte-iface"), "wwan0\n")
		f.bearer("wwan1", "static", "10.20.30.40", "yes", 0)
		_, e := f.shell(`apply_bearer "$TEST_BEARER"`)
		if e != nil {
			t.Fatal(e)
		}
		no(t, !f.hasCall("ip", "-4", "route", "flush", "dev", "wwan0", "proto", "186"), "old routes not cleared")
		no(t, !f.hasCall("ip", "link", "set", "dev", "wwan1", "up"), "new interface not up")
	})
	t.Run("invalid_overlap", func(t *testing.T) {
		f := newFixture(t)
		for _, addr := range []string{"192.168.68.9", "192.168.69.9", "300.1.1.1", "bad"} {
			f.bearer("wwan0", "static", addr, "yes", 0)
			_, e := f.shell(`apply_bearer "$TEST_BEARER"`)
			no(t, e == nil, "bad address accepted: "+addr)
		}
		for _, c := range f.calls() {
			no(t, c[0] == "ip", "invalid bearer changed network")
		}
	})
	t.Run("dhcp_bearer", func(t *testing.T) {
		f := newFixture(t)
		f.bearer("wwan0", "dhcp", "10.20.30.40", "yes", 0)
		_, e := f.shell(`apply_bearer "$TEST_BEARER"`)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, c := range f.calls() {
			if c[0] == "udhcpc" && strings.Contains(strings.Join(c, " "), "wwan0") {
				found = true
			}
		}
		no(t, !found, "DHCP client not started")
	})
	for _, v := range []struct{ lock, reason string }{{"sim-pin", "sim_pin_required"}, {"sim-puk", "sim_puk_required"}, {"ph-net-pin", "sim_locked"}} {
		t.Run("blocking_"+v.lock, func(t *testing.T) {
			f := newFixture(t)
			f.modem("locked", v.lock, false)
			_, e := f.shell("poll_lte")
			no(t, e == nil, "locked SIM connected")
			equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), v.reason)
			for _, c := range f.calls() {
				no(t, strings.Contains(strings.Join(c, " "), "--simple-connect="), "blocked SIM attempted connection")
			}
		})
	}
	for _, lock := range []string{"sim-pin2", "sim-puk2"} {
		t.Run("nonblocking_"+lock, func(t *testing.T) {
			f := newFixture(t)
			f.modem("disabled", lock, false)
			_, e := f.shell("poll_lte")
			if e != nil {
				t.Fatal(e)
			}
			count := 0
			for _, c := range f.calls() {
				s := strings.Join(c, " ")
				if strings.Contains(s, "--simple-connect=") {
					count++
					no(t, strings.Contains(s, "pin=") || strings.Contains(s, "puk="), "sent SIM code")
				}
			}
			equal(t, count, 1)
		})
	}
	t.Run("disabled_clears_error", func(t *testing.T) {
		f := newFixture(t)
		write(t, filepath.Join(f.r, "lte-error"), "lte_connect_failed")
		_, e := f.shell("LTE_ENABLED=no; poll_lte")
		if e != nil {
			t.Fatal(e)
		}
		no(t, exists(filepath.Join(f.r, "lte-error")), "stale error retained")
	})
	for _, v := range []struct{ name, body, reason string }{{"manual_denied", "cat >/dev/null\necho '{\"ok\":false,\"error\":\"registration_denied\"}'\nexit 1\n", "registration_denied"}, {"manual_timeout", "exit 124\n", "lte_timeout"}} {
		t.Run(v.name, func(t *testing.T) {
			f := newFixture(t)
			f.modem("disabled", "sim-pin2", false)
			write(t, filepath.Join(f.e, "lte.json"), `{"apn":"internet"}`)
			helper := filepath.Join(f.root, "helper")
			write(t, helper, "#!/bin/sh\n"+v.body)
			f.env["MICROSTICK_MODEM"] = helper
			_, e := f.shell("poll_lte")
			no(t, e == nil, "manual failure ignored")
			equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), v.reason)
			no(t, exists(filepath.Join(f.r, "lte-connecting")), "progress retained")
		})
	}
	t.Run("protocol_error_sanitized", func(t *testing.T) {
		f := newFixture(t)
		f.modem("disabled", "sim-pin2", false)
		f.env["TEST_CONNECT_ERROR"] = "org.freedesktop.libqmi.Error.Protocol.DeviceNotReady private-test-text"
		_, e := f.shell("poll_lte")
		no(t, e == nil, "failure ignored")
		equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), "modem_not_ready")
		no(t, strings.Contains(read(t, filepath.Join(f.r, "error")), "private-test-text"), "raw error exposed")
		no(t, exists(filepath.Join(f.r, "lte-mmcli-error")), "raw error retained")
	})
	t.Run("missing_sim_priority", func(t *testing.T) {
		f := newFixture(t)
		write(t, f.env["TEST_MODEM"], `{"modem":{"generic":{"state":"failed","state-failed-reason":"sim-missing","unlock-required":"unknown"}}}`)
		_, e := f.shell("poll_lte")
		no(t, e == nil, "missing SIM accepted")
		equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), "sim_missing")
	})
	for _, v := range []struct{ name, properties, reason string }{{"pin_forbidden", "apn=internet,pin=1234\n", ""}, {"apn_missing", "# unset\n", "apn_required"}, {"ipv6_only", "apn=internet,ip-type=ipv6\n", ""}} {
		t.Run(v.name, func(t *testing.T) {
			f := newFixture(t)
			f.modem("registered", "none", false)
			write(t, filepath.Join(f.e, "lte.properties"), v.properties)
			_, e := f.shell("poll_lte")
			no(t, e == nil, "invalid request accepted")
			for _, c := range f.calls() {
				no(t, strings.Contains(strings.Join(c, " "), "--simple-connect="), "invalid connection attempted")
			}
			if v.reason != "" {
				equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), v.reason)
			}
		})
	}
	t.Run("explicit_ipv4", func(t *testing.T) {
		f := newFixture(t)
		f.modem("registered", "none", false)
		write(t, filepath.Join(f.e, "lte.properties"), "apn=internet\n")
		_, e := f.shell("poll_lte")
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, c := range f.calls() {
			if strings.Contains(strings.Join(c, " "), "--simple-connect=apn=internet,ip-type=ipv4") {
				found = true
			}
		}
		no(t, !found, "IPv4 not requested")
	})
	t.Run("disconnect_cleanup", func(t *testing.T) {
		f := newFixture(t)
		_, e := f.shell("poll_lte")
		if e != nil {
			t.Fatal(e)
		}
		f.modem("failed", "none", true)
		_, e = f.shell("poll_lte")
		no(t, e == nil, "failed modem connected")
		no(t, exists(filepath.Join(f.r, "lte-dns")) || exists(filepath.Join(f.r, "lte-iface")), "stale network state")
		no(t, !f.hasCall("ip", "-4", "route", "flush", "dev", "wwan0", "proto", "186"), "routes retained")
	})
	t.Run("stats_no_reapply", func(t *testing.T) {
		f := newFixture(t)
		_, e := f.shell("poll_lte")
		if e != nil {
			t.Fatal(e)
		}
		os.Remove(filepath.Join(f.r, "calls"))
		f.bearer("wwan0", "static", "10.20.30.40", "yes", 10)
		_, e = f.shell("poll_lte")
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range f.calls() {
			no(t, c[0] == "ip", "stats caused address changes")
		}
	})
	t.Run("native_cache", func(t *testing.T) {
		f := newFixture(t)
		m := object(t, read(t, f.env["TEST_MODEM"]))
		m["modem"].(map[string]any)["dbus-path"] = "/Modem/0"
		write(t, filepath.Join(f.r, "modem-control.json"), js(m))
		write(t, filepath.Join(f.r, "bearer-control.json"), read(t, f.env["TEST_BEARER"]))
		f.env["MICROSTICK_CACHED_MODEM"] = "yes"
		_, e := f.shell("poll_lte")
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range f.calls() {
			no(t, c[0] == "mmcli", "cache queried mmcli")
		}
	})
	t.Run("dhcp_hook", func(t *testing.T) {
		f := newFixture(t)
		for k, v := range map[string]string{"interface": "wlan0", "ip": "10.11.12.13", "subnet": "255.255.255.0", "router": "10.11.12.1", "dns": "8.8.8.8 1.1.1.1"} {
			f.env[k] = v
		}
		ok(t, f.env, "", "sh", overlay("usr/libexec/microstick-dhcp"), "bound")
		no(t, !f.hasCall("ip", "-4", "addr", "add", "10.11.12.13/24", "dev", "wlan0"), "DHCP address missing")
		no(t, !f.hasCall("ip", "-4", "route", "replace", "default", "via", "10.11.12.1", "dev", "wlan0", "onlink", "metric", "100", "proto", "186"), "DHCP route missing")
		equal(t, read(t, filepath.Join(f.r, "wifi-dns")), "nameserver 8.8.8.8\nnameserver 1.1.1.1\n")
		ok(t, f.env, "", "sh", overlay("usr/libexec/microstick-dhcp"), "deconfig")
		no(t, exists(filepath.Join(f.r, "wifi-dns")), "DHCP DNS retained")
	})
	t.Run("dhcp_invalid", func(t *testing.T) {
		f := newFixture(t)
		for _, v := range [][2]string{{"192.168.68.10", "255.255.255.0"}, {"10.1.2.3", "255.0.255.0"}} {
			write(t, filepath.Join(f.r, "wifi-dns"), "nameserver 8.8.8.8\n")
			f.env["interface"] = "wlan0"
			f.env["ip"] = v[0]
			f.env["subnet"] = v[1]
			f.env["router"] = "10.1.2.1"
			_, e := run(t, f.env, "", "sh", overlay("usr/libexec/microstick-dhcp"), "bound")
			no(t, e == nil, "invalid DHCP accepted")
			no(t, exists(filepath.Join(f.r, "wifi-dns")), "invalid DHCP DNS retained")
		}
		for _, c := range f.calls() {
			s := " " + strings.Join(c, " ") + " "
			no(t, strings.Contains(s, " add ") || strings.Contains(s, " replace "), "invalid DHCP changed network")
		}
	})
}

func TestShellSyntax(t *testing.T) {
	e := filepath.WalkDir(external, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			if strings.HasPrefix(string(b), "#!/bin/sh") {
				ok(t, nil, "", "sh", "-n", p)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
