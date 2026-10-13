package main

// variant_file.go — B-plan credential file namespaces.
//
// Canonical names (via credentialFileName, the only writer):
//   - trae-cn region → `trae-cn-<uid>.json` (suffixed, B-plan),
//   - trae region     → `trae-<uid>.json` (bare = international, B-plan).
//
// The retired solo namespace (`trae-solo-cn-<uid>.json`) is never written
// anymore. Files already on disk under that name — and every other legacy
// name (`trae-<uid>.json` CN files, `trae-intl-<uid>.json`, single-file
// `trae.json` / `trae-cn.json` / `trae-intl.json`) — keep reading: listing
// filters by the `trae-` prefix, claiming by the family-prefix lists, and
// routing by file content (sniffVariantFromJSON + normalizeVariant). There
// is deliberately no migration/rename pass: old names are adopted in place
// by adopt.go (type/provider rewrite only, variant untouched).

import (
	"fmt"
	"strings"
)

// credentialFileName maps a login region + account uid to the credential
// file name. Regions normalize first, so legacy spellings ("cn", "intl")
// and the retired solo spellings (→ trae-cn fallback) can never mint an
// off-namespace name. Split-channel builds (providerName != "trae") keep
// their injected `<provider>-<uid>.json` shape.
func credentialFileName(variant, uid string) string {
	uid = strings.TrimSpace(uid)
	// Split-channel build (issue #29): the registered id ALREADY encodes the
	// region namespace — no infix, or the file would double it.
	if providerName != "trae" {
		return fmt.Sprintf("%s-%s.json", providerName, uid)
	}
	switch normalizeVariant(variant) {
	case variantIntl:
		return fmt.Sprintf("%s-%s.json", providerName, uid)
	default:
		// variantCN already carries the "trae-" prefix ("trae-cn"), so
		// the canonical CN name is "trae-cn-<uid>.json" — not doubled.
		return fmt.Sprintf("%s-%s.json", variantCN, uid)
	}
}
