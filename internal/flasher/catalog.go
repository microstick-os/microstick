package flasher

import (
	"embed"
	"encoding/json"
)

//go:embed catalog.json
var catalogFS embed.FS

func Catalog() ([]HardwareProfile, error) {
	b, e := catalogFS.ReadFile("catalog.json")
	if e != nil {
		return nil, e
	}
	var ps []HardwareProfile
	e = json.Unmarshal(b, &ps)
	return ps, e
}
