package firmware

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPanelAPNProfilesAndBrand(t *testing.T) {
	p := newPanel(t)
	profiles := []map[string]any{}
	seen := map[string]bool{}
	for _, v := range reviewedProfiles(t) {
		id := v["id"].(string)
		if !seen[id] {
			profiles = append(profiles, map[string]any{"id": id, "name": v["name"], "apn": v["apn"]})
			seen[id] = true
		}
	}
	config := map[string]any{"lte": map[string]any{"profile": "auto", "effective_apn": "internet.yota", "provider": "Yota", "state": "ready", "profiles": profiles}}
	p.mu.Lock()
	p.config = config
	p.statusData = object(t, js(baseStatus))
	p.statusData["lte"] = map[string]any{"profile": "auto", "effective_apn": "internet.yota", "provider": "Yota", "state": "ready"}
	p.statusData["uplink"] = "wwan0"
	modem := p.statusData["modem"].(map[string]any)["modem"].(map[string]any)
	modem["generic"].(map[string]any)["state"] = "connected"
	modem["3gpp"] = map[string]any{"operator-name": "Yota", "operator-code": "25011", "registration-state": "home"}
	p.mu.Unlock()
	p.js("configureLteProfile(arguments[0]);selectSubTab('lte','connection');", config)
	p.check("return $('lteprofile').options.length===12&&$('apn').value==='internet.yota'&&$('apn').readOnly&&$('lteauthfields').hidden")
	p.check("return getComputedStyle(document.querySelector('.primary')).backgroundColor==='rgb(50, 83, 220)'")
	p.js("carrierView($('operator'),'МегаФон','25002')")
	p.check("return getComputedStyle($('operator')).color==='rgb(0, 131, 71)'")
	p.js("$('lteprofile').value='ru-gpbmobile';$('lteprofile').dispatchEvent(new Event('change'))")
	p.check("return $('apn').value==='gpb'&&$('apn').readOnly")
	n := p.count()
	p.js("$('lteform').requestSubmit()")
	q := p.next(n)
	equal(t, q["path"], "/api/lte")
	body := q["body"].(map[string]any)
	equal(t, body["profile"], "ru-gpbmobile")
	if _, present := body["apn"]; present {
		t.Fatal("automatic profile frozen into manual APN")
	}
	p.js("configureLteProfile(arguments[0]);$('lteprofile').value='manual';$('lteprofile').dispatchEvent(new Event('change'));$('apn').value='private.example';$('apn').dispatchEvent(new Event('input'));", config)
	p.js("renderLteProfile({profile:'auto',effective_apn:'internet',provider:'T2',state:'ready'});configureLteProfile(arguments[0]);", config)
	p.check("return $('lteprofile').value==='manual'&&$('apn').value==='private.example'&&!$('apn').readOnly&&!$('lteauthfields').hidden")
	n = p.count()
	p.js("$('lteform').requestSubmit()")
	q = p.next(n)
	body = q["body"].(map[string]any)
	equal(t, body["profile"], "manual")
	equal(t, body["apn"], "private.example")
	for _, key := range []string{"username", "password", "auth"} {
		if _, present := body[key]; present {
			t.Fatal("untouched authorization overwritten", key)
		}
	}
	p.js("configureLteProfile(arguments[0]);lang='en';language();", config)
	p.check("return $('lteprofile').options[0].textContent==='Automatic'&&$('lteprofile').options[11].textContent==='Manual'")
	p.js("lang='ru';language();")
	for _, size := range [][2]int{{1280, 900}, {390, 844}} {
		p.call(p.session+"/window/rect", map[string]int{"width": size[0], "height": size[1]}, "")
		until(t, time.Second, func() bool {
			return p.js("return innerWidth>860||document.getElementById('sidebar').getBoundingClientRect().right<=1") == true
		})
		p.check("return document.documentElement.scrollWidth<=innerWidth")
		if dir := os.Getenv("MICROSTICK_PANEL_EVIDENCE"); dir != "" {
			if !filepath.IsAbs(dir) {
				t.Fatal("evidence path must be absolute")
			}
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			encoded := p.call(p.session+"/screenshot", nil, "").(string)
			raw, e := base64.StdEncoding.DecodeString(encoded)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, fmt.Sprintf("panel-apn-%dx%d.png", size[0], size[1])), raw, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
}

func TestWebServesAPNAsset(t *testing.T) {
	w := newWeb(t)
	req, e := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/lte.js", w.port), nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Host = "microstick.local"
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	equal(t, response.StatusCode, 200)
	equal(t, response.Header.Get("Content-Type"), "application/javascript")
	if !strings.Contains(string(body), "configureLteProfile") {
		t.Fatal("LTE asset not served by production web service")
	}
}
