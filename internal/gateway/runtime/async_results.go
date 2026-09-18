package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
)

type ManagedCopy struct {
	MediaAssetID                             uint64
	ObjectKey, StorageLocator, ObjectVersion string
	ContentType, SHA256                      string
	ContentLength                            uint64
}

type managedCopyPreparation struct {
	ManagedCopy
	FailureReason string
}

const managedResultCleanupTimeout = 15 * time.Second

var downloadManagedResult = safeurl.Download
var uploadManagedResult = func(ctx context.Context, apiKey string, data []byte, contentType, storagePath, filename string) (filestorage.UploadResult, error) {
	return filestorage.WithAPIKey(apiKey).UploadReaderAtPathWithFilename(ctx, bytes.NewReader(data), contentType, storagePath, filename)
}
var verifyManagedResult = func(ctx context.Context, apiKey, locator string, expectedBytes int64, expectedSHA256 string) error {
	return filestorage.WithAPIKey(apiKey).VerifyURL(ctx, locator, expectedBytes, expectedSHA256)
}
var deleteManagedResult = func(ctx context.Context, apiKey, locator string) error {
	return filestorage.WithAPIKey(apiKey).DeleteURL(ctx, locator)
}

type AsyncResultInput struct {
	Item            repository.OutboxItem
	RequestID       uint64
	Response        repository.RequestLogResult
	State           execution.AsyncState
	Facts           billing.Facts
	Result          *repository.BlobInput
	Sources         []delivery.RemoteResult
	SourceURLPolicy string
	NextQuery       time.Time
}

// RecordAsyncResult commits a query's response and the next query or terminal
// call, payload, task slot and billing outcome in one lease-fenced transaction.
func (s *Service) RecordAsyncResult(ctx context.Context, in AsyncResultInput) error {
	if in.RequestID == 0 || in.Item.Action != "query" || in.Response.HTTPStatus == nil || *in.Response.HTTPStatus < 200 || *in.Response.HTTPStatus >= 300 || !in.Response.ResponseComplete || in.Response.ResponseBytesHMAC == "" {
		return repository.ErrInvalidInput
	}
	switch in.State {
	case execution.AsyncAccepted, execution.AsyncRunning:
		if in.NextQuery.IsZero() || in.Result != nil || len(in.Sources) != 0 {
			return repository.ErrInvalidInput
		}
		return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
			action, err := s.recordAsyncResponse(ctx, tx, in)
			if err != nil || action.completed {
				return err
			}
			// Providers may briefly report an older queued state. Preserve the
			// observed running fact, while still advancing the polling sequence.
			state := in.State
			if action.state == execution.AsyncRunning {
				state = execution.AsyncRunning
			}
			if err := s.Store.ScheduleAsyncQuery(ctx, tx, in.Item.AsyncExecutionID, action.state, state, action.version, in.NextQuery); err != nil {
				return err
			}
			if err := s.updateAsyncResourceProjection(ctx, tx, in.Item.AsyncExecutionID, state); err != nil {
				return err
			}
			return s.Store.CompleteAsyncOutbox(ctx, tx, in.Item, true, "")
		})
	case execution.AsyncSucceeded, execution.AsyncFailed, execution.AsyncCancelled:
		if in.State == execution.AsyncSucceeded && (in.Result == nil || len(in.Result.Plaintext) == 0) || in.State != execution.AsyncSucceeded && (in.Result != nil || len(in.Sources) != 0) {
			return repository.ErrInvalidInput
		}
		managedCopies, err := s.prepareManagedCopies(ctx, in)
		if err != nil {
			return err
		}
		err = s.finishAsync(ctx, in.Item.AsyncExecutionID, in.State, "provider_terminal", in.Facts, func(ctx context.Context, tx *sql.Tx) error {
			action, err := s.recordAsyncResponse(ctx, tx, in)
			if err != nil || action.completed {
				return err
			}
			if in.Result != nil {
				if err := s.persistAsyncResult(ctx, tx, action.attemptID, in.RequestID, *in.Result, in.Sources, in.SourceURLPolicy, managedCopies); err != nil {
					return err
				}
			}
			return s.Store.CompleteAsyncOutbox(ctx, tx, in.Item, true, "")
		})
		return err
	default:
		return repository.ErrInvalidInput
	}
}

