package service

import (
	"os"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}
