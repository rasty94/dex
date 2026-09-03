package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewLogger(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		logger, err := newLogger(slog.LevelInfo, "json", nil)
		require.NoError(t, err)
		require.NotEqual(t, (*slog.Logger)(nil), logger)
	})

	t.Run("Text", func(t *testing.T) {
		logger, err := newLogger(slog.LevelError, "text", nil)
		require.NoError(t, err)
		require.NotEqual(t, (*slog.Logger)(nil), logger)
	})

	t.Run("Unknown", func(t *testing.T) {
		logger, err := newLogger(slog.LevelError, "gofmt", nil)
		require.Error(t, err)
		require.Equal(t, "log format is not one of the supported values (json, text): gofmt", err.Error())
		require.Equal(t, (*slog.Logger)(nil), logger)
	})
}

func TestApplyConfigOverridesValkeyKeyPrefix(t *testing.T) {
	t.Run("defaults when empty", func(t *testing.T) {
		var c Config
		applyConfigOverrides(serveOptions{}, &c)
		require.Equal(t, "dex:", c.Valkey.KeyPrefix)
	})

	t.Run("leaves an explicit prefix alone", func(t *testing.T) {
		c := Config{}
		c.Valkey.KeyPrefix = "custom:"
		applyConfigOverrides(serveOptions{}, &c)
		require.Equal(t, "custom:", c.Valkey.KeyPrefix)
	})
}