func (s *Service) persistAsyncResult(ctx context.Context, tx *sql.Tx, attemptID, requestID uint64, result repository.BlobInput, sources []delivery.RemoteResult, sourceURLPolicy string, managedCopies []managedCopyPreparation) error {
	if sourceURLPolicy != "fixed" && sourceURLPolicy != "refreshable" {
		return repository.ErrInvalidInput
	}
	var callID, userID, tokenID uint64
	var resourceKind string
	var deliveryMode string
	if err := tx.QueryRowContext(ctx, `SELECT c.id,c.user_id,c.token_id,c.delivery_mode,r.resource_kind FROM gw_api_call_attempts a JOIN gw_api_calls c ON c.id=a.call_id JOIN gw_api_resources r ON r.call_id=c.id AND r.user_id=c.user_id AND r.token_id=c.token_id WHERE a.id=? FOR UPDATE`, attemptID).Scan(&callID, &userID, &tokenID, &deliveryMode, &resourceKind); err != nil {
		return err
	}
	if deliveryMode != "managed_copy" && deliveryMode != "reference" {
		return repository.ErrConflict
	}
	if err := delivery.ValidateResult(resourceKind, result.Plaintext, sources); err != nil {
		return err
	}
	if resourceKind == delivery.ResourceVideoTask {
		var typed delivery.VideoResult
		if err := json.Unmarshal(result.Plaintext, &typed); err != nil {
			return delivery.ErrInvalidResult
		}
		if typed.Duration != "" {
			if _, err := billing.ParseAmount(typed.Duration, 18, true); err != nil {
				return err
			}
		}
	}
	deliveryIDs := make([]uint64, 0, len(sources))
	if deliveryMode == "reference" {
		for ordinal, source := range sources {
			if delivery.SourceKind(source) != "remote_url" {
				return delivery.ErrInvalidResult
			}
			blob := resultForURL(result, source.URL)
			deliveryID, err := s.Store.CreateReferenceDelivery(ctx, tx, repository.ResultDeliveryInput{CallID: callID, AttemptID: attemptID, Ordinal: uint32(ordinal), UserID: userID, TokenID: tokenID, Mode: "reference", SourceKind: "remote_url"}, blob, requestID, source.ExpiresAt, sourceURLPolicy)
			if err != nil {
				return err
			}
			deliveryIDs = append(deliveryIDs, deliveryID)
		}
	} else if deliveryMode == "managed_copy" {
		if len(managedCopies) != len(sources) {
			return delivery.ErrInvalidResult
		}
		for ordinal, source := range sources {
			prepared := managedCopies[ordinal]
			sourceKind := delivery.SourceKind(source)
			if sourceKind == "" {
				return delivery.ErrInvalidResult
			}
			deliveryInput := repository.ResultDeliveryInput{CallID: callID, AttemptID: attemptID, Ordinal: uint32(ordinal), UserID: userID, TokenID: tokenID, Mode: "managed_copy", SourceKind: sourceKind}
			if prepared.FailureReason != "" {
				var deliveryID uint64
				var err error
				if sourceKind == "remote_url" && delivery.RetryableManagedCopyFailure(prepared.FailureReason) {
					// Keep the observed provider URL encrypted so a transient transfer
					// failure can be repaired without submitting generation again.
					deliveryID, err = s.Store.CreateFailedManagedCopyDelivery(ctx, tx, deliveryInput, resultForURL(result, source.URL), requestID, source.ExpiresAt, prepared.FailureReason)
				} else {
					deliveryID, err = s.Store.CreateResultDelivery(ctx, tx, deliveryInput)
					if err == nil {
						err = s.Store.FailResultDelivery(ctx, tx, deliveryID, prepared.FailureReason)
					}
				}
				if err != nil {
					return err
				}
				deliveryIDs = append(deliveryIDs, deliveryID)
				continue
			}
			copy := prepared.ManagedCopy
			if copy.MediaAssetID == 0 {
				return delivery.ErrInvalidResult
			}
			deliveryID, err := s.Store.CreateManagedCopyDelivery(ctx, tx, repository.ManagedCopyDeliveryInput{ResultDeliveryInput: deliveryInput, MediaAssetID: copy.MediaAssetID, ContentType: copy.ContentType, ContentLength: copy.ContentLength, SHA256: copy.SHA256})
			if err != nil {
				return err
			}
			deliveryIDs = append(deliveryIDs, deliveryID)
		}
	} else {
		return delivery.ErrInvalidResult
	}
	payload, err := delivery.BindResult(resourceKind, result.Plaintext, sources, deliveryIDs)
	if err != nil {
		return err
	}
	result.Plaintext = payload
	retentionUntil := time.Now().UTC().Add(config.ResourceHistoryRetentionDuration())
	result.RetentionUntil = &retentionUntil
	_, err = s.Store.PutCallPayload(ctx, tx, callID, "result", result)
	return err
}

