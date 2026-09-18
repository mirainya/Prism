package runtime

import (
	"context"
	"database/sql"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func (s *Service) configureMediaDelivery(ctx context.Context, tx *sql.Tx, in *SubmitInput) error {
	if s == nil || s.Store == nil || tx == nil || in == nil {
		return repository.ErrInvalidInput
	}
	if in.ResourceKind != delivery.ResourceVideoTask && in.ResourceKind != delivery.ResourceCapabilityTask {
		return nil
	}
	storage, err := s.Store.ReadTokenFileStorage(ctx, tx, in.Call.UserID, in.Call.TokenID)
	if err != nil {
		return err
	}
	if storage.APIKey == "" {
		in.Call.DeliveryMode = "reference"
		in.Call.XFSAPIKey = ""
	} else {
		in.Call.DeliveryMode = "managed_copy"
		in.Call.XFSAPIKey = storage.APIKey
	}
	return nil
}
