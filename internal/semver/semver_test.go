package semver

import "testing"

func TestNext(t *testing.T) {
	cases := []struct {
		name    string
		current string
		bump    Bump
		want    string
	}{
		{"major bump resets minor and patch", "v1.4.2", Major, "v2.0.0"},
		{"minor bump resets patch", "v1.4.2", Minor, "v1.5.0"},
		{"patch bump", "v1.4.2", Patch, "v1.4.3"},
		{"empty current treated as v0.0.0, major", "", Major, "v1.0.0"},
		{"empty current treated as v0.0.0, minor", "", Minor, "v0.1.0"},
		{"empty current treated as v0.0.0, patch", "", Patch, "v0.0.1"},
		{"large components", "v10.20.30", Patch, "v10.20.31"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Next(tc.current, tc.bump)
			if err != nil {
				t.Fatalf("Next(%q, %q) returned error: %v", tc.current, tc.bump, err)
			}
			if got != tc.want {
				t.Errorf("Next(%q, %q) = %q, want %q", tc.current, tc.bump, got, tc.want)
			}
		})
	}
}

func TestNextRejectsMalformedCurrent(t *testing.T) {
	cases := []string{
		"1.2.3",    // missing v prefix
		"v1.2",     // missing patch
		"v1.2.3-1", // extra component
		"v1.2.x",   // non-numeric component
		"vv1.2.3",  // malformed prefix
		"v01.2.3",  // leading zero must still parse as decimal, not reject outright
	}

	// v01.2.3 is a special case: leading zeros are valid decimal digits to
	// strconv.ParseUint, so this one must succeed, not error. Verify it
	// separately from the genuinely malformed tags below.
	got, err := Next("v01.2.3", Patch)
	if err != nil {
		t.Fatalf("Next(%q, %q) returned error: %v", "v01.2.3", Patch, err)
	}
	if got != "v1.2.4" {
		t.Errorf("Next(%q, %q) = %q, want %q", "v01.2.3", Patch, got, "v1.2.4")
	}

	for _, current := range cases {
		if current == "v01.2.3" {
			continue
		}
		if _, err := Next(current, Patch); err == nil {
			t.Errorf("Next(%q, %q) succeeded, want error", current, Patch)
		}
	}
}

func TestNextRejectsBadBump(t *testing.T) {
	if _, err := Next("v1.2.3", Bump("bogus")); err == nil {
		t.Errorf("Next with bump %q succeeded, want error", "bogus")
	}
}