func (s *Service) prepareManagedCopies(ctx context.Context, in AsyncResultInput) ([]managedCopyPreparation, error) {
	if in.State != execution.AsyncSucceeded || in.Result == nil || len(in.Sources) == 0 {
		return nil, nil
	}
	dispatch, err := s.Store.ReadAsyncDispatch(ctx, in.Item.AsyncExecutionID)
	if err != nil {
		return nil, err
	}
	if dispatch.DeliveryMode != "managed_copy" {
		return nil, nil
	}
	return s.prepareManagedResultCopies(ctx, dispatch.AttemptID, dispatch.DeliveryMode, in.Sources)
}

func (s *Service) prepareManagedResultCopies(ctx context.Context, attemptID uint64, deliveryMode string, sources []delivery.RemoteResult) ([]managedCopyPreparation, error) {
	if deliveryMode != "managed_copy" {
		return nil, nil
	}
	if attemptID == 0 {
		return nil, repository.ErrInvalidInput
	}
	storage, err := s.Store.ReadAttemptFileStorage(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if storage.APIKey == "" {
		return nil, repository.ErrConflict
	}
	maxBytes := int64(64 << 20)
	if config.C != nil && config.C.FileStorage.MaxFileSizeMB > 0 {
		maxBytes = int64(config.C.FileStorage.MaxFileSizeMB) * 1024 * 1024
	}
	result := make([]managedCopyPreparation, 0, len(sources))
	for ordinal, source := range sources {
		if !delivery.ValidSource(source) {
			return nil, fmt.Errorf("invalid managed result source")
		}
		var data []byte
		var contentType string
		if source.URL != "" {
			downloaded, err := downloadManagedResult(ctx, source.URL, maxBytes)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyDownloadFailed})
				continue
			}
			data = downloaded.Data
			contentType = strings.TrimSpace(strings.Split(downloaded.ContentType, ";")[0])
		} else {
			if int64(len(source.InlineData)) > maxBytes {
				clear(source.InlineData)
				sources[ordinal].InlineData = []byte{0}
				result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopySizeExceeded})
				continue
			}
			data = source.InlineData
			contentType = strings.TrimSpace(strings.Split(source.ContentType, ";")[0])
		}
		releaseData := func() {
			clear(data)
			if source.URL == "" {
				// Preserve the already validated inline source kind without retaining
				// the complete provider payload during transactional persistence.
				sources[ordinal].InlineData = []byte{0}
			}
		}
		if len(data) == 0 {
			releaseData()
			result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyEmpty})
			continue
		}
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		contentType = strings.ToLower(contentType)
		if !validManagedResultMIME(source.Role, contentType, http.DetectContentType(data)) {
			releaseData()
			result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyContentTypeInvalid})
			continue
		}
		hash := sha256.Sum256(data)
		digest := hex.EncodeToString(hash[:])
		logicalKey := fmt.Sprintf("gateway-result:%d:%d", attemptID, ordinal)
		retentionUntil := time.Now().UTC().Add(config.ResourceHistoryRetentionDuration())
		var asset repository.ManagedCopyAssetRecord
		err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
			var reserveErr error
			asset, reserveErr = s.Store.ReserveManagedCopyAsset(ctx, tx, attemptID, repository.MediaAssetInput{
				UserID: storage.UserID, TokenID: storage.TokenID, Purpose: "result", ObjectKey: logicalKey,
				ContentType: contentType, ContentLength: uint64(len(data)), SHA256: digest, RetentionUntil: &retentionUntil,
			})
			return reserveErr
		})
		if err != nil {
			releaseData()
			return nil, fmt.Errorf("reserve managed result: %w", err)
		}
		if asset.State == "active" {
			releaseData()
			result = append(result, managedCopyPreparation{ManagedCopy: managedCopyFromAsset(asset)})
			continue
		}
		if asset.StorageLocator == "" {
			uploaded, uploadErr := uploadManagedResult(ctx, storage.APIKey, data, contentType, managedResultStoragePath(asset.ID), managedResultStorageFilename(digest, contentType))
			if uploadErr != nil {
				releaseData()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyUploadFailed})
				continue
			}
			locator := uploaded.StorageLocator()
			if locator == "" || len(locator) > 2048 {
				deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
				releaseData()
				result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyUploadFailed})
				continue
			}
			if verifyErr := verifyManagedResult(ctx, storage.APIKey, locator, int64(len(data)), digest); verifyErr != nil {
				deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
				releaseData()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyVerificationFailed})
				continue
			}
			objectVersion := strings.TrimSpace(uploaded.ObjectID)
			if objectVersion == "" {
				objectVersion = strings.TrimSpace(uploaded.ID)
			}
			if recordErr := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
				return s.Store.RecordManagedCopyUpload(ctx, tx, asset.ID, locator, objectVersion)
			}); recordErr != nil {
				deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
				releaseData()
				return nil, fmt.Errorf("record managed result: %w", recordErr)
			}
			asset.StorageLocator, asset.ObjectVersion = locator, objectVersion
		} else if verifyErr := verifyManagedResult(ctx, storage.APIKey, asset.StorageLocator, int64(asset.ContentLength), asset.SHA256); verifyErr != nil {
			releaseData()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			result = append(result, managedCopyPreparation{FailureReason: delivery.ManagedCopyVerificationFailed})
			continue
		}
		releaseData()
		result = append(result, managedCopyPreparation{ManagedCopy: managedCopyFromAsset(asset)})
	}
	return result, nil
}

