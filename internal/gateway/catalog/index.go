package catalog

import (
	"fmt"
	"sort"
)

// Candidate is the immutable routing snapshot used by request workers. IDs
// are release-scoped; no runtime lookup is allowed to silently mix releases.
type Candidate struct {
	RouteID, SKUID, OfferingID uint64
	Priority                   uint32
	Weight                     uint32
}

type ModelOperation struct {
	ModelName  string
	Operation  string
	SKUID      uint64
	Candidates []Candidate
}

type Index struct {
	ReleaseID   uint64
	ContentHash string
	Operations  map[string]ModelOperation
}

func CompileIndex(releaseID uint64, contentHash string, operations []ModelOperation) (Index, error) {
	if releaseID == 0 || contentHash == "" {
		return Index{}, fmt.Errorf("catalog index: release and content hash are required")
	}
	index := Index{ReleaseID: releaseID, ContentHash: contentHash, Operations: make(map[string]ModelOperation, len(operations))}
	for _, operation := range operations {
		model, err := NormalizeAPIName(operation.ModelName)
		if err != nil {
			return Index{}, err
		}
		name, err := NormalizeAPIName(operation.Operation)
		if err != nil {
			return Index{}, err
		}
		if operation.SKUID == 0 || len(operation.Candidates) == 0 {
			return Index{}, fmt.Errorf("catalog index: %s/%s has no SKU or route", model, name)
		}
		key := model + "\x00" + name
		if _, exists := index.Operations[key]; exists {
			return Index{}, fmt.Errorf("catalog index: duplicate operation %s/%s", model, name)
		}
		candidateCopy := append([]Candidate(nil), operation.Candidates...)
		for _, candidate := range candidateCopy {
			if candidate.RouteID == 0 || candidate.OfferingID == 0 || candidate.SKUID != operation.SKUID || candidate.Weight == 0 {
				return Index{}, fmt.Errorf("catalog index: invalid candidate for %s/%s", model, name)
			}
		}
		sort.SliceStable(candidateCopy, func(i, j int) bool {
			if candidateCopy[i].Priority != candidateCopy[j].Priority {
				return candidateCopy[i].Priority < candidateCopy[j].Priority
			}
			return candidateCopy[i].RouteID < candidateCopy[j].RouteID
		})
		index.Operations[key] = ModelOperation{ModelName: model, Operation: name, SKUID: operation.SKUID, Candidates: candidateCopy}
	}
	return index, nil
}

func (i Index) Lookup(modelName, operation string) (ModelOperation, bool) {
	model, err := NormalizeAPIName(modelName)
	if err != nil {
		return ModelOperation{}, false
	}
	name, err := NormalizeAPIName(operation)
	if err != nil {
		return ModelOperation{}, false
	}
	value, ok := i.Operations[model+"\x00"+name]
	if !ok {
		return ModelOperation{}, false
	}
	value.Candidates = append([]Candidate(nil), value.Candidates...)
	return value, true
}
