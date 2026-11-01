package main

import "testing"

func TestVersionString(t *testing.T) {
	old := version
	defer func() { version = old }()

	for in, want := range map[string]string{"1.2.3": "1.2.3", "v1.2.3": "1.2.3", "0.2.0-rc1": "0.2.0-rc1"} {
		version = in
		if got := versionString(); got != want {
			t.Errorf("version %q: versionString() = %q, want %q", in, got, want)
		}
	}

	// Without a linker version, the result comes from the build info:
	// "devel" in a test binary, never empty.
	version = ""
	if got := versionString(); got == "" {
		t.Error("versionString() is empty without a linker version")
	}
}
