package catalog

import "testing"

func TestCompileIndexSortsCandidatesAndCopiesSlices(t *testing.T) {
	index, err := CompileIndex(1, "hash", []ModelOperation{{ModelName: " Model-A ", Operation: "IMAGE.GENERATE", SKUID: 9, Candidates: []Candidate{{RouteID: 2, SKUID: 9, OfferingID: 2, Priority: 2, Weight: 1}, {RouteID: 1, SKUID: 9, OfferingID: 1, Priority: 1, Weight: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := index.Lookup("model-a", "image.generate")
	if !ok || value.Candidates[0].RouteID != 1 {
		t.Fatalf("lookup=%#v", value)
	}
	value.Candidates[0].RouteID = 99
	again, _ := index.Lookup("model-a", "image.generate")
	if again.Candidates[0].RouteID != 1 {
		t.Fatal("lookup leaked mutable slice")
	}
}
