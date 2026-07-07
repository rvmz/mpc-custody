// Package main starts the custody wallet API service.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rvmz/mpc-custody/internal/api"
	"github.com/rvmz/mpc-custody/internal/chains/bitcoin"
	"github.com/rvmz/mpc-custody/internal/chains/evm"
	"github.com/rvmz/mpc-custody/internal/config"
	"github.com/rvmz/mpc-custody/internal/observability"
	"github.com/rvmz/mpc-custody/internal/signing"
	"github.com/rvmz/mpc-custody/internal/store"
	"github.com/rvmz/mpc-custody/internal/wallet"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	metrics := observability.NewMetrics()
	ctx := context.Background()

	custodyStore, closeStore, err := openStore(ctx, cfg, logger)
	if err != nil {
		logger.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer closeStore()

	registry := wallet.NewChainRegistry(
		bitcoin.NewAdapter("testnet"),
		evm.NewAdapter(cfg.EVMChainID, cfg.EVMRPCURL),
	)
	signer, err := signing.NewDemoQuorumBackend(signing.WithEVMPrivateKey(cfg.EVMDevPrivateKey))
	if err != nil {
		logger.Error("create signer failed", "error", err)
		os.Exit(1)
	}
	service := wallet.NewService(
		custodyStore,
		registry,
		signer,
		metrics,
	)
	apiServer := api.NewServer(service, metrics, logger)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("custody api listening",
			"addr", cfg.HTTPAddr,
			"service", cfg.ServiceName,
			"environment", cfg.Environment,
			"broadcast_mode", cfg.BroadcastMode,
			"evm_rpc_configured", cfg.EVMRPCURL != "",
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	<-shutdown

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGraceTime)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("custody api stopped")
}

func openStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (wallet.Store, func(), error) {
	if cfg.DatabaseURL == "" {
		logger.Warn("using in-memory store; set DATABASE_URL for durable persistence")
		return store.NewMemoryStore(), func() {}, nil
	}

	postgresStore, err := store.OpenPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	logger.Info("using postgres store")
	return postgresStore, postgresStore.Close, nil
}
