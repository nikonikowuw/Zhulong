package app

import (
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func TestFxProviderFailsValidationWhenRequiredLoggerIsMissing(t *testing.T) {
	invokeStore := fx.Invoke(func(*database.Store) {})
	options := fx.Options(
		fx.Provide(provideDatabase),
		fx.Supply(Config{}),
		invokeStore,
		fx.NopLogger,
	)
	if err := fx.ValidateApp(options); err == nil || !strings.Contains(err.Error(), "*zap.Logger") {
		t.Fatalf("missing logger did not produce an Fx graph error: %v", err)
	}

	withLogger := fx.Options(
		fx.Provide(provideDatabase),
		fx.Supply(Config{}, zap.NewNop()),
		invokeStore,
		fx.NopLogger,
	)
	if err := fx.ValidateApp(withLogger); err != nil {
		t.Fatalf("explicit test logger did not satisfy the provider: %v", err)
	}
}

func TestApplicationModuleDependencyGraph(t *testing.T) {
	if err := fx.ValidateApp(fx.Supply(DefaultConfig()), Module, fx.NopLogger); err != nil {
		t.Fatalf("application Fx graph validation: %v", err)
	}
}
