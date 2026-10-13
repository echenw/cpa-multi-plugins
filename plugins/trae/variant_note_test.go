package main

import "testing"

// B-plan: display labels use the natural wording ("trae cn" / "trae");
// stored solo accounts keep the verbatim "SOLO" note / "Trae SOLO CN"
// label (safe-reader: they stay distinguishable instead of being
// relabeled into a region they were not created in).

func TestAuthNote(t *testing.T) {
	cases := []struct {
		variant string
		want    string
	}{
		{variantCN, "trae cn"},
		{variantIntl, "trae"},
		{retiredVariantSolo, "SOLO"},
		{"", "trae cn"},          // legacy/unknown falls to trae-cn (the default region)
		{"cn", "trae cn"},        // legacy pre-B-plan spelling
		{"intl", "trae"},         // legacy pre-B-plan spelling
		{" SOLO ", "SOLO"},       // tolerant of stray whitespace/case
		{"solo-intl", "SOLO"},    // retired spelling stays verbatim
		{"trae-solo-cn", "SOLO"}, // retired spelling stays verbatim
		{"garbage", "trae cn"},
	}
	for _, tc := range cases {
		if got := authNote(tc.variant); got != tc.want {
			t.Errorf("authNote(%q) = %q, want %q", tc.variant, got, tc.want)
		}
	}
}

func TestVariantLabel(t *testing.T) {
	cases := []struct {
		variant string
		want    string
	}{
		{variantCN, "trae cn"},
		{variantIntl, "trae"},
		{retiredVariantSolo, "Trae SOLO CN"},
		{"cn", "trae cn"},
		{"intl", "trae"},
		{"", "trae cn"},
	}
	for _, tc := range cases {
		if got := variantLabel(tc.variant); got != tc.want {
			t.Errorf("variantLabel(%q) = %q, want %q", tc.variant, got, tc.want)
		}
	}
}
