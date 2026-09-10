package security

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestVersionedHMACEnvNameKeepsVersionOneCompatibility(t *testing.T) {
	for _, test := range []struct {
		version uint32
		want    string
	}{
		{version: 1, want: "PRISM_GATEWAY_PAYLOAD_HMAC_B64"},
		{version: 2, want: "PRISM_GATEWAY_PAYLOAD_HMAC_B64_V2"},
		{version: 17, want: "PRISM_GATEWAY_PAYLOAD_HMAC_B64_V17"},
	} {
		got, err := VersionedHMACEnvName("PRISM_GATEWAY_PAYLOAD_HMAC_B64", test.version)
		if err != nil || got != test.want {
			t.Fatalf("version %d: name=%q err=%v, want %q", test.version, got, err, test.want)
		}
	}
	if _, err := VersionedHMACEnvName("", 1); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty base error=%v, want ErrInvalidKey", err)
	}
	if _, err := VersionedHMACEnvName("PRISM_GATEWAY_HMAC_B64", 0); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("zero version error=%v, want ErrInvalidKey", err)
	}
}

func TestLoadVersionedHMACKeyUsesSuffixedVersions(t *testing.T) {
	v1 := bytes.Repeat([]byte{0x11}, KeySize)
	v2 := bytes.Repeat([]byte{0x22}, KeySize)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", base64.StdEncoding.EncodeToString(v1))
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64_V2", base64.RawStdEncoding.EncodeToString(v2))

	got1, err := LoadVersionedHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64", 1)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := LoadVersionedHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got1, v1) || !bytes.Equal(got2, v2) {
		t.Fatalf("loaded keys do not match configured versions")
	}
	clear(got1)
	clear(got2)

	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64_V3", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, KeySize-1)))
	if _, err := LoadVersionedHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64", 3); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid version 3 key error=%v, want ErrInvalidKey", err)
	}
}

func TestLoadVersionedHMACKeysClearsOnFailure(t *testing.T) {
	v1 := bytes.Repeat([]byte{0x11}, KeySize)
	t.Setenv("PRISM_GATEWAY_HMAC_B64", base64.StdEncoding.EncodeToString(v1))
	// Version 2 is intentionally absent. The helper must not return a partial
	// key set that a caller could accidentally use.
	keys, err := LoadVersionedHMACKeys("PRISM_GATEWAY_HMAC_B64", []uint32{2, 1, 1})
	if err == nil {
		t.Fatal("missing version was accepted")
	}
	if keys != nil {
		t.Fatalf("partial key set returned: %#v", keys)
	}

	keys, err = LoadVersionedHMACKeys("PRISM_GATEWAY_HMAC_B64", []uint32{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !bytes.Equal(keys[1], v1) {
		t.Fatalf("deduplicated key set=%#v", keys)
	}
	ClearHMACKeys(keys)
	if len(keys) != 0 {
		t.Fatalf("keys remained after clear: %#v", keys)
	}
}
