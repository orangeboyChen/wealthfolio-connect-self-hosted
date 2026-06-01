package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// HoldingHandler answers GET /api/v1/sync/brokerage/accounts/{id}/holdings.
type HoldingHandler struct {
	svc *appbrokerage.HoldingService
}

// NewHoldingHandler wires the handler.
func NewHoldingHandler(svc *appbrokerage.HoldingService) *HoldingHandler {
	return &HoldingHandler{svc: svc}
}

// RegisterAPIRoutes mounts the holdings path.
func (h *HoldingHandler) RegisterAPIRoutes(r chi.Router) {
	r.Get("/sync/brokerage/accounts/{accountID}/holdings", h.Get)
}

// ─── DTOs ────────────────────────────────────────────────────────────────

type accountSummaryDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Number  string `json:"number"`
	RawType string `json:"raw_type"`
}

type balanceLineDTO struct {
	Currency    currencyDTO `json:"currency"`
	Cash        float64     `json:"cash"`
	BuyingPower float64     `json:"buying_power"`
}

type positionSymbolDTO struct {
	Symbol      symbolDTO `json:"symbol"`
	ID          string    `json:"id"`
	Description string    `json:"description"`
}

type positionDTO struct {
	Symbol               positionSymbolDTO `json:"symbol"`
	Units                float64           `json:"units"`
	Price                float64           `json:"price"`
	OpenPnL              float64           `json:"open_pnl"`
	AveragePurchasePrice float64           `json:"average_purchase_price"`
	Currency             currencyDTO       `json:"currency"`
	CashEquivalent       bool              `json:"cash_equivalent"`
}

type optionPositionDTO struct {
	OptionSymbol         optionSymbolDTO `json:"option_symbol"`
	Units                float64         `json:"units"`
	Price                float64         `json:"price"`
	AveragePurchasePrice float64         `json:"average_purchase_price"`
	Currency             currencyDTO     `json:"currency"`
}

type holdingsResponse struct {
	Account         accountSummaryDTO   `json:"account"`
	Balances        []balanceLineDTO    `json:"balances"`
	Positions       []positionDTO       `json:"positions"`
	OptionPositions []optionPositionDTO `json:"option_positions"`
}

func toBalanceDTO(b brokerage.Balance) balanceLineDTO {
	return balanceLineDTO{Currency: toCurrencyDTO(b.Currency), Cash: b.Cash, BuyingPower: b.BuyingPower}
}

func toPositionDTO(p brokerage.Position) positionDTO {
	return positionDTO{
		Symbol: positionSymbolDTO{
			Symbol:      toSymbolDTO(p.Symbol),
			ID:          "holding-" + p.Symbol.Symbol,
			Description: p.Symbol.Description,
		},
		Units:                p.Units,
		Price:                p.Price,
		OpenPnL:              p.OpenPnL,
		AveragePurchasePrice: p.AveragePurchasePrice,
		Currency:             toCurrencyDTO(p.Currency),
		CashEquivalent:       p.CashEquivalent,
	}
}

func toOptionPositionDTO(o brokerage.OptionPosition) optionPositionDTO {
	return optionPositionDTO{
		OptionSymbol:         toOptionSymbolDTO(o.OptionSymbol),
		Units:                o.Units,
		Price:                o.Price,
		AveragePurchasePrice: o.AveragePurchasePrice,
		Currency:             toCurrencyDTO(o.Currency),
	}
}

// Get returns the latest holdings snapshot for the supplied account.
func (h *HoldingHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	res, err := h.svc.Get(r.Context(), accountID)
	if err != nil {
		if errors.Is(err, appbrokerage.ErrAccountNotFound) {
			middleware.WriteError(w, http.StatusNotFound, "not_found", "ACCOUNT_NOT_FOUND", "account not found")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "internal_error", "internal_error", err.Error())
		return
	}
	out := holdingsResponse{
		Account: accountSummaryDTO{
			ID:      res.Account.ID,
			Name:    res.Account.Name,
			Number:  res.Account.AccountNumber,
			RawType: res.Account.RawType,
		},
		Balances:        make([]balanceLineDTO, 0, len(res.Holdings.Balances)),
		Positions:       make([]positionDTO, 0, len(res.Holdings.Positions)),
		OptionPositions: make([]optionPositionDTO, 0, len(res.Holdings.OptionPositions)),
	}
	for _, b := range res.Holdings.Balances {
		out.Balances = append(out.Balances, toBalanceDTO(b))
	}
	for _, p := range res.Holdings.Positions {
		out.Positions = append(out.Positions, toPositionDTO(p))
	}
	for _, o := range res.Holdings.OptionPositions {
		out.OptionPositions = append(out.OptionPositions, toOptionPositionDTO(o))
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}
