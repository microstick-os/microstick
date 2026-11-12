package firmware

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRouter(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		f := newFixture(t)
		cfg := object(t, f.routerOK(nil, "config"))
		f.routerOK(cfg, "validate", "lan")
		for _, v := range []struct {
			k string
			v any
		}{{"address", "192.168.100.0/24"}, {"address", "8.8.8.1/24"}, {"address", "192.168.68.1/24"}, {"start", "192.168.100.1"}, {"end", "192.168.101.9"}, {"lease", 10}, {"dns", []string{"1.1.1.1\ninterface=wlan0"}}} {
			x := object(t, js(cfg))
			x["ap"].(map[string]any)[v.k] = v.v
			_, e := f.router(x, "validate", "lan")
			no(t, e == nil, "invalid LAN accepted: "+v.k)
		}
		for _, mac := range []string{"01:12:34:56:78:90", "ff:ff:ff:ff:ff:ff", "$(reboot)"} {
			x := object(t, js(cfg))
			x["reservations"] = []any{map[string]any{"network": "ap", "mac": mac, "ip": "192.168.100.9", "name": "Phone"}}
			_, e := f.router(x, "validate", "lan")
			no(t, e == nil, "invalid MAC accepted")
		}
		cfg["reservations"] = []any{map[string]string{"network": "ap", "mac": "02:12:34:56:78:90", "ip": "192.168.100.9", "name": "Phone"}, map[string]string{"network": "ap", "mac": "02:12:34:56:78:90", "ip": "192.168.100.9", "name": "Phone"}}
		_, e := f.router(cfg, "validate", "lan")
		no(t, e == nil, "duplicate reservation accepted")
	})
	t.Run("dnsmasq_modes_subnet", func(t *testing.T) {
		f := newFixture(t)
		cfg := object(t, f.routerOK(nil, "config"))
		for _, mode := range []string{"ap", "sta", "off"} {
			f.routerOK(nil, "mode", mode)
			s := read(t, filepath.Join(f.r, "dnsmasq.conf"))
			contains(t, s, "interface=usb0")
			equal(t, strings.Contains(s, "interface=wlan0"), mode == "ap")
			equal(t, strings.Contains(s, "dhcp-range=set:ap"), mode == "ap")
		}
		ap := cfg["ap"].(map[string]any)
		ap["address"] = "192.168.2.1/23"
		ap["start"] = "192.168.2.100"
		ap["end"] = "192.168.3.200"
		ap["lease"] = 7200
		ap["dns"] = []string{"9.9.9.9"}
		f.routerOK(cfg, "stage")
		f.routerOK(nil, "mode", "ap")
		s := read(t, filepath.Join(f.r, "dnsmasq.conf"))
		contains(t, s, "255.255.254.0,7200s")
		contains(t, s, "6,9.9.9.9")
	})
	t.Run("confirm_timeout_reboot_failed_apply", func(t *testing.T) {
		f := newFixture(t)
		cfg := object(t, f.routerOK(nil, "config"))
		v := object(t, js(cfg))
		ap := v["ap"].(map[string]any)
		ap["address"] = "192.168.2.1/24"
		ap["start"] = "192.168.2.10"
		ap["end"] = "192.168.2.200"
		pending := object(t, f.routerOK(v, "stage"))["pending"].(map[string]any)
		no(t, exists(filepath.Join(f.e, "lan.json")), "unconfirmed LAN persisted")
		_, e := f.router(map[string]string{"token": "wrong"}, "confirm")
		no(t, e == nil, "wrong token accepted")
		f.routerOK(map[string]any{"token": pending["token"]}, "confirm")
		equal(t, object(t, read(t, filepath.Join(f.e, "lan.json")))["ap"].(map[string]any)["address"], "192.168.2.1/24")
		f.routerOK(cfg, "stage")
		write(t, filepath.Join(f.root, "uptime"), "221.0 0.0")
		f.routerOK(nil, "tick")
		no(t, exists(filepath.Join(f.r, "lan-pending.json")), "expired pending retained")
		equal(t, object(t, f.routerOK(nil, "config"))["ap"].(map[string]any)["address"], "192.168.2.1/24")
		f.routerOK(cfg, "stage")
		os.Remove(filepath.Join(f.r, "lan-pending.json"))
		equal(t, object(t, f.routerOK(nil, "config"))["ap"].(map[string]any)["address"], "192.168.2.1/24")
		before := read(t, filepath.Join(f.e, "lan.json"))
		f.env["FAIL_DNS"] = "1"
		_, e = f.router(cfg, "stage")
		no(t, e == nil, "failed DNS apply accepted")
		equal(t, read(t, filepath.Join(f.e, "lan.json")), before)
		no(t, exists(filepath.Join(f.r, "lan-pending.json")), "failed candidate retained")
	})
	t.Run("range_reservation_clients", func(t *testing.T) {
		f := newFixture(t)
		cfg := object(t, f.routerOK(nil, "config"))
		write(t, filepath.Join(f.r, "dnsmasq.leases"), "9999999999 02:12:34:56:78:90 192.168.100.15 phone *\n")
		cfg["ap"].(map[string]any)["end"] = "192.168.100.50"
		cfg["reservations"] = []any{map[string]string{"network": "ap", "mac": "02:12:34:56:78:90", "ip": "192.168.100.15", "name": "<script>name</script>"}}
		equal(t, object(t, f.routerOK(cfg, "stage"))["pending"], nil)
		equal(t, object(t, read(t, filepath.Join(f.r, "lan-public.json")))["pending"], nil)
		client := object(t, f.routerOK(nil, "clients"))["clients"].([]any)[0].(map[string]any)
		equal(t, client["name"], "<script>name</script>")
		equal(t, client["activity"], "lease_only")
		equal(t, client["reserved"], true)
		contains(t, read(t, filepath.Join(f.r, "dhcp-hosts")), "02:12:34:56:78:90,192.168.100.15")
	})
	t.Run("wan_overlap_occupied_reservation", func(t *testing.T) {
		f := newFixture(t)
		cfg := object(t, f.routerOK(nil, "config"))
		f.env["TEST_WAN"] = `[{"ifname":"wwan0","addr_info":[{"family":"inet","local":"192.168.100.2","prefixlen":24}]}]`
		_, e := f.router(cfg, "stage")
		no(t, e == nil, "WAN overlap accepted")
		delete(f.env, "TEST_WAN")
		write(t, filepath.Join(f.r, "clients-all.json"), `[{"network":"ap","mac":"02:aa:aa:aa:aa:aa","ips":["192.168.100.15"]}]`)
		cfg["reservations"] = []any{map[string]string{"network": "ap", "mac": "02:12:34:56:78:90", "ip": "192.168.100.15", "name": "x"}}
		_, e = f.router(cfg, "stage")
		no(t, e == nil, "occupied address accepted")
	})
	t.Run("firewall_directions_rollback", func(t *testing.T) {
		f := newFixture(t)
		f.routerOK(nil, "tick")
		write(t, filepath.Join(f.r, "uplink"), "wwan0")
		write(t, filepath.Join(f.r, "clients-all.json"), `[{"network":"ap","mac":"02:12:34:56:78:90","ips":["192.168.100.15"]}]`)
		rule := map[string]any{"name": "block", "enabled": true, "network": "ap", "mac": "02:12:34:56:78:90", "destination": "1.1.1.1", "protocol": "both", "port": "80-90"}
		cfg := map[string]any{"rules": []any{rule}}
		f.routerOK(cfg, "firewall")
		s := read(t, filepath.Join(f.r, "firewall.rules"))
		contains(t, s, "--mac-source 02:12:34:56:78:90 -d 1.1.1.1 -j DROP")
		contains(t, s, "-i wwan0 -o wlan0 -d 192.168.100.15 -p udp --sport 80:90 -s 1.1.1.1 -j DROP")
		no(t, strings.Index(s, "--mac-source") >= strings.Index(s, "-A MICROSTICK_FORWARD -m conntrack"), "drop ordering wrong")
		before := read(t, filepath.Join(f.e, "firewall.json"))
		rule["port"] = "443"
		f.env["FAIL_FIREWALL"] = "1"
		_, e := f.router(cfg, "firewall")
		no(t, e == nil, "failed firewall accepted")
		equal(t, read(t, filepath.Join(f.e, "firewall.json")), before)
		rule["port"] = "80;reboot"
		_, e = f.router(cfg, "validate", "firewall")
		no(t, e == nil, "injection accepted")
	})
	t.Run("service_validation_sta_dynamic_subnet", func(t *testing.T) {
		f := newFixture(t)
		service := map[string]any{"name": "Program", "enabled": true, "protocol": "both", "port": "8000-8010", "source": ""}
		for _, v := range []struct{ k, v string }{{"port", "0"}, {"port", "65536"}, {"port", "9000-8000"}, {"port", "1;reboot"}, {"protocol", "any"}, {"source", "192.168.1.1/33"}, {"source", "1.2.3.4\n-j ACCEPT"}} {
			s := object(t, js(service))
			s[v.k] = v.v
			_, e := f.router(map[string]any{"rules": []any{}, "services": []any{s}}, "validate", "firewall")
			no(t, e == nil, "bad service accepted")
		}
		f.routerOK(nil, "mode", "sta")
		cfg := map[string]any{"rules": []any{}, "services": []any{service}}
		f.routerOK(cfg, "firewall")
		s := read(t, filepath.Join(f.r, "firewall.rules"))
		contains(t, s, "-i wlan0 -s 192.168.31.77/24 -p tcp --dport 8000:8010 -j ACCEPT")
		contains(t, s, "-i wlan0 -s 192.168.31.77/24 -p udp --dport 8000:8010 -j ACCEPT")
		contains(t, s, "--dport 5353 -j ACCEPT")
		no(t, strings.Contains(s, "-i wwan0 -p tcp --dport 8000"), "service exposed to LTE")
		f.env["TEST_STA"] = "192.168.9.3/24"
		f.routerOK(nil, "apply-firewall")
		contains(t, read(t, filepath.Join(f.r, "firewall.rules")), "-s 192.168.9.3/24 -p tcp --dport 8000:8010")
		service["enabled"] = false
		f.routerOK(cfg, "firewall")
		no(t, strings.Contains(read(t, filepath.Join(f.r, "firewall.rules")), "--dport 8000:8010"), "disabled service retained")
	})
}
func TestRouterEvents(t *testing.T) {
	binary := compile(t, "routerd", []string{source("event.c"), source("clients.c"), source("routerd.c")}, []string{"gio-unix-2.0", "libip4tc"}, nil)
	setup := func(t *testing.T) *fixture {
		f := newFixture(t)
		write(t, filepath.Join(f.r, "usb-iface"), "testusb0")
		write(t, filepath.Join(f.r, "wifi-interface"), "testwlan0")
		f.env["MICROSTICK_ROUTER_DAEMON"] = binary
		f.env["MICROSTICK_UPTIME"] = "/proc/uptime"
		f.env["TEST_OWNER_PID"] = strconv.Itoa(os.Getpid())
		start(t, f.env, binary)
		until(t, 8*time.Second, func() bool { return exists(filepath.Join(f.r, "router-lan-inputs.json")) })
		time.Sleep(300 * time.Millisecond)
		return f
	}
	t.Run("idle_duplicate_inputs_on_demand", func(t *testing.T) {
		f := setup(t)
		sock := filepath.Join(f.r, "router.sock")
		q := func(m string) map[string]any { return rpc(t, sock, map[string]string{"method": m}) }
		before := q("diagnostics")
		calls := read(t, filepath.Join(f.r, "calls"))
		for i := 0; i < 20; i++ {
			p := filepath.Join(f.e, "network.conf")
			write(t, p, read(t, p))
		}
		for i := 0; i < 20; i++ {
			equal(t, q("clients")["total"], float64(0))
		}
		time.Sleep(500 * time.Millisecond)
		equal(t, q("diagnostics"), before)
		equal(t, read(t, filepath.Join(f.r, "calls")), calls)
		no(t, exists(filepath.Join(f.r, "lan-public.json")) || exists(filepath.Join(f.r, "clients-0.json")), "periodic status files created")
	})
	t.Run("lease_no_lan_reapply", func(t *testing.T) {
		f := setup(t)
		sock := filepath.Join(f.r, "router.sock")
		before := rpc(t, sock, map[string]string{"method": "diagnostics"})
		write(t, filepath.Join(f.r, "dnsmasq.leases"), "9999999999 02:12:34:56:78:90 192.168.100.15 phone *\n")
		until(t, 8*time.Second, func() bool {
			b, _ := os.ReadFile(filepath.Join(f.r, "client-bindings.json"))
			return strings.Contains(string(b), "192.168.100.15")
		})
		c := rpc(t, sock, map[string]string{"method": "clients"})["clients"].([]any)[0].(map[string]any)
		equal(t, c["hostname"], "phone")
		equal(t, c["ips"], []any{"192.168.100.15"})
		time.Sleep(300 * time.Millisecond)
		equal(t, rpc(t, sock, map[string]string{"method": "diagnostics"}), before)
	})
	t.Run("pending_deadline", func(t *testing.T) {
		f := setup(t)
		cfg := object(t, f.routerOK(nil, "config"))
		fields := strings.Fields(read(t, "/proc/uptime"))
		up, _ := strconv.ParseFloat(fields[0], 64)
		write(t, filepath.Join(f.r, "lan-pending.json"), js(map[string]any{"config": cfg, "token": "x", "deadline": int(up) + 2}))
		until(t, 5*time.Second, func() bool { return !exists(filepath.Join(f.r, "lan-pending.json")) })
		equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "router-notice"))), "lan_reverted")
		equal(t, rpc(t, filepath.Join(f.r, "router.sock"), map[string]string{"method": "lan_config"})["pending"], nil)
	})
}