func deleteManagedResultBestEffort(ctx context.Context, apiKey, locator string) {
	if strings.TrimSpace(locator) == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedResultCleanupTimeout)
	defer cancel()
	_ = deleteManagedResult(cleanupCtx, apiKey, locator)
}

func managedCopyFromAsset(asset repository.ManagedCopyAssetRecord) ManagedCopy {
	return ManagedCopy{MediaAssetID: asset.ID, ObjectKey: asset.ObjectKey, StorageLocator: asset.StorageLocator,
		ObjectVersion: asset.ObjectVersion, ContentType: asset.ContentType, ContentLength: asset.ContentLength, SHA256: asset.SHA256}
}

func managedResultStoragePath(assetID uint64) string {
	prefix := ""
	if config.C != nil {
		prefix = strings.Trim(strings.TrimSpace(config.C.FileStorage.UploadPath), "/")
		if prefix != "" {
			prefix += "/"
		}
	}
	return fmt.Sprintf("%sgateway-results/%d/", prefix, assetID)
}

func managedResultStorageFilename(digest, contentType string) string {
	extension := ".bin"
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	case "image/gif":
		extension = ".gif"
	case "video/mp4":
		extension = ".mp4"
	case "video/webm":
		extension = ".webm"
	case "video/quicktime":
		extension = ".mov"
	}
	return strings.ToLower(strings.TrimSpace(digest)) + extension
}

func validManagedResultMIME(role, declared, detected string) bool {
	detected = strings.ToLower(strings.TrimSpace(strings.Split(detected, ";")[0]))
	prefix := ""
	switch role {
	case "image", "thumbnail":
		prefix = "image/"
	case "video":
		prefix = "video/"
	default:
		return false
	}
	return strings.HasPrefix(declared, prefix) && (strings.HasPrefix(detected, prefix) || detected == "application/octet-stream")
}

