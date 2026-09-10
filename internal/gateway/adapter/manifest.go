package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// BuildRevision is injected by release and container builds. Keeping the
// default explicit makes development binaries identifiable without allowing
// them to impersonate a published build.
var BuildRevision = "dev"

// Descriptor is the server-owned adapter contract that may be referenced by
// a catalog release. Administrative input selects a descriptor; it cannot
// invent an implementation digest for code that is not deployed.
type Descriptor struct {
	Code                   string `json:"code"`
	Version                uint32 `json:"version"`
	Protocol               string `json:"protocol"`
	MinimumSemanticVersion string `json:"minimum_semantic_version"`
	ImplementationDigest   string `json:"implementation_digest"`
}

var descriptors = buildDescriptors([]struct {
	code, protocol string
	version        uint32
}{
	{"openai_chat", "openai", 1},
	{"openai_responses", "openai_responses", 1},
	{"anthropic_messages", "anthropic_messages", 1},
	{"google_generate_content", "google_generate_content", 1},
	{"volcengine_responses_v3", "volcengine_responses_v3", 1},
	{"openai_images", "openai_images", 1},
	{"generic", "video_generation", 1},
	{"seedance", "seedance", 1},
	{"aicost_models_v1", "catalog_discovery", 1},
	{"aicost_pricing_v1", "catalog_discovery", 1},
})

func buildDescriptors(values []struct {
	code, protocol string
	version        uint32
}) []Descriptor {
	out := make([]Descriptor, 0, len(values))
	for _, value := range values {
		digest := sha256.Sum256([]byte("prism-gateway-adapter:v2\nrevision=" + BuildRevision + "\ncode=" + value.code + "\nversion=" + uintString(value.version)))
		out = append(out, Descriptor{
			Code: value.code, Version: value.version, Protocol: value.protocol,
			MinimumSemanticVersion: "1.0.0", ImplementationDigest: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code == out[j].Code {
			return out[i].Version < out[j].Version
		}
		return out[i].Code < out[j].Code
	})
	return out
}

func uintString(value uint32) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [10]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}

// Descriptors returns a copy so callers cannot mutate the process manifest.
func Descriptors() []Descriptor {
	return append([]Descriptor(nil), descriptors...)
}

func DescriptorFor(code string, version uint32) (Descriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.Code == code && descriptor.Version == version {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// SemanticDigest identifies the catalog schema and all executable adapters in
// this binary. Deployment readiness must present this exact value.
func SemanticDigest() string {
	var body strings.Builder
	body.WriteString("catalog-schema-v1\n")
	for _, descriptor := range descriptors {
		body.WriteString(descriptor.Code)
		body.WriteByte('@')
		body.WriteString(uintString(descriptor.Version))
		body.WriteByte(':')
		body.WriteString(descriptor.ImplementationDigest)
		body.WriteByte('\n')
	}
	digest := sha256.Sum256([]byte(body.String()))
	return hex.EncodeToString(digest[:])
}
