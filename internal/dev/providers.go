package dev

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
)

type providerProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Operator  string   `json:"operator"`
	Operators []string `json:"operators,omitempty"`
	APN       string   `json:"apn"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Auth      string   `json:"auth"`
	SPN       []string `json:"spn,omitempty"`
	GID1      string   `json:"gid1,omitempty"`
	Preferred bool     `json:"preferred,omitempty"`
}

// Keep the worldwide upstream database, adding reviewed preferred profiles and
// SIM selectors. Credential tuples alone are not a carrier identity.
func providerCatalog(raw, overrides []byte) ([]providerProfile, error) {
	var doc struct {
		Providers []struct {
			Name string `xml:"name"`
			GSM  []struct {
				Networks []struct {
					MCC string `xml:"mcc,attr"`
					MNC string `xml:"mnc,attr"`
				} `xml:"network-id"`
				APNs []struct {
					Value    string `xml:"value,attr"`
					Username string `xml:"username"`
					Password string `xml:"password"`
					Usage    struct {
						Type string `xml:"type,attr"`
					} `xml:"usage"`
				} `xml:"apn"`
			} `xml:"gsm"`
		} `xml:"country>provider"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	profiles := []providerProfile{}
	for _, p := range doc.Providers {
		for _, gsm := range p.GSM {
			for _, apn := range gsm.APNs {
				if apn.Value == "" || apn.Usage.Type != "" && apn.Usage.Type != "internet" {
					continue
				}
				for _, network := range gsm.Networks {
					operator := network.MCC + network.MNC
					sum := sha256.Sum256([]byte(operator + "\x00" + apn.Value + "\x00" + apn.Username + "\x00" + apn.Password))
					auth := "none"
					if apn.Username != "" || apn.Password != "" {
						auth = "pap-or-chap"
					}
					profiles = append(profiles, providerProfile{ID: fmt.Sprintf("upstream-%x", sum[:8]), Name: p.Name, Operator: operator, APN: apn.Value, Username: apn.Username, Password: apn.Password, Auth: auth})
				}
			}
		}
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("provider database contains no internet profiles")
	}
	var reviewed []providerProfile
	if err := json.Unmarshal(overrides, &reviewed); err != nil {
		return nil, err
	}
	for _, p := range reviewed {
		if p.ID == "" || p.Name == "" || p.APN == "" || len(p.Operators) == 0 {
			return nil, fmt.Errorf("incomplete reviewed operator profile")
		}
		for _, operator := range p.Operators {
			if len(operator) != 5 && len(operator) != 6 {
				return nil, fmt.Errorf("invalid operator code in %s", p.ID)
			}
			for _, c := range operator {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("invalid operator code in %s", p.ID)
				}
			}
			entry := p
			entry.Operator = operator
			entry.Operators = nil
			profiles = append(profiles, entry)
		}
	}
	return profiles, nil
}
