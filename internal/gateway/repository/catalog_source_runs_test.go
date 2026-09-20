package repository

import "testing"

func TestValidateCatalogDiscoveryItemRequiresSelectedGroupMembership(t *testing.T) {
	run := CatalogDiscoveryRun{ExternalGroup: "target-group"}
	item := CatalogDiscoveryItem{Code: "model-a", Groups: []string{"other-group"}, SelectedGroupEnabled: true}
	if err := validateCatalogDiscoveryItem(run, item); err != ErrInvalidInput {
		t.Fatalf("mismatched selected group error=%v", err)
	}
	item.Groups = append(item.Groups, run.ExternalGroup)
	if err := validateCatalogDiscoveryItem(run, item); err != nil {
		t.Fatalf("matching selected group error=%v", err)
	}
}
