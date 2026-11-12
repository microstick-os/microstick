package firmware

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func (f *fixture) action(method string, payload any) (map[string]any, error) {
	p := filepath.Join(f.root, "request.json")
	write(f.t, p, js(payload))
	_, e := run(f.t, f.env, "", "sh", overlay("usr/libexec/microstick-action"), method, p)
	return object(f.t, read(f.t, filepath.Join(f.r, "result.json"))), e
}
func settings(t *testing.T) *fixture {
	f := newFixture(t)
	write(t, filepath.Join(f.e, "network.conf"), "WIFI_MODE=ap\nLTE_ENABLED=yes\n")
	f.env["MICROSTICK_DEFAULT_IMEI"] = filepath.Join(f.root, "default-imei.json")
	for _, n := range []string{"sync", "sleep", "reboot"} {
		write(t, filepath.Join(f.root, "bin", n), "#!/bin/sh\nexit 0\n")
	}
	return f
}
func TestSettings(t *testing.T) {
	t.Run("exact_cli_json", func(t *testing.T) {
		f := settings(t)
		core := filepath.Join(f.root, "bin/core")
		write(t, core, "#!/bin/sh\nprintf '%s\\n' \"$1\"\n")
		cli := filepath.Join(f.root, "cli")
		text := strings.ReplaceAll(read(t, overlay("usr/sbin/microstick")), "/usr/libexec/microstick-core", core)
		write(t, cli, strings.ReplaceAll(text, "/run/microstick", f.r))
		for _, p := range []string{`{"method":"ttl","enabled":true,"value":"64"}`, `{"method":"imei","imei":"490154203237518"}`} {
			equal(t, strings.TrimSpace(ok(t, f.env, "", "sh", cli, "api", p)), p)
		}
		equal(t, strings.TrimSpace(ok(t, f.env, "", "sh", cli, "api")), "{}")
	})
	t.Run("ttl_atomic_failure", func(t *testing.T) {
		f := settings(t)
		helper := filepath.Join(f.root, "bin/ttl")
		write(t, helper, "#!/bin/sh\ntest \"${TEST_FAIL:-no}\" != yes\n")
		f.env["MICROSTICK_TTL"] = helper
		p := filepath.Join(f.e, "ttl.conf")
		before := "FIX_TTL_ENABLED=no\nFIX_TTL_VALUE=64\n"
		write(t, p, before)
		for _, v := range []any{"0", "256", "64;reboot", "1.5", "", nil, 64} {
			r, e := f.action("ttl", map[string]any{"enabled": true, "value": v})
			no(t, e == nil, "bad TTL accepted")
			want := "invalid_ttl"
			if v == nil || v == 64 {
				want = "invalid_request"
			}
			equal(t, r["error"], want)
			equal(t, read(t, p), before)
		}
		f.env["TEST_FAIL"] = "yes"
		r, _ := f.action("ttl", map[string]any{"enabled": true, "value": "64"})
		equal(t, r["error"], "ttl_apply_failed")
		equal(t, read(t, p), before)
		delete(f.env, "TEST_FAIL")
		_, e := f.action("ttl", map[string]any{"enabled": true, "value": "064"})
		if e != nil {
			t.Fatal(e)
		}
		contains(t, read(t, p), "FIX_TTL_ENABLED=yes")
		st, _ := os.Stat(p)
		equal(t, st.Mode().Perm(), os.FileMode(0600))
	})
	t.Run("ttl_scope_idempotence_reboot", func(t *testing.T) {
		f := settings(t)
		nft := filepath.Join(f.root, "bin/nft")
		write(t, nft, `#!/bin/sh
if [ "$1" = list ]; then test -f "$MICROSTICK_RUN/table"; exit; fi
cat "$2" >> "$MICROSTICK_RUN/rules"
touch "$MICROSTICK_RUN/table"
`)
		f.env["MICROSTICK_NFT"] = nft
		cfg := filepath.Join(f.e, "ttl.conf")
		write(t, cfg, "FIX_TTL_ENABLED=yes\nFIX_TTL_VALUE=64\n")
		apply := func() { ok(t, f.env, "", "sh", overlay("usr/libexec/microstick-ttl")) }
		apply()
		rules := read(t, filepath.Join(f.r, "rules"))
		contains(t, rules, `oifname "wwan*" ip ttl set 64 counter`)
		no(t, strings.Contains(rules, "flush ruleset"), "global flush")
		apply()
		equal(t, read(t, filepath.Join(f.r, "rules")), rules)
		os.Remove(filepath.Join(f.r, "ttl-applied"))
		os.Remove(filepath.Join(f.r, "table"))
		apply()
		write(t, cfg, "FIX_TTL_ENABLED=no\nFIX_TTL_VALUE=64\n")
		apply()
		no(t, strings.Contains(read(t, filepath.Join(f.r, "ttl.nft")), "ip ttl set"), "disabled TTL still active")
	})
	t.Run("imei_override_reset_boot", func(t *testing.T) {
		f := settings(t)
		helper := filepath.Join(f.root, "bin/nv")
		write(t, helper, `#!/bin/sh
case "$1" in
validate) jq -e '.imei=="490154203237518"' >/dev/null;;
read) echo '{"ok":true,"current":"490154203237518"}';;
set) cat > "$MICROSTICK_RUN/desired"; echo '{"ok":true}';;
esac
`)
		f.env["MICROSTICK_IMEI_NV"] = helper
		p := filepath.Join(f.e, "imei.json")
		_, e := f.action("imei", map[string]string{"imei": "bad"})
		no(t, e == nil || exists(p), "invalid IMEI persisted")
		write(t, f.env["MICROSTICK_DEFAULT_IMEI"], `{"imei":"490154203237526"}`)
		apply := func() string {
			ok(t, f.env, "", "sh", overlay("usr/libexec/microstick-imei-apply"))
			return object(t, read(t, filepath.Join(f.r, "desired")))["imei"].(string)
		}
		equal(t, apply(), "490154203237526")
		_, e = f.action("imei", map[string]string{"imei": "490154203237518"})
		if e != nil {
			t.Fatal(e)
		}
		st, _ := os.Stat(p)
		equal(t, st.Mode().Perm(), os.FileMode(0600))
		for i := 0; i < 2; i++ {
			os.Remove(filepath.Join(f.r, "desired"))
			equal(t, apply(), "490154203237518")
		}
		_, e = f.action("imei", map[string]bool{"restore": true})
		if e != nil {
			t.Fatal(e)
		}
		no(t, exists(p), "override retained")
		equal(t, apply(), "490154203237526")
		os.Remove(f.env["MICROSTICK_DEFAULT_IMEI"])
		os.Remove(filepath.Join(f.r, "desired"))
		ok(t, f.env, "", "sh", overlay("usr/libexec/microstick-imei-apply"))
		no(t, exists(filepath.Join(f.r, "desired")), "base generated default")
	})
	t.Run("diag_codec_fixed_nv", func(t *testing.T) {
		b := compile(t, "diag", []string{filepath.Join(external, "tests/imei_test.c")}, nil, nil)
		ok(t, nativeEnv(nil), "", b)
	})
}
func TestActions(t *testing.T) {
	t.Run("invalid_wifi_preserves_saved", func(t *testing.T) {
		f := newFixture(t)
		write(t, filepath.Join(f.e, "network.conf"), "WIFI_MODE=ap\n")
		saved := "ctrl_interface=/run/wpa_supplicant\nnetwork={\n ssid=\"saved\"\n}\n"
		write(t, filepath.Join(f.e, "wpa_supplicant.conf"), saved)
		p := filepath.Join(f.root, "bin/validate")
		write(t, p, "#!/bin/sh\nexit 1\n")
		f.env["MICROSTICK_WIFI_TOOL"] = p
		r, e := f.action("connect", map[string]string{"ssid": "", "security": "open"})
		no(t, e == nil, "invalid wifi accepted")
		equal(t, r["error"], "invalid_wifi_parameters")
		equal(t, read(t, filepath.Join(f.e, "wpa_supplicant.conf")), saved)
		contains(t, read(t, filepath.Join(f.e, "network.conf")), "WIFI_MODE=ap\n")
		no(t, exists(filepath.Join(f.r, "wifi-candidate.conf")), "invalid candidate retained")
	})
	t.Run("pin_once", func(t *testing.T) {
		f := newFixture(t)
		p := filepath.Join(f.root, "modem")
		write(t, p, "#!/bin/sh\ncat >/dev/null\necho call >> \"$MICROSTICK_RUN/modem-calls\"\necho '{\"ok\":true}'\n")
		f.env["MICROSTICK_MODEM"] = p
		r, e := f.action("pin", map[string]string{"pin": "1234"})
		if e != nil {
			t.Fatal(e)
		}
		equal(t, read(t, filepath.Join(f.r, "modem-calls")), "call\n")
		equal(t, r["message"], "pin_accepted")
		no(t, exists(filepath.Join(f.root, "request.json")) || !exists(filepath.Join(f.r, "lte-retry")), "PIN request/retry lifecycle")
	})
	t.Run("lte_durable", func(t *testing.T) {
		f := newFixture(t)
		for _, v := range []bool{true, false} {
			_, e := f.action("lte", map[string]bool{"enabled": v})
			if e != nil {
				t.Fatal(e)
			}
			os.Remove(filepath.Join(f.r, "lte-request"))
			want := "no"
			if v {
				want = "yes"
			}
			contains(t, read(t, filepath.Join(f.e, "network.conf")), "LTE_ENABLED="+want+"\n")
		}
	})
	t.Run("policy_invalid", func(t *testing.T) {
		f := newFixture(t)
		p := filepath.Join(f.e, "network.conf")
		write(t, p, "WIFI_MODE=ap\nUPLINK_MODE=auto\nLTE_ENABLED=no\nUSB_ADDRESS=192.168.68.1/24\n")
		write(t, filepath.Join(f.e, "wpa_supplicant.conf"), "ctrl_interface=/run/wpa_supplicant\nnetwork={\n ssid=74657374\n}\n")
		for _, mode := range []string{"wifi", "lte", "auto"} {
			write(t, filepath.Join(f.r, "auto-initialized"), "")
			_, e := f.action("mode", map[string]string{"mode": mode})
			if e != nil {
				t.Fatal(e)
			}
			s := read(t, p)
			contains(t, s, "UPLINK_MODE="+mode+"\n")
			if mode == "wifi" {
				contains(t, s, "WIFI_MODE=sta\n")
				contains(t, s, "LTE_ENABLED=no\n")
			} else if mode == "lte" {
				contains(t, s, "WIFI_MODE=ap\n")
				contains(t, s, "LTE_ENABLED=yes\n")
			} else {
				no(t, exists(filepath.Join(f.r, "auto-initialized")) || exists(filepath.Join(f.r, "lte-request")), "auto runtime retained")
			}
		}
		before := read(t, p)
		for _, mode := range []any{"ap", "invalid", "wifi; reboot", nil} {
			v := map[string]any{}
			if mode != nil {
				v["mode"] = mode
			}
			r, e := f.action("mode", v)
			no(t, e == nil, "invalid policy accepted")
			equal(t, r["error"], "invalid_mode")
			equal(t, read(t, p), before)
		}
	})
}
func TestPluginLifecycle(t *testing.T) {
	r := t.TempDir()
	data, defaults, runtime := filepath.Join(r, "data"), filepath.Join(r, "defaults"), filepath.Join(r, "runtime")
	for _, p := range []string{data, defaults} {
		os.MkdirAll(p, 0755)
	}
	plugin := func(p, s string) { write(t, filepath.Join(p, "run"), s) }
	plugin(filepath.Join(defaults, "web"), "#!/bin/sh\nexit 0\n")
	write(t, filepath.Join(defaults, "README.md"), "plugin contract")
	write(t, filepath.Join(data, "plugin-state/web/auth.json"), "preserve-auth")
	marker := filepath.Join(r, "cleanup-called")
	write(t, filepath.Join(defaults, "web/cleanup"), "#!/bin/sh\nprintf done > '"+marker+"'\n")
	for _, n := range []string{"custom", "container/nested", "disabled", "writable"} {
		plugin(filepath.Join(data, "plugins", n), "#!/bin/sh\nexit 0\n")
	}
	write(t, filepath.Join(data, "plugins/disabled/disabled"), "")
	os.Chmod(filepath.Join(data, "plugins/writable"), 0777)
	os.Symlink(filepath.Join(defaults, "web"), filepath.Join(data, "plugins/link"))
	script := filepath.Join(r, "loader")
	s := read(t, overlay("usr/libexec/microstick-plugins"))
	gate := filepath.Join(r, "mount-check")
	ready := filepath.Join(r, "mounted")
	write(t, gate, "#!/bin/sh\n[ -f '"+ready+"' ]\n")
	s = strings.ReplaceAll(s, "/usr/libexec/microstick-data", gate)
	s = strings.ReplaceAll(s, "/data", data)
	s = strings.ReplaceAll(s, "/run/microstick/plugins", runtime)
	s = strings.ReplaceAll(s, "/usr/share/microstick/default-plugins", defaults)
	s = strings.ReplaceAll(s, "= 0 ]", "= "+strconv.Itoa(os.Getuid())+" ]")
	write(t, script, s)
	if _, e := run(t, nil, "", script, "rescan"); e == nil {
		t.Fatal("plugins ran without data mount")
	}
	no(t, exists(runtime) || exists(filepath.Join(data, ".default-plugins")), "loader wrote beneath unmounted data")
	write(t, ready, "")
	rescan := func() { ok(t, nil, "", script, "rescan") }
	rescan()
	entries, _ := os.ReadDir(runtime)
	names := []string{}
	for _, d := range entries {
		names = append(names, d.Name())
	}
	equal(t, names, []string{"custom", "web"})
	equal(t, read(t, filepath.Join(data, "plugins/README.md")), "plugin contract")
	equal(t, read(t, filepath.Join(data, "plugins/web/.state/auth.json")), "preserve-auth")
	no(t, exists(filepath.Join(data, "plugin-state/web")), "state not migrated")
	contains(t, read(t, filepath.Join(runtime, "web/run")), filepath.Join(data, "plugins/web/.state"))
	write(t, filepath.Join(data, "plugins/README.md"), "user documentation")
	write(t, filepath.Join(data, "plugins/web/run"), "user-customized")
	write(t, filepath.Join(defaults, "web/run"), "new default")
	rescan()
	equal(t, read(t, filepath.Join(data, "plugins/web/run")), "user-customized")
	os.RemoveAll(filepath.Join(data, "plugins/web"))
	plugin(filepath.Join(defaults, "new"), "#!/bin/sh\nexit 0\n")
	rescan()
	equal(t, read(t, filepath.Join(data, "plugins/README.md")), "user documentation")
	no(t, exists(filepath.Join(runtime, "web")) || exists(filepath.Join(data, "plugins/web")), "deleted plugin restored")
	equal(t, read(t, marker), "done")
	no(t, !exists(filepath.Join(data, "plugins/new/run")), "new default not seeded")
}
func TestRmtfsRAMShadow(t *testing.T) {
	src, _ := filepath.Glob(filepath.Join(workspace, "buildroot-output/build/microstick-rmtfs-*/storage.c"))
	qrtr, _ := filepath.Glob(filepath.Join(workspace, "buildroot-output/build/microstick-qrtr-*/lib"))
	if len(src) != 1 || len(qrtr) != 1 {
		t.Fatal("build pinned rmtfs/qrtr first")
	}
	root := t.TempDir()
	common := []string{"-g", "-O1", "-I" + qrtr[0], `-DRMTFS_STORAGE_SOURCE="` + src[0] + `"`, filepath.Join(external, "tests/rmtfs_storage_test.c")}
	host := filepath.Join(root, "host")
	args := append([]string{"-fsanitize=address,undefined", "-fno-omit-frame-pointer", "-fno-pie", "-no-pie"}, common...)
	args = append(args, "-o", host)
	ok(t, nil, "", "cc", args...)
	ok(t, nativeEnv(nil), "", host)
	arm := filepath.Join(root, "arm")
	args = append(append([]string{}, common...), "-o", arm)
	ok(t, nil, "", filepath.Join(workspace, "buildroot-output/host/bin/aarch64-linux-gcc"), args...)
	ok(t, nil, "", filepath.Join(workspace, "buildroot-output/host/bin/qemu-aarch64"), "-L", filepath.Join(workspace, "buildroot-output/target"), arm)
}
