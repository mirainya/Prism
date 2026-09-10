package security

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// VersionedHMACEnvName returns the environment variable used for one HMAC
// key version. Version 1 keeps the original, un-suffixed name for backwards
// compatibility; every later version is explicitly suffixed with _V<n>.
func VersionedHMACEnvName(base string, version uint32) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" || version == 0 {
		return "", ErrInvalidKey
	}
	if version == 1 {
		return base, nil
	}
	return base + "_V" + strconv.FormatUint(uint64(version), 10), nil
}

// LoadVersionedHMACKey loads exactly one 256-bit HMAC key from the process
// environment. The caller owns the returned bytes and must clear them after
// use. Errors intentionally contain only the variable name, never key data.
func LoadVersionedHMACKey(base string, version uint32) ([]byte, error) {
	name, err := VersionedHMACEnvName(base, version)
	if err != nil {
		return nil, err
	}
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("hmac key version %d is not configured (%s): %w", version, name, ErrInvalidKey)
	}
	key, err := DecodeBase64Key(raw)
	if err != nil {
		return nil, fmt.Errorf("hmac key version %d is invalid (%s): %w", version, name, ErrInvalidKey)
	}
	return key, nil
}

// LoadVersionedHMACKeys loads a set of HMAC versions in deterministic order.
// Duplicate version numbers are harmless. If one load fails, all previously
// loaded key material is cleared before returning.
func LoadVersionedHMACKeys(base string, versions []uint32) (map[uint32][]byte, error) {
	if strings.TrimSpace(base) == "" || len(versions) == 0 {
		return nil, ErrInvalidKey
	}
	ordered := append([]uint32(nil), versions...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	keys := make(map[uint32][]byte, len(ordered))
	for _, version := range ordered {
		if version == 0 {
			clearHMACKeys(keys)
			return nil, ErrInvalidKey
		}
		if _, exists := keys[version]; exists {
			continue
		}
		key, err := LoadVersionedHMACKey(base, version)
		if err != nil {
			clearHMACKeys(keys)
			return nil, err
		}
		keys[version] = key
	}
	return keys, nil
}

// ClearHMACKeys releases key material loaded by LoadVersionedHMACKeys.
// Keeping this operation in the security package makes cleanup consistent at
// every caller and avoids accidentally retaining old rotation keys.
func ClearHMACKeys(keys map[uint32][]byte) {
	clearHMACKeys(keys)
}

func clearHMACKeys(keys map[uint32][]byte) {
	for version, key := range keys {
		clear(key)
		delete(keys, version)
	}
}
