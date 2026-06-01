package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// AccountHandler answers GET /api/v1/sync/brokerage/accounts.
type AccountHandler struct {
	svc *appbrokerage.AccountService
}

// NewAccountHandler wires the handler.
func NewAccountHandler(svc *appbrokerage.AccountService) *AccountHandler {
	return &AccountHandler{svc: svc}
}

// RegisterAPIRoutes mounts /sync/brokerage/accounts.
func (h *AccountHandler) RegisterAPIRoutes(r chi.Router) {
	r.Get("/sync/brokerage/accounts", h.List)
	r.Patch("/sync/brokerage/accounts/{id}", h.Patch)
}

// ─── DTOs ────────────────────────────────────────────────────────────────

type moneyDTO struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type balanceDTO struct {
	Total moneyDTO `json:"total"`
}

type txSyncDTO struct {
	InitialSyncCompleted bool       `json:"initial_sync_completed"`
	LastSuccessfulSync   *time.Time `json:"last_successful_sync"`
	FirstTransactionDate *string    `json:"first_transaction_date,omitempty"`
}

type holdingsSyncDTO struct {
	InitialSyncCompleted bool       `json:"initial_sync_completed"`
	LastSuccessfulSync   *time.Time `json:"last_successful_sync"`
}

type syncStatusDTO struct {
	Transactions txSyncDTO       `json:"transactions"`
	Holdings     holdingsSyncDTO `json:"holdings"`
}

type ownerDTO struct {
	UserID       string `json:"user_id"`
	FullName     string `json:"full_name"`
	Email        string `json:"email"`
	IsOwnAccount bool   `json:"is_own_account"`
}

type accountDTO struct {
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	AccountNumber          string         `json:"account_number"`
	Type                   string         `json:"type"`
	RawType                string         `json:"raw_type,omitempty"`
	Currency               string         `json:"currency"`
	Balance                balanceDTO     `json:"balance"`
	BrokerageAuthorization string         `json:"brokerage_authorization"`
	InstitutionName        string         `json:"institution_name"`
	SyncEnabled            bool           `json:"sync_enabled"`
	SharedWithHousehold    bool           `json:"shared_with_household"`
	IsPaper                bool           `json:"is_paper"`
	Status                 string         `json:"status"`
	CreatedDate            time.Time      `json:"created_date"`
	SyncStatus             syncStatusDTO  `json:"sync_status"`
	Owner                  ownerDTO       `json:"owner"`
	Meta                   map[string]any `json:"meta"`
}

type accountsResponse struct {
	Accounts []accountDTO `json:"accounts"`
}

func toAccountDTO(a brokerage.Account) accountDTO {
	balanceCurrency := a.BalanceCurrency
	if balanceCurrency == "" {
		balanceCurrency = a.Currency
	}
	var firstTx *string
	if a.FirstTxDate != nil {
		s := a.FirstTxDate.Format("2006-01-02")
		firstTx = &s
	}
	return accountDTO{
		ID:            a.ID,
		Name:          a.Name,
		AccountNumber: a.AccountNumber,
		Type:          string(a.Type),
		RawType:       a.RawType,
		Currency:      a.Currency,
		Balance: balanceDTO{
			Total: moneyDTO{Amount: a.BalanceTotal, Currency: balanceCurrency},
		},
		BrokerageAuthorization: a.BrokerageAuthorization,
		InstitutionName:        a.InstitutionName,
		SyncEnabled:            a.SyncEnabled,
		SharedWithHousehold:    a.SharedWithHousehold,
		IsPaper:                a.IsPaper,
		Status:                 a.Status,
		CreatedDate:            a.CreatedDate,
		SyncStatus: syncStatusDTO{
			Transactions: txSyncDTO{
				InitialSyncCompleted: a.InitialTxSyncDone,
				LastSuccessfulSync:   a.LastTxSync,
				FirstTransactionDate: firstTx,
			},
			Holdings: holdingsSyncDTO{
				InitialSyncCompleted: a.InitialHoldingsDone,
				LastSuccessfulSync:   a.LastHoldingsSync,
			},
		},
		Owner: ownerDTO{
			UserID:       a.OwnerUserID,
			FullName:     a.OwnerFullName,
			Email:        a.OwnerEmail,
			IsOwnAccount: true,
		},
		Meta: nil,
	}
}

// patchRequest is the body schema accepted by PATCH /sync/brokerage/accounts/{id}.
// Only sync_enabled is mutable today; the field is a pointer so callers can
// distinguish "not provided" from "set to false".
type patchRequest struct {
	SyncEnabled *bool `json:"sync_enabled,omitempty"`
}

// Patch mutates the small set of broker-account fields the client is allowed
// to flip. Currently only sync_enabled.
func (h *AccountHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		middleware.WriteError(w, http.StatusBadRequest, "bad_request", "bad_request", "missing account id")
		return
	}
	var body patchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "bad_request", "bad_request", "invalid JSON body")
		return
	}
	if body.SyncEnabled == nil {
		middleware.WriteError(w, http.StatusBadRequest, "bad_request", "bad_request", "no mutable fields provided")
		return
	}
	acc, err := h.svc.SetSyncEnabled(r.Context(), id, *body.SyncEnabled)
	if err != nil {
		if errors.Is(err, appbrokerage.ErrAccountNotFound) {
			middleware.WriteError(w, http.StatusNotFound, "not_found", "not_found", "account not found")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "internal_error", "internal_error", err.Error())
		return
	}
	middleware.WriteJSON(w, http.StatusOK, toAccountDTO(acc))
}

// List returns every brokerage account.
func (h *AccountHandler) List(w http.ResponseWriter, r *http.Request) {
	accs, err := h.svc.List(r.Context())
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "internal_error", "internal_error", err.Error())
		return
	}
	out := accountsResponse{Accounts: make([]accountDTO, 0, len(accs))}
	for _, a := range accs {
		out.Accounts = append(out.Accounts, toAccountDTO(a))
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}
