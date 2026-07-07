// Package api exposes the custody wallet HTTP API.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
	apiKeys []string
}

// Option configures the API server.
type Option func(*Server)

// WithAPIKeys configures API keys for protected custody endpoints.
func WithAPIKeys(keys []string) Option {
	return func(server *Server) {
		server.apiKeys = append([]string(nil), keys...)
	}
}

// NewServer creates an HTTP server wrapper.
func NewServer(service *wallet.Service, metrics *observability.Metrics, logger *slog.Logger, options ...Option) *Server {
	server := &Server{service: service, metrics: metrics, logger: logger}
	for _, option := range options {
		option(server)
	}
	return server
}

// Handler returns the HTTP handler with observability middleware installed.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /metrics", s.metricsHandler)
	mux.HandleFunc("POST /v1/wallets", s.requireAPIKey(s.createWallet))
	mux.HandleFunc("POST /v1/transactions", s.requireAPIKey(s.proposeTransaction))
	mux.HandleFunc("GET /v1/audit/events", s.requireAPIKey(s.listAuditEvents))
	mux.HandleFunc("GET /v1/transactions/", s.requireAPIKey(s.transactionAction))
	mux.HandleFunc("POST /v1/transactions/", s.requireAPIKey(s.transactionAction))
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

	created, err := s.service.CreateWallet(r.Context(), request.Chain, idempotencyKey(r))
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

	proposal, err := s.service.ProposeTransaction(r.Context(), request, idempotencyKey(r))
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

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("limit must be an integer"))
			return
		}
		limit = parsed
	}
	events, err := s.service.ListAuditEvents(r.Context(), wallet.AuditFilter{
		ResourceID: r.URL.Query().Get("resource_id"),
		Limit:      limit,
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(s.apiKeys) == 0 {
			next(w, r)
			return
		}

		provided := r.Header.Get("X-API-Key")
		if provided == "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			provided = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		for _, key := range s.apiKeys {
			if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
				next(w, r)
				return
			}
		}
		writeError(w, http.StatusUnauthorized, errors.New("missing or invalid api key"))
	}
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
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, errors.New("request body must contain a single json object"))
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

func idempotencyKey(r *http.Request) string {
	return r.Header.Get("Idempotency-Key")
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, store.ErrWalletNotFound), errors.Is(err, store.ErrTransactionNotFound):
		return http.StatusNotFound
	case errors.Is(err, wallet.ErrUnauthorizedSigner):
		return http.StatusForbidden
	case errors.Is(err, store.ErrDuplicateIdempotency):
		return http.StatusConflict
	case errors.Is(err, wallet.ErrUnsupportedChain), errors.Is(err, wallet.ErrDuplicateApproval), errors.Is(err, wallet.ErrTransactionNotSigned), errors.Is(err, wallet.ErrTransactionNotProposed), errors.Is(err, wallet.ErrPolicyViolation):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
