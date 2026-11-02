// Package evaldata embeds the machine copy of the trust report so the server
// can offer first-run handovers without reading the file system.
package evaldata

import _ "embed"

// TrustTiers is the content of trust_tiers.json.
//
//go:embed trust_tiers.json
var TrustTiers []byte
