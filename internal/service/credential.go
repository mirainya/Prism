package service

// MaskCredential keeps only the final four bytes of an upstream credential.
func MaskCredential(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func isCurrentCredentialMask(candidate, current string) bool {
	return candidate != "" && candidate == MaskCredential(current)
}
