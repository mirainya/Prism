package video

import (
	"reflect"
	"testing"
)

func TestParseVideoModelMappingsAcceptsLegacyStrings(t *testing.T) {
	got, err := ParseVideoModelMappings([]byte(`["seedance-2.0"]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []VideoModelMapping{{ModelName: "seedance-2.0", VendorModel: "seedance-2.0"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mappings = %#v, want %#v", got, want)
	}
}

func TestParseVideoModelMappingsAcceptsAliases(t *testing.T) {
	got, err := ParseVideoModelMappings([]byte(`[{"model_name":"video-fast","vendor_model":"seedance-2.0-fast"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ModelName != "video-fast" || got[0].VendorModel != "seedance-2.0-fast" {
		t.Fatalf("unexpected mappings: %#v", got)
	}
	vendorModel, ok := ResolveVideoVendorModel([]byte(`[{"model_name":"video-fast","vendor_model":"seedance-2.0-fast"}]`), "video-fast")
	if !ok || vendorModel != "seedance-2.0-fast" {
		t.Fatalf("vendor model = %q, supported = %v", vendorModel, ok)
	}
}

func TestParseVideoModelMappingsRejectsDuplicatePublicNames(t *testing.T) {
	_, err := ParseVideoModelMappings([]byte(`[
		{"model_name":"video-fast","vendor_model":"seedance-2.0-fast"},
		{"model_name":"video-fast","vendor_model":"another-model"}
	]`))
	if err == nil {
		t.Fatal("expected duplicate public model to fail")
	}
}
