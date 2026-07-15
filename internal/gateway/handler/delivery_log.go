package handler

import (
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

func logDeliveryError(action, callID string, err error) {
	if err == nil || logger.L == nil {
		return
	}
	logger.Error(action, zap.String("call_id", callID), zap.Error(err))
}
