// Package api exposes the custody wallet HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rvmz/mpc-custody/internal/observability"
	"github.com/rvmz/mpc-custody/internal/store"
	"github.com/rvmz/mpc-custody/internal/wallet"
)

// Server owns HTTP routing for the custody API.
type Server struct {
	service *wallet.Service
	metrics *observability.Metrics
	logger  *slog.Logger
}

// NewServer creates an HTTP server wrapper.
func NewServer(service *wallet.Service, metrics *observability.Metrics, logger *slog.Logger) *Server {
	return &Server{service: service, metrics: metrics, logger: logger}
}

// Handler returns the HTTP handler with observability middleware installed.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /metrics", s.metricsHandler)
	mux.HandleFunc("POST /v1/wallets", s.createWallet)
	mux.HandleFunc("POST /v1/transactions", s.proposeTransaction)
	mux.HandleFunc("GET /v1/transactions/", s.transactionAction)
	mux.HandleFunc("POST /v1/transactions/", s.transactionAction)
	return observability.Middleware(s.metrics, s.logger)(mux)
}

type createWalletRequest struct {
	Chain wallet.Chain `json:"chain"`
}

type cosignRequest struct {
	SignerID string `json:"signer_id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.metrics.WritePrometheus(w)
}

func (s *Server) createWallet(w http.ResponseWriter, r *http.Request) {
	var request createWalletRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	created, err := s.service.CreateWallet(r.Context(), request.Chain)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) proposeTransaction(w http.ResponseWriter, r *http.Request) {
	var request wallet.TransactionRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	proposal, err := s.service.ProposeTransaction(r.Context(), request)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, proposal)
}

func (s *Server) transactionAction(w http.ResponseWriter, r *http.Request) {
	id, action, ok := parseTransactionPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("transaction route not found"))
		return
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		s.getTransaction(w, r, id)
	case r.Method == http.MethodPost && action == "cosign":
		s.cosign(w, r, id)
	case r.Method == http.MethodPost && action == "broadcast":
		s.broadcast(w, r, id)
	default:
		writeError(w, http.StatusNotFound, errors.New("transaction route not found"))
	}
}

func (s *Server) getTransaction(w http.ResponseWriter, r *http.Request, id string) {
	proposal, err := s.service.GetTransaction(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *Server) cosign(w http.ResponseWriter, r *http.Request, id string) {
	var request cosignRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	proposal, err := s.service.CoSign(r.Context(), id, request.SignerID)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *Server) broadcast(w http.ResponseWriter, r *http.Request, id string) {
	proposal, err := s.service.Broadcast(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func parseTransactionPath(path string) (string, string, bool) {
	trimmed := strings.TrimPrefix(path, "/v1/transactions/")
	if trimmed == path || trimmed == "" {
		return "", "", false
	}

	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, store.ErrWalletNotFound), errors.Is(err, store.ErrTransactionNotFound):
		return http.StatusNotFound
	case errors.Is(err, wallet.ErrUnsupportedChain), errors.Is(err, wallet.ErrDuplicateApproval), errors.Is(err, wallet.ErrTransactionNotSigned):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
