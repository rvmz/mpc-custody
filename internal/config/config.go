// Package config loads runtime configuration for the custody service.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config contains service runtime settings.
type Config struct {
	HTTPAddr          string
	ServiceName       string
	Environment       string
	BroadcastMode     string
	DatabaseURL       string
	EVMRPCURL         string
	EVMChainID        uint64
	EVMDevPrivateKey  string
	ShutdownGraceTime time.Duration
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		HTTPAddr:          env("HTTP_ADDR", ":8080"),
		ServiceName:       env("SERVICE_NAME", "mpc-custody-api"),
		Environment:       env("ENVIRONMENT", "local"),
		BroadcastMode:     env("BROADCAST_MODE", "mock"),
		DatabaseURL:       env("DATABASE_URL", ""),
		EVMRPCURL:         env("EVM_RPC_URL", ""),
		EVMChainID:        envUint64("EVM_CHAIN_ID", 31337),
		EVMDevPrivateKey:  env("EVM_DEV_PRIVATE_KEY", ""),
		ShutdownGraceTime: envDuration("SHUTDOWN_GRACE_SECONDS", 10*time.Second),
	}
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func envUint64(key string, fallback uint64) uint64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return fallback
	}
	return parsed
}
