package firmware

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var baseStatus = map[string]any{"wifi": "ap", "policy": "lte", "lte_enabled": true, "uplink": "", "lte": map[string]any{}, "modem": map[string]any{"modem": map[string]any{"generic": map[string]any{"state": "disabled", "unlock-required": "sim-pin2", "unlock-retries": []string{"sim-pin (3)"}}, "3gpp": map[string]any{}}}}
var baseStatistics = map[string]any{"api_version": 1, "error": "", "clock_synced": true, "timezone": "UTC +0000", "lte": map[string]any{"rx": "104857600", "tx": "10485760", "total_rx": "104857600", "total_tx": "10485760", "reset_at": 1793760000, "undated_rx": "0", "undated_tx": "0", "rx_bps": 1250000, "tx_bps": 125000}, "wifi": map[string]any{"rx": "42949672960", "tx": "1000000", "connected": false, "session_seconds": 200, "rx_bps": 0, "tx_bps": 0}, "system": map[string]any{"uptime": 7200, "cpu_percent": 2, "memory_used": 60000000, "memory_total": 400000000, "modem_temperature": 53}}

func baseLAN() map[string]any {
	out := map[string]any{"reservations": []any{}}
	for k, sub := range map[string]int{"usb": 68, "ap": 100} {
		out[k] = map[string]any{"address": fmt.Sprintf("192.168.%d.1/24", sub), "enabled": true, "start": fmt.Sprintf("192.168.%d.10", sub), "end": fmt.Sprintf("192.168.%d.200", sub), "lease": 3600, "dns": []any{}}
	}
	return out
}

type panel struct {
	t               *testing.T
	driver, session string
	mu              sync.Mutex
	writes          []map[string]any
	config          map[string]any
	statusData      map[string]any
}