func resultForURL(result repository.BlobInput, value string) repository.BlobInput {
	result.Plaintext = []byte(value)
	return result
}

func (s *Service) recordAsyncResponse(ctx context.Context, tx *sql.Tx, in AsyncResultInput) (asyncAction, error) {
	action, err := s.lockAsyncAction(ctx, tx, in.Item, true)
	if err != nil {
		return action, err
	}
	var status string
	var responseHMAC sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,response_bytes_hmac FROM gw_channel_request_logs WHERE id=? AND outbox_id=? AND outbox_attempt_count=? AND attempt_id=? AND action=? FOR UPDATE`, in.RequestID, in.Item.ID, in.Item.Attempts, action.attemptID, in.Item.Action).Scan(&status, &responseHMAC); err != nil {
		return action, err
	}
	if action.completed {
		if status != "response_recorded" || !responseHMAC.Valid || responseHMAC.String != in.Response.ResponseBytesHMAC {
			return action, repository.ErrConflict
		}
		return action, nil
	}
	if action.version != in.Item.StateVersion || action.sequence != in.Item.ActionSeq || !asyncRequestAllowed(action.state, in.Item.Action) {
		return action, repository.ErrConflict
	}
	return action, s.finishRequest(ctx, tx, in.RequestID, "response_recorded", in.Response)
}

// PrepareAsyncDispatch records lost exchanges and acknowledges obsolete actions
// before any network work. An uncertain submit is never sent a second time.
func (s *Service) PrepareAsyncDispatch(ctx context.Context, item repository.OutboxItem) (bool, error) {
	handled := false
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockAsyncAction(ctx, tx, item, true)
		if err != nil || action.completed {
			handled = action.completed
			return err
		}
		if action.version < item.StateVersion || action.sequence < item.ActionSeq {
			return repository.ErrConflict
		}
		obsolete := action.version != item.StateVersion || action.sequence != item.ActionSeq
		var requestID uint64
		var status string
		err = tx.QueryRowContext(ctx, `SELECT id,status FROM gw_channel_request_logs WHERE outbox_id=? AND outbox_attempt_count<? AND action=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, item.ID, item.Attempts, item.Action).Scan(&requestID, &status)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if status == "dispatching" || status == "sent" {
			if err := s.finishRequest(ctx, tx, requestID, "unknown", repository.RequestLogResult{ErrorCode: "worker_exchange_lost"}); err != nil {
				return err
			}
		}
		if obsolete {
			handled = true
			return s.Store.CompleteAsyncOutbox(ctx, tx, item, true, "superseded_action")
		}
		if !asyncRequestAllowed(action.state, item.Action) {
			return repository.ErrConflict
		}
		if requestID == 0 || item.Action != "submit" || status == "not_sent" {
			return nil
		}
		if _, err := s.Store.TransitionAsync(ctx, tx, item.AsyncExecutionID, action.state, execution.AsyncSubmissionUnknown, action.version, "submission_unverified", "recover"); err != nil {
			return err
		}
		if err := s.updateAsyncResourceProjection(ctx, tx, item.AsyncExecutionID, execution.AsyncSubmissionUnknown); err != nil {
			return err
		}
		handled = true
		return s.Store.CompleteAsyncOutbox(ctx, tx, item, true, "")
	})
	return handled, err
}

func (s *Service) RequireAsyncManualReview(ctx context.Context, item repository.OutboxItem) error {
	if item.Action != "recover" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockAsyncAction(ctx, tx, item, true)
		if err != nil || action.completed {
			return err
		}
		if action.version != item.StateVersion || action.sequence != item.ActionSeq || action.state != execution.AsyncSubmissionUnknown {
			return repository.ErrConflict
		}
		if _, err := s.Store.TransitionAsync(ctx, tx, item.AsyncExecutionID, action.state, execution.AsyncManualReview, action.version, "provider_recovery_unavailable", ""); err != nil {
			return err
		}
		if err := s.updateAsyncResourceProjection(ctx, tx, item.AsyncExecutionID, execution.AsyncManualReview); err != nil {
			return err
		}
		return s.Store.CompleteAsyncOutbox(ctx, tx, item, true, "")
	})
}
