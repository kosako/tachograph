package main

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"":                                     fallbackVersion,
		"(devel)":                              fallbackVersion,
		"v0.1.0":                               "v0.1.0",
		"v1.2.3-rc.1":                          "v1.2.3-rc.1",
		"v0.1.1-0.20260615120000-abcdef123456": "v0.1.1-0.20260615120000-abcdef123456", // pseudo-version
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
