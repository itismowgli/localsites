package main

import (
	"encoding/base64"
	"testing"
)

func TestReleaseFile(t *testing.T) {
	cases := []struct{ name, ver, os, arch, format string }{
		{"LocalSites-1.5.0-macos-arm64.zip", "1.5.0", "darwin", "arm64", ""},
		{"localsites_1.5.0_amd64.deb", "1.5.0", "linux", "amd64", "deb"},
		{"localsites_1.5.0_arm64.deb", "1.5.0", "linux", "arm64", "deb"},
		{"localsites-1.5.0-1.x86_64.rpm", "1.5.0", "linux", "amd64", "rpm"},
		{"localsites-1.5.0-1.aarch64.rpm", "1.5.0", "linux", "arm64", "rpm"},
	}
	for _, c := range cases {
		v, o, a, f, ok := releaseFile(c.name)
		if !ok || v != c.ver || o != c.os || a != c.arch || f != c.format {
			t.Errorf("%s: %q %q %q %q %v", c.name, v, o, a, f, ok)
		}
	}
	for _, bad := range []string{"localsites_1.5.0_i386.deb", "notes.md", "update.json", "localsites-1.5.0.x86_64.rpm"} {
		if _, _, _, _, ok := releaseFile(bad); ok {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSplitKeyBundle(t *testing.T) {
	bundle := "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nAAA\n-----END PGP PRIVATE KEY BLOCK-----\n" +
		"-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nBBB\n-----END PGP PUBLIC KEY BLOCK-----\nBINARY\n" + base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) + "\n"
	priv, pub, bin, ok := splitKeyBundle([]byte(bundle))
	if !ok || string(priv) != "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nAAA\n-----END PGP PRIVATE KEY BLOCK-----\n" || len(pub) == 0 || len(bin) != 3 {
		t.Fatalf("priv %q pub %q bin %v ok %v", priv, pub, bin, ok)
	}
	if _, _, _, ok := splitKeyBundle([]byte("nothing")); ok {
		t.Fatal("garbage accepted")
	}
}
