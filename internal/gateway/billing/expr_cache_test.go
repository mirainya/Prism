package billing

import (
	"fmt"
	"sync"
	"testing"
)

func TestExpressionCacheReusesParseAndKeysOnManifest(t *testing.T) {
	cache := NewExpressionCache()
	declared := declare("p")
	first, err := cache.Parse("p * 2", declared, "digest-a")
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Parse("p * 2", declared, "digest-a")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatal("identical source under one manifest was parsed twice")
	}
	// The same text under a different manifest is a different expression, so it
	// must not reuse the first entry's whitelist.
	other, err := cache.Parse("p * 2", declared, "digest-b")
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatal("different manifests shared one cache entry")
	}
	if _, err := cache.Parse("q * 2", declared, "digest-a"); err == nil {
		t.Fatal("undeclared identifier was accepted through the cache")
	}
	if cache.Len() != 2 {
		t.Fatalf("cache len=%d, want 2 (failures are not cached)", cache.Len())
	}
}

func TestExpressionCacheEvictsOldestBeyondCapacity(t *testing.T) {
	cache := NewExpressionCache()
	declared := declare("p")
	for index := range expressionCacheSize + 50 {
		if _, err := cache.Parse(fmt.Sprintf("p + %d", index), declared, "digest"); err != nil {
			t.Fatal(err)
		}
	}
	if cache.Len() != expressionCacheSize {
		t.Fatalf("cache len=%d, want %d", cache.Len(), expressionCacheSize)
	}
}

func TestExpressionCacheIsConcurrencySafe(t *testing.T) {
	cache := NewExpressionCache()
	declared := declare("p")
	var group sync.WaitGroup
	for worker := range 8 {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for index := range 64 {
				source := fmt.Sprintf("p * %d", index%16)
				if _, err := cache.Parse(source, declared, "digest"); err != nil {
					t.Errorf("worker %d: %v", worker, err)
					return
				}
			}
		}(worker)
	}
	group.Wait()
	if cache.Len() != 16 {
		t.Fatalf("cache len=%d, want 16", cache.Len())
	}
}

func TestExpressionCacheNilFallsBackToDirectParse(t *testing.T) {
	var cache *ExpressionCache
	declared := declare("p")
	if _, err := cache.Parse("p * 2", declared, "digest"); err != nil {
		t.Fatal(err)
	}
	// An empty digest means the caller cannot prove which manifest applies, so
	// caching is skipped rather than keyed on the source alone.
	real := NewExpressionCache()
	if _, err := real.Parse("p * 2", declared, ""); err != nil {
		t.Fatal(err)
	}
	if real.Len() != 0 {
		t.Fatalf("cache len=%d, want 0", real.Len())
	}
}
