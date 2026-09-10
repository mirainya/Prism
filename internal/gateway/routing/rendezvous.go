package routing

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"math/bits"
)

const weightedRendezvousVersion uint32 = 1

// weightedRendezvousScore defines the portable v1 candidate order. The
// SHA-256 numerator and integer weight divisor avoid process randomness and
// floating-point differences across platforms; lower scores rank first.
func weightedRendezvousScore(selectionKey string, routeID, offeringID, credentialID, routeWeight, credentialWeight uint64) (*big.Int, error) {
	if selectionKey == "" || len(selectionKey) > 256 || routeID == 0 || offeringID == 0 || credentialID == 0 {
		return nil, ErrInvalidSelectionKey
	}
	if routeWeight == 0 || credentialWeight == 0 {
		return nil, ErrInvalidRouteWeight
	}
	hi, weight := bits.Mul64(routeWeight, credentialWeight)
	if hi != 0 || weight == 0 {
		return nil, ErrInvalidRouteWeight
	}
	key := []byte(selectionKey)
	encoded := make([]byte, 4+4+len(key)+8*3)
	binary.BigEndian.PutUint32(encoded[0:4], weightedRendezvousVersion)
	binary.BigEndian.PutUint32(encoded[4:8], uint32(len(key)))
	copy(encoded[8:], key)
	offset := 8 + len(key)
	binary.BigEndian.PutUint64(encoded[offset:offset+8], routeID)
	binary.BigEndian.PutUint64(encoded[offset+8:offset+16], offeringID)
	binary.BigEndian.PutUint64(encoded[offset+16:offset+24], credentialID)
	digest := sha256.Sum256(encoded)
	return new(big.Int).Quo(new(big.Int).SetBytes(digest[:]), new(big.Int).SetUint64(weight)), nil
}
