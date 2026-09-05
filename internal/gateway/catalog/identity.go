// Package catalog contains pure rules for the unified gateway catalog.
//
// It deliberately does not depend on GORM or HTTP handlers. Database writes
// must call these functions before entering a transaction so that the same
// canonical bytes are used by importers, management APIs, and runtime lookup.
package catalog

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidAPIName    = errors.New("invalid API name")
	ErrInvalidRoute      = errors.New("invalid route template")
	ErrInvalidHTTPMethod = errors.New("invalid HTTP method")
)

// NormalizeAPIName applies the catalog's ASCII-only model/operation name rule.
func NormalizeAPIName(input string) (string, error) {
	value := trimASCIIWhitespace(input)
	if value == "" || len(value) > 128 {
		return "", fmt.Errorf("%w: length", ErrInvalidAPIName)
	}

	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if !isAPINameByte(c) {
			return "", fmt.Errorf("%w: unsupported byte at %d", ErrInvalidAPIName, i)
		}
		if i == 0 && !isLowerAlphaNumeric(c) {
			return "", fmt.Errorf("%w: first byte", ErrInvalidAPIName)
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// NormalizeHTTPMethod validates and uppercases an ASCII HTTP method.
func NormalizeHTTPMethod(input string) (string, error) {
	value := trimASCIIWhitespace(input)
	if value == "" || len(value) > 16 {
		return "", ErrInvalidHTTPMethod
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if !(c >= 'A' && c <= 'Z' || c == '-') {
			return "", ErrInvalidHTTPMethod
		}
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// NormalizeRouteTemplate canonicalizes a route without decoding or folding
// Unicode. Empty path segments are collapsed; parameter segments must be
// exactly {name} and may not contain a slash, query, or fragment.
func NormalizeRouteTemplate(input string) (string, error) {
	value := trimASCIIWhitespace(input)
	if value == "" || value[0] != '/' || strings.ContainsAny(value, "?#") {
		return "", ErrInvalidRoute
	}
	parts := strings.Split(value, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "{") || strings.HasSuffix(part, "}") {
			if len(part) < 3 || part[0] != '{' || part[len(part)-1] != '}' || !isParameterName(part[1:len(part)-1]) {
				return "", ErrInvalidRoute
			}
			segments = append(segments, part)
			continue
		}
		var b strings.Builder
		b.Grow(len(part))
		for i := 0; i < len(part); i++ {
			c := part[i]
			if c >= 0x80 || c == '{' || c == '}' {
				return "", ErrInvalidRoute
			}
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			b.WriteByte(c)
		}
		segments = append(segments, b.String())
	}
	if len(segments) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(segments, "/"), nil
}

func trimASCIIWhitespace(value string) string {
	start, end := 0, len(value)
	for start < end && isASCIIWhitespace(value[start]) {
		start++
	}
	for end > start && isASCIIWhitespace(value[end-1]) {
		end--
	}
	return value[start:end]
}

func isASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isLowerAlphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

func isAPINameByte(c byte) bool {
	return isLowerAlphaNumeric(c) || c == '.' || c == '_' || c == ':' || c == '/' || c == '-'
}

func isParameterName(value string) bool {
	if value == "" || len(value) > 64 || !(value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
