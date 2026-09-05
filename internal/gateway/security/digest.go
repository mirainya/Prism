package security

import (
	"encoding/binary"
)

// DomainDigest prevents the same secret from producing interchangeable
// aliases for different namespaces. Every part is length-prefixed.
func DomainDigest(key []byte, domain string, parts ...[]byte) [32]byte {
	buffer := make([]byte, 0, len(domain)+16)
	buffer = append(buffer, "prism-digest-v1"...)
	buffer = appendLengthPrefixed(buffer, []byte(domain))
	for _, part := range parts {
		buffer = appendLengthPrefixed(buffer, part)
	}
	return HMACSHA256(key, buffer)
}

func appendLengthPrefixed(dst, value []byte) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}