func (p *panel) call(path string, data any, method string) any {
	p.t.Helper()
	if method == "" {
		method = "GET"
		if data != nil {
			method = "POST"
		}
	}
	var b io.Reader
	if data != nil {
		b = bytes.NewBufferString(js(data))
	}
	req, e := http.NewRequest(method, p.driver+path, b)
	if e != nil {
		p.t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	c := http.Client{Timeout: 30 * time.Second}
	r, e := c.Do(req)
	if e != nil {
		p.t.Fatal(e)
	}
	defer r.Body.Close()
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		p.t.Fatal(e)
	}
	out := object(p.t, string(raw))["value"]
	if v, ok := out.(map[string]any); ok && v["error"] != nil {
		p.t.Fatal("WebDriver:", v)
	}
	return out
}
func (p *panel) js(code string, args ...any) any {
	p.t.Helper()
	if args == nil {
		args = []any{}
	}
	return p.call(p.session+"/execute/sync", map[string]any{"script": code, "args": args}, "")
}
func (p *panel) check(code string, args ...any) { p.t.Helper(); equal(p.t, p.js(code, args...), true) }
func (p *panel) text(id string) string {
	p.t.Helper()
	return p.js("return document.getElementById(arguments[0]).textContent", id).(string)
}
func (p *panel) count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.writes) }
func (p *panel) next(start int) map[string]any {
	p.t.Helper()
	until(p.t, 3*time.Second, func() bool { return p.count() > start })
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writes[start]
}
func (p *panel) status(lock, state, reason string) {
	s := object(p.t, js(baseStatus))
	s["lte"] = map[string]string{"error": reason}
	g := s["modem"].(map[string]any)["modem"].(map[string]any)["generic"].(map[string]any)
	g["state"] = state
	g["unlock-required"] = lock
	p.js("render(arguments[0]);", s)
}
func newPanel(t *testing.T) *panel {
	t.Helper()
	p := &panel{t: t}
	files := http.FileServer(http.Dir(overlay("usr/share/microstick/default-plugins/web/www")))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			p.mu.Lock()
			p.writes = append(p.writes, map[string]any{"path": r.URL.Path, "body": object(t, string(b))})
			p.mu.Unlock()
			fmt.Fprint(w, `{"ok":true}`)
			return
		}
		var out any
		switch r.URL.Path {
		case "/api/session":
			out = map[string]string{"csrf": "synthetic-test-token"}
		case "/api/status":
			p.mu.Lock()
			out = baseStatus
			if p.statusData != nil {
				out = p.statusData
			}
			p.mu.Unlock()
		case "/api/statistics":
			out = baseStatistics
		case "/api/statistics/series":
			out = map[string]any{"source": "lte", "points": [][]int{{-15, 100000, 15000}, {-12, 200000, 12000}, {-9, 1250000, 125000}, {-6, 800000, 65000}, {-3, 700000, 50000}, {0, 1250000, 125000}}}
		case "/api/statistics/history":
			out = map[string]any{"buckets": []any{map[string]any{"date": 20261103, "rx": "104857600", "tx": "10485760"}}}
		case "/api/config":
			p.mu.Lock()
			out = map[string]any{}
			if p.config != nil {
				out = p.config
			}
			p.mu.Unlock()
		case "/api/lan_config":
			out = map[string]any{"config": baseLAN(), "pending": nil, "wifi_mode": "ap"}
		case "/api/firewall_config":
			out = map[string]any{"rules": []any{}}
		case "/api/clients":
			out = map[string]any{"clients": []any{map[string]any{"network": "ap", "mac": "02:12:34:56:78:90", "name": "<img src=x onerror=alert(1)>", "hostname": "phone", "ip": "192.168.100.15", "ips": []string{"192.168.100.15"}, "activity": "connected", "expires": 9999999999, "reserved": false}}, "total": 1, "page": 0}
		case "/api/operation":
			out = map[string]bool{"busy": false}
		default:
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, js(out))
	}))
	t.Cleanup(s.Close)
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	p.driver = fmt.Sprintf("http://127.0.0.1:%d", port)
	start(t, nil, "chromedriver", fmt.Sprintf("--port=%d", port))
	until(t, 5*time.Second, func() bool {
		c, e := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if e != nil {
			return false
		}
		c.Close()
		return true
	})
	session := p.call("/session", map[string]any{"capabilities": map[string]any{"alwaysMatch": map[string]any{"browserName": "chrome", "goog:chromeOptions": map[string]any{"binary": "/usr/bin/chromium", "args": []string{"--headless=new", "--no-sandbox", "--disable-dev-shm-usage"}}}}}, "").(map[string]any)
	p.session = "/session/" + session["sessionId"].(string)
	t.Cleanup(func() { p.call(p.session, nil, "DELETE") })
	p.call(p.session+"/url", map[string]string{"url": s.URL + "/"}, "")
	until(t, 5*time.Second, func() bool { return p.js("return !document.getElementById('app').hidden") == true })
	p.js("window.alert=window.confirm=window.prompt=()=>{throw Error('browser dialog used')};clearInterval(1);localStorage.setItem('language','ru');lang='ru';language();")
	return p
}
func TestPanelChromium(t *testing.T) {
	p := newPanel(t)
	t.Run("sim_errors", func(t *testing.T) {
		p.t = t
		p.status("sim-pin2", "disabled", "")
		contains(t, p.text("simstate"), "Запуск")
		p.check("return $('pinform').hidden&&!document.body.innerText.includes('PIN2/PUK2')")
		p.status("sim-pin", "locked", "")
		contains(t, p.text("ltetitle"), "PIN")
		contains(t, p.text("pinretries"), "3")
		p.js("$('mobileaction').click()")
		p.check("return !$('lte').hidden&&document.activeElement.id==='pin'")
		p.status("sim-puk", "locked", "")
		contains(t, p.text("ltedetail"), "оператора")
		p.check("return $('pinform').hidden")
		for _, e := range []string{"apn_required", "lte_connect_failed", "lte_timeout", "lte_ip_failed", "modem_unavailable", "modem_not_ready"} {
			p.status("none", "disabled", e)
			no(t, strings.Contains(p.text("ltedetail"), "_"), "raw error displayed")
			p.check("return !$('lteretry').hidden&&!$('ltedetail').hidden")
		}
		p.js("lang='en';language()")
		p.status("sim-pin", "locked", "")
		equal(t, p.text("simstate"), "SIM PIN required")
		p.js("lang='ru';language()")
	})
	t.Run("confirm_escape_cancel_accept_focus", func(t *testing.T) {
		p.t = t
		p.status("none", "disabled", "")
		n := p.count()
		p.js("selectTab('settings');$('reboot').focus();$('reboot').click()")
		p.check("return $('confirmation').open&&document.activeElement.value==='cancel'")
		p.call(p.session+"/actions", map[string]any{"actions": []any{map[string]any{"type": "key", "id": "keyboard", "actions": []any{map[string]string{"type": "keyDown", "value": "\ue00c"}, map[string]string{"type": "keyUp", "value": "\ue00c"}}}}}, "")
		until(t, 2*time.Second, func() bool {
			return p.js("return !$('confirmation').open&&document.activeElement.id==='reboot'") == true
		})
		equal(t, p.count(), n)
		for _, name := range []string{"mode", "ap", "connect", "scan", "forget", "reboot"} {
			p.js("window.testOperation=operation(arguments[0],{},true)", name)
			p.check("return $('confirmation').open")
			no(t, strings.Contains(p.text("confirmtext"), "_"), "raw confirmation label")
			p.js("$('confirmation').querySelector('[value=cancel]').click();return window.testOperation.then(()=>true)")
			equal(t, p.count(), n)
		}
		p.js("window.testOperation=operation('scan',{},true)")
		p.js("$('confirmation').querySelector('[value=continue]').click();return window.testOperation.then(()=>true)")
		equal(t, p.next(n)["path"], "/api/scan")
	})
	t.Run("ttl_imei", func(t *testing.T) {
		p.t = t
		p.status("none", "disabled", "")
		n := p.count()
		p.js("selectTab('lte');$('ttlenabled').value='true';$('ttlvalue').value='65';$('ttlform').requestSubmit()")
		equal(t, p.next(n), map[string]any{"path": "/api/ttl", "body": map[string]any{"enabled": true, "value": "65"}})
		p.check("return !validIMEI('490154203237519')&&validIMEI('490154203237518')")
		n = p.count()
		p.js("$('imeivalue').value='490154203237519';$('imeiform').requestSubmit()")
		p.check("return !$('confirmation').open")
		equal(t, p.count(), n)
		p.js("$('imeivalue').value='490154203237518';$('imeiform').requestSubmit()")
		p.check("return $('confirmation').open")
		p.js("$('confirmation').querySelector('[value=cancel]').click()")
		equal(t, p.count(), n)
		p.js("$('imeiform').requestSubmit();$('confirmation').querySelector('[value=continue]').click()")
		equal(t, p.next(n), map[string]any{"path": "/api/imei", "body": map[string]any{"imei": "490154203237518"}})
		p.js("$('imeirestore').click()")
		p.check("return $('confirmation').open")
		p.js("$('confirmation').querySelector('[value=cancel]').click()")
	})
	t.Run("lan_drafts_pending_xss", func(t *testing.T) {
		p.t = t
		p.js("selectTab('lan')")
		until(t, 3*time.Second, func() bool { return p.js("return lanLoaded&&firewallLoaded") == true })
		contains(t, p.text("clientrows"), "<img")
		p.check("return $('clientrows').querySelectorAll('img').length===0")
		p.js("$('lan-ap-end').value='192.168.100.50';$('lan-ap-end').dispatchEvent(new Event('input',{bubbles:true}));return refreshLan()")
		equal(t, p.js("return $('lan-ap-end').value"), "192.168.100.50")
		p.js("$('addreservation').click();$('reservationrows').querySelector('[data-field=mac]').value='02:12:34:56:78:90';$('reservationrows').querySelector('[data-field=name]').value='Phone';$('reservationrows').querySelector('[data-field=ip]').value='192.168.100.15'")
		n := p.count()
		p.js("$('lanform').requestSubmit()")
		p.check("return $('confirmation').open")
		p.js("$('confirmation').querySelector('[value=cancel]').click()")
		equal(t, p.count(), n)
		p.js("$('lanform').requestSubmit();$('confirmation').querySelector('[value=continue]').click()")
		q := p.next(n)
		equal(t, q["path"], "/api/lan")
		cfg := q["body"].(map[string]any)["config"].(map[string]any)
		equal(t, cfg["ap"].(map[string]any)["end"], "192.168.100.50")
		equal(t, cfg["reservations"], []any{})
		time.Sleep(400 * time.Millisecond)
		n = p.count()
		p.js("$('reservationsform').requestSubmit()")
		q = p.next(n)
		equal(t, q["body"].(map[string]any)["config"].(map[string]any)["reservations"].([]any)[0].(map[string]any)["name"], "Phone")
		time.Sleep(400 * time.Millisecond)
		p.js("fieldError('lanform',{message:'invalid_lan',field:'ap.range'});pendingView({config:arguments[0],pending:{token:'synthetic',remaining:119},wifi_mode:'sta'})", baseLAN())
		p.check("return $('lan-ap-end').getAttribute('aria-invalid')==='true'&&!$('lanpending').hidden&&$('lanlinks').querySelectorAll('a').length===2")
		contains(t, p.text("lanpendingtext"), "119")
		n = p.count()
		p.js("$('lanconfirm').click()")
		equal(t, p.next(n), map[string]any{"path": "/api/lan_confirm", "body": map[string]any{"token": "synthetic"}})
		time.Sleep(400 * time.Millisecond)
	})
	t.Run("firewall_advanced_roundtrip", func(t *testing.T) {
		p.t = t
		p.js("selectSubTab('lan','firewall');firewallDraft={rules:[newRule()]};renderFirewallForm()")
		p.check("return !$('firewallrows').querySelector('details').open")
		p.js("const d=$('firewallrows').querySelector('[data-field=device]');d.value='ap/02:12:34:56:78:90';d.dispatchEvent(new Event('change',{bubbles:true}));readFirewallForm()")
		p.check("const r=firewallDraft.rules[0];return r.mac==='02:12:34:56:78:90'&&r.network==='ap'&&r.destination===''&&r.protocol==='any'&&r.port===''")
		p.js("const r=$('firewallrows');r.querySelector('details').open=true;r.querySelector('[data-field=destination]').value='1.1.1.0/24';const v=r.querySelector('[data-field=port]');v.value='8000-8010';v.dispatchEvent(new Event('input',{bubbles:true}));readFirewallForm()")
		expected := p.js("return firewallDraft.rules[0]")
		equal(t, expected.(map[string]any)["protocol"], "both")
		p.js("lang='en';language();$('firewallrows').querySelector('details').open=false;readFirewallForm()")
		equal(t, p.js("return firewallDraft.rules[0]"), expected)
		p.js("fieldError('firewallform',{message:'invalid_firewall',field:'rule.port'})")
		p.check("return $('firewallrows').querySelector('details').open&&document.activeElement.dataset.field==='port'")
		p.js("firewallDraft={rules:[]};firewallDirty=false;renderFirewallForm();lang='ru';language()")
	})
	t.Run("chart_history_scale", func(t *testing.T) {
		p.t = t
		p.js("selectSubTab('statistics','lte')")
		for _, size := range [][2]int{{1680, 1050}, {390, 844}, {1280, 950}} {
			p.call(p.session+"/window/rect", map[string]int{"width": size[0], "height": size[1]}, "")
			p.js(`drawSeries(document.querySelector('[data-panel="statistics/lte"] [data-stat-chart]'),{source:'lte',points:[[-900,100000,15000],[-3,700000,50000],[0,1250000,125000]]})`)
			until(t, 2*time.Second, func() bool {
				return p.js(`const m=document.querySelector('[data-panel="statistics/lte"] svg text').getScreenCTM();return Math.abs(m.a-1)<.01&&Math.abs(m.d-1)<.01`) == true
			})
			p.check("return document.documentElement.scrollWidth<=innerWidth")
		}
		p.js("drawHistory({buckets:[{date:20261104,rx:'104857600',tx:'10485760'}]})")
		contains(t, p.text("historytable"), "04.11.2026")
		p.check("return $('historytable').querySelectorAll('tbody tr').length===1&&!$('historychart')&&!$('undated')")
		p.js("drawHistory({buckets:[]})")
		contains(t, p.text("historytable"), "История пока пуста")
	})
	t.Run("statistics_sources_reset", func(t *testing.T) {
		p.t = t
		p.js("selectSubTab('statistics','lte');renderStatistics(arguments[0])", baseStatistics)
		contains(t, p.text("periodrx"), "100")
		contains(t, p.text("overviewperiod"), "110")
		no(t, strings.Contains(p.text("overviewperiod"), "40"), "sources merged")
		p.js("selectSubTab('statistics','wifi')")
		contains(t, p.text("wifisessionrx"), "40")
		p.js("selectSubTab('statistics','lte')")
		n := p.count()
		p.js("$('resetstatistics').click()")
		p.check("return $('confirmation').open")
		contains(t, p.text("confirmtext"), "сохранится")
		p.js("$('confirmation').close('cancel')")
		until(t, 2*time.Second, func() bool { return p.js("return !$('resetstatistics').disabled") == true })
		equal(t, p.count(), n)
		p.js("$('resetstatistics').click();$('confirmation').querySelector('[value=continue]').click()")
		equal(t, p.next(n)["path"], "/api/statistics/reset")
	})
	t.Run("responsive", func(t *testing.T) {
		p.t = t
		p.status("sim-pin", "locked", "")
		for _, size := range [][2]int{{1280, 950}, {390, 844}} {
			p.call(p.session+"/window/rect", map[string]int{"width": size[0], "height": size[1]}, "")
			for _, tab := range []string{"overview", "wifi", "lte", "lan", "settings", "statistics"} {
				p.js("selectTab(arguments[0])", tab)
				p.check("return document.documentElement.scrollWidth<=innerWidth")
			}
			p.js("window.testOperation=operation('reboot',{},true)")
			p.check("const r=$('confirmation').getBoundingClientRect();return r.left>=0&&r.right<=innerWidth")
			p.js("$('confirmation').close('cancel')")
		}
	})
	t.Run("radio_discovery_ssid", func(t *testing.T) {
		p.t = t
		s := object(t, js(baseStatus))
		g := s["modem"].(map[string]any)["modem"].(map[string]any)["generic"].(map[string]any)
		g["state"] = "unknown"
		g["unlock-required"] = "unknown"
		g["signal-quality"] = map[string]any{"value": 55, "recent": "yes"}
		s["device"] = map[string]any{"hostname": "microstick", "mdns_name": "microstick.local", "mdns_ready": true}
		s["networks"] = []any{map[string]any{"ssid": "Русская сеть", "ssid_hex": fmt.Sprintf("%x", "Русская сеть"), "security": "open", "signal": -42}}
		for _, v := range [][2]string{{"gprs", "G"}, {"edge", "E"}, {"umts", "3G"}, {"hspa", "H"}, {"hspa+", "H+"}, {"lte", "4G"}} {
			g["access-technologies"] = []string{v[0]}
			p.js("render(arguments[0])", s)
			contains(t, p.text("radio"), v[1])
		}
		g["access-technologies"] = []string{"unknown"}
		p.js("render(arguments[0])", s)
		no(t, strings.Contains(p.text("radio"), "UNKNOWN") || strings.Contains(p.text("radio"), "LTE"), "unknown radio mislabeled")
		contains(t, p.text("simstate"), "регистрация")
		contains(t, p.text("addresses"), "microstick.local")
		p.js("$('networks').querySelector('button').click()")
		equal(t, p.js("return $('ssid').value"), "Русская сеть")
		p.check("return !!$('ssid').dataset.hex")
		p.js("$('ssid').value='manual';$('ssid').dispatchEvent(new Event('input'))")
		p.check("return !$('ssid').dataset.hex")
		p.status("none", "disabled", "")
	})
	t.Run("ports_hostname_wifi_persistence", func(t *testing.T) {
		p.t = t
		p.js("selectSubTab('lan','ports')")
		until(t, 2*time.Second, func() bool { return p.js("return servicesLoaded") == true })
		p.js("$('addservice').click();$('servicerows').querySelector('[data-field=port]').value='8080';readServices()")
		equal(t, p.js("return servicesDraft[0].port"), "8080")
		n := p.count()
		p.js("$('servicesform').requestSubmit()")
		q := p.next(n)
		equal(t, q["path"], "/api/firewall")
		cfg := q["body"].(map[string]any)["config"].(map[string]any)
		equal(t, cfg["services"].([]any)[0].(map[string]any)["port"], "8080")
		no(t, cfg["rules"] == nil, "firewall rules dropped")
		n = p.count()
		p.js("selectSubTab('settings','device');$('hostname').value='travel-stick';$('hostnameform').requestSubmit()")
		equal(t, p.next(n), map[string]any{"path": "/api/device", "body": map[string]any{"hostname": "travel-stick"}})
		n = p.count()
		p.js("selectSubTab('statistics','wifi');$('wifipersistence').checked=true;$('wifipersistence').dispatchEvent(new Event('change'))")
		equal(t, p.next(n), map[string]any{"path": "/api/statistics/config", "body": map[string]any{"wifi_persistence": true}})
		p.js("servicesDirty=false;selectTab('overview')")
	})
	// Cleanup callbacks use the parent T after subtests finish.
	p.t = t
}
