package repository

import (
	"strconv"
	"strings"
	"testing"
)

func TestValidCatalogRequestMethodRestrictsGenericExtensions(t *testing.T) {
	if !validCatalogRequestMethod("seedance", "POST") {
		t.Fatal("seedance POST was rejected")
	}
	for _, method := range []string{"GET", "DELETE", "PUT", "PATCH"} {
		if !validCatalogRequestMethod("generic", method) {
			t.Fatalf("generic method %s was rejected", method)
		}
		if validCatalogRequestMethod("seedance", method) {
			t.Fatalf("non-generic method %s was accepted", method)
		}
	}
	for _, method := range []string{"", "OPTIONS", "post"} {
		if validCatalogRequestMethod("generic", method) {
			t.Fatalf("invalid method %q was accepted", method)
		}
	}
}

func TestValidCatalogTaskPolicyRequiresExecutableVideoSemantics(t *testing.T) {
	for _, adapterCode := range []string{"generic", "seedance"} {
		if !validCatalogTaskPolicy(adapterCode, "task", "none") {
			t.Fatalf("valid %s video policy was rejected", adapterCode)
		}
		for _, value := range [][2]string{{"request", "none"}, {"task", "upstream"}, {"task", "local_only"}} {
			if validCatalogTaskPolicy(adapterCode, value[0], value[1]) {
				t.Fatalf("invalid %s video policy %q/%q was accepted", adapterCode, value[0], value[1])
			}
		}
	}
	if !validCatalogTaskPolicy("openai_chat", "request", "none") {
		t.Fatal("synchronous request policy was rejected")
	}
	for _, value := range [][2]string{{"task", "none"}, {"request", "upstream"}, {"request", "local_only"}} {
		if validCatalogTaskPolicy("openai_chat", value[0], value[1]) {
			t.Fatalf("invalid synchronous policy %q/%q was accepted", value[0], value[1])
		}
	}
}

func TestCatalogAllowedHostsIncludesBaseAndDeduplicates(t *testing.T) {
	hosts, err := catalogAllowedHosts(CatalogProductInput{
		BaseURL: "https://API.example.com/v1",
		AllowedHosts: []CatalogAllowedHostInput{
			{Protocol: "https", Host: "api.example.com", Port: 443},
			{Protocol: "https", Host: "cdn.example.com", Port: 443},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0].Host != "api.example.com" || hosts[1].Host != "cdn.example.com" {
		t.Fatalf("hosts = %#v", hosts)
	}
}

func TestCatalogAllowedHostsEnforcesEffectiveLimit(t *testing.T) {
	in := CatalogProductInput{BaseURL: "https://api.example.com"}
	for index := 0; index < 32; index++ {
		in.AllowedHosts = append(in.AllowedHosts, CatalogAllowedHostInput{
			Protocol: "https", Host: "cdn" + strconv.Itoa(index) + ".example.com", Port: 443,
		})
	}
	if _, err := catalogAllowedHosts(in); err != ErrInvalidInput {
		t.Fatalf("effective host limit error = %v", err)
	}
}

func TestCatalogProductExecutionDigestIncludesAdapterIdentity(t *testing.T) {
	input := CatalogProductInput{
		BaseURL: "https://api.example.com", Protocol: "video_generation", RequestMethod: "POST",
		RequestPath: "/tasks", AuthScheme: "bearer", TaskScope: "task", CancelMode: "none",
		SourceURLPolicy: "fixed", CapabilityConstraints: []byte(`{}`),
		Adapter: CatalogAdapterInput{
			Code: "generic", Version: 1, Protocol: "video_generation",
			MinimumSemanticVersion: "1.0.0", ImplementationDigest: strings.Repeat("a", 64),
		},
	}
	first := catalogProductExecutionDigest(input)
	input.Adapter.Code = "seedance"
	if second := catalogProductExecutionDigest(input); first == second {
		t.Fatal("adapter identity must change the execution fingerprint")
	}
}
