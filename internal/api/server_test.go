// Package api_test verifies HTTP behavior for the custody API.
package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rvmz/mpc-custody/internal/api"
	"github.com/rvmz/mpc-custody/internal/chains/bitcoin"
	"github.com/rvmz/mpc-custody/internal/chains/evm"
	"github.com/rvmz/mpc-custody/internal/observability"
	"github.com/rvmz/mpc-custody/internal/signing"
	"github.com/rvmz/mpc-custody/internal/store"
	"github.com/rvmz/mpc-custody/internal/wallet"
)

func TestCreateWalletEndpoint(t *testing.T) {
	handler := newTestHandler()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/wallets", bytes.NewBufferString(`{"chain":"evm"}`))
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}

	var created wallet.Wallet
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode wallet: %v", err)
	}
	if created.ID == "" || created.Address == "" {
		t.Fatalf("created wallet missing identifiers: %+v", created)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	handler := newTestHandler()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/plain; version=0.0.4" {
		t.Fatalf("content-type = %q", contentType)
	}
}

func newTestHandler() http.Handler {
	metrics := observability.NewMetrics()
	registry := wallet.NewChainRegistry(
		bitcoin.NewAdapter("testnet"),
		evm.NewAdapter(31337),
	)
	signer, err := signing.NewDemoQuorumBackend()
	if err != nil {
		panic(err)
	}
	service := wallet.NewService(
		store.NewMemoryStore(),
		registry,
		signer,
		metrics,
	)
	logger := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
	return api.NewServer(service, metrics, logger).Handler()
}
