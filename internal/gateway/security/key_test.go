package security

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestDecodeBase64KeyAcceptsPaddedAndUnpaddedStandardEncoding(t *testing.T) {
	want := []byte("0123456789abcdef0123456789abcdef")
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(want),
		base64.RawStdEncoding.EncodeToString(want),
		"  " + base64.RawStdEncoding.EncodeToString(want) + "\r\n",
	} {
		got, err := DecodeBase64Key(encoded)
		if err != nil {
			t.Fatalf("DecodeBase64Key(%q): %v", encoded, err)
		}
		if string(got) != string(want) {
			t.Fatalf("DecodeBase64Key(%q) = %x, want %x", encoded, got, want)
		}
		clear(got)
	}
}

func TestDecodeBase64KeyRejectsInvalidMaterial(t *testing.T) {
	for _, encoded := range []string{"", "not base64", base64.StdEncoding.EncodeToString(make([]byte, KeySize-1))} {
		if _, err := DecodeBase64Key(encoded); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("DecodeBase64Key(%q) error = %v, want ErrInvalidKey", encoded, err)
		}
	}
}
