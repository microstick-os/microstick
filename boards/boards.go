// Package boards contains the offline bootstrap data for the board registry.
// Network updates use authenticated snapshots and do not modify these files.
package boards

import "embed"

//go:embed */board.json bundles/*.json resources.json transforms.json keys.json
var Files embed.FS
