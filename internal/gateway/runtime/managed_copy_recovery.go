package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/config"
)

func (d *AsyncDispatcher) recoverManagedCopy(ctx context.Context, item repository.OutboxItem, target repository.DeliveryRecoveryTarget) error {
	if d == nil || d.service == nil || target.Mode != "managed_copy" || target.SourceKind != "remote_url" || target.SourceBlobID == 0 || target.SourceSequence == 0 {
		return &PermanentDispatchError{Code: "invalid_managed_copy_recovery"}
	}
	sourceURL, err := d.openBlob(ctx, target.SourceBlobID, fmt.Sprintf("delivery:%d:source:%d", target.DeliveryID, target.SourceSequence), "gateway-result-source", false)
	if err != nil {
		return err
	}
	defer clear(sourceURL)
	role, err := managedCopyRecoveryRole(target.ResourceKind, target.Ordinal)
	if err != nil {
		return err
	}
	copy, err := d.service.prepareManagedResultCopyAt(ctx, target.AttemptID, target.Ordinal, delivery.RemoteResult{Role: role, URL: string(sourceURL)})
	if err != nil {
		return err
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	return d.service.ApplyManagedCopyRecovery(commitCtx, item, copy)
}

func managedCopyRecoveryRole(resourceKind string, ordinal uint32) (string, error) {
	switch resourceKind {
	case delivery.ResourceCapabilityTask:
		return "image", nil
	case delivery.ResourceVideoTask:
		if ordinal == 0 {
			return "video", nil
		}
		if ordinal == 1 {
			return "thumbnail", nil
		}
	}
	return "", &PermanentDispatchError{Code: "invalid_managed_copy_ordinal"}
}

// prepareManagedResultCopyAt retries exactly one result ordinal. The object
// key remains the one fixed by the original Attempt and ordinal.
func (s *Service) prepareManagedResultCopyAt(ctx context.Context, attemptID uint64, ordinal uint32, source delivery.RemoteResult) (ManagedCopy, error) {
	if s == nil || s.Store == nil || attemptID == 0 || !delivery.ValidSource(source) {
		return ManagedCopy{}, &PermanentDispatchError{Code: "invalid_managed_copy_source"}
	}
	storage, err := s.Store.ReadAttemptFileStorage(ctx, attemptID)
	if err != nil {
		return ManagedCopy{}, err
	}
	if storage.APIKey == "" {
		return ManagedCopy{}, &PermanentDispatchError{Code: "managed_copy_storage_unconfigured"}
	}
	maxBytes := config.FileStorageMaxResultSizeBytes()
	if source.URL == "" {
		return ManagedCopy{}, &PermanentDispatchError{Code: "invalid_managed_copy_recovery_source"}
	}
	prepared, err := s.prepareManagedURLCopy(ctx, storage, attemptID, ordinal, source, maxBytes)
	if err != nil {
		return ManagedCopy{}, err
	}
	if prepared.FailureReason == "" {
		return prepared.ManagedCopy, nil
	}
	if delivery.RetryableManagedCopyFailure(prepared.FailureReason) {
		return ManagedCopy{}, fmt.Errorf("managed copy URL import failed: %s", prepared.FailureReason)
	}
	return ManagedCopy{}, &PermanentDispatchError{Code: prepared.FailureReason}
}
