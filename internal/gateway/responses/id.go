package responses

import (
	"encoding/hex"

	"github.com/google/uuid"
)

func newResponseID() string {
	id := uuid.New()
	return "resp_" + hex.EncodeToString(id[:])[:31]
}
