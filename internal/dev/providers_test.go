package dev

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderCatalog(t *testing.T) {
	raw := []byte(`<serviceproviders><country code="ru"><provider><name>Example</name><gsm><network-id mcc="250" mnc="11"/><apn value="internet.yota"><usage type="internet"/></apn><apn value="yota.ru"><usage type="internet"/></apn><apn value="mms"><usage type="mms"/></apn><apn value="ims"><usage type="ims"/></apn></gsm></provider></country><country code="gb"><provider><name>Other</name><gsm><network-id mcc="234" mnc="15"/><apn value="internet"/></gsm></provider></country></serviceproviders>`)
	reviewed, err := os.ReadFile(filepath.Join("..", "..", "buildroot-external", "board", "msm8916", "operator-profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := providerCatalog(raw, reviewed)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	international := false
	legacy := false
	for _, p := range catalog {
		if p.APN == "mms" || p.APN == "ims" {
			t.Fatal("non-internet profile imported")
		}
		if p.Operator == "23415" && p.APN == "internet" {
			international = true
		}
		if p.APN == "yota.ru" {
			legacy = true
		}
		if p.Preferred {
			ids[p.ID] = true
		}
	}
	if len(ids) != 10 || !international || !legacy {
		t.Fatalf("catalog coverage: %d / %v / %v", len(ids), international, legacy)
	}
	again, err := providerCatalog(raw, reviewed)
	if err != nil || again[0].ID != catalog[0].ID {
		t.Fatal("unstable upstream IDs")
	}
	for _, bad := range [][]byte{[]byte(`bad`), []byte(`[{}]`), []byte(`[{"id":"x","name":"X","apn":"x","operators":["25x01"]}]`)} {
		if _, err := providerCatalog(raw, bad); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
	if _, err := providerCatalog([]byte(`<serviceproviders/>`), reviewed); err == nil {
		t.Fatal("missing worldwide profiles accepted")
	}
}
