package logger

import (
	"go.uber.org/zap"
)

var Log *zap.Logger

func New() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	Log = logger
}
