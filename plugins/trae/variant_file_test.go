package main

// variant_file_test.go — B-plan credential file namespaces.
//
// Canonical names: trae-cn → `trae-cn-<uid>.json` (suffixed),
// trae → `trae-<uid>.json` (bare = international). Legacy spellings and
// the retired solo spellings normalize onto the same names (never minting
// an off-namespace file). Old on-disk names keep reading — there is no
// migration pass anymore.

import "testing"

func TestCredentialFileName_RegionNamespaces(t *testing.T) {
	cases := []struct{ variant, uid, want string }{
		{"trae-cn", "u1", "trae-cn-u1.json"},
		{"cn", "u1", "trae-cn-u1.json"},        // legacy spelling, same file
		{"", "u1", "trae-cn-u1.json"},          // unknown variant -> trae-cn namespace
		{"trae", "u1", "trae-u1.json"},         // bare = international
		{"intl", "u1", "trae-u1.json"},         // legacy spelling, same file
		{"solo", "u1", "trae-cn-u1.json"},      // retired solo -> trae-cn fallback
		{"SOLO", "u1", "trae-cn-u1.json"},      // normalizeVariant is case-insensitive
		{"solo-intl", "u1", "trae-cn-u1.json"}, // retired spelling -> trae-cn fallback (pre-B-plan default was cn)
		{"trae-solo-cn", "u1", "trae-cn-u1.json"},
	}
	for _, tc := range cases {
		if got := credentialFileName(tc.variant, tc.uid); got != tc.want {
			t.Fatalf("credentialFileName(%q,%q) = %q, want %q", tc.variant, tc.uid, got, tc.want)
		}
	}
	// Same uid, different regions -> different files (no cross-region overwrite).
	cn := credentialFileName("trae-cn", "same-uid")
	intl := credentialFileName("trae", "same-uid")
	if cn == intl {
		t.Fatalf("trae-cn and trae of one account must not share a file name: %q", cn)
	}
}

func TestLegacyTraeVariant_RetiredSoloClaimable(t *testing.T) {
	// The retired solo namespace must still be recognized (adopt rewrites
	// its type/provider; the variant stays untouched for the SOLO badge).
	if got := legacyTraeVariant("trae-solo-cn-u2.json"); got != retiredVariantSolo {
		t.Fatalf("legacyTraeVariant solo file = %q, want %q", got, retiredVariantSolo)
	}
	if got := legacyTraeVariant("trae-cn-u2.json"); got != variantCN {
		t.Fatalf("legacyTraeVariant cn file = %q, want %q", got, variantCN)
	}
	if got := legacyTraeVariant("trae-intl-u2.json"); got != variantIntl {
		t.Fatalf("legacyTraeVariant intl file = %q, want %q", got, variantIntl)
	}
	if got := legacyTraeVariant("trae-u2.json"); got != "" {
		t.Fatalf("legacyTraeVariant canonical file = %q, want empty", got)
	}
}
