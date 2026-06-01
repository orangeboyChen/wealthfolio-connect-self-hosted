package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// ActivityHandler answers GET /api/v1/sync/brokerage/accounts/{id}/activities.
type ActivityHandler struct {
	svc *appbrokerage.ActivityService
}

// NewActivityHandler wires the handler.
func NewActivityHandler(svc *appbrokerage.ActivityService) *ActivityHandler {
	return &ActivityHandler{svc: svc}
}

// RegisterAPIRoutes mounts the activities path.
func (h *ActivityHandler) RegisterAPIRoutes(r chi.Router) {
	r.Get("/sync/brokerage/accounts/{accountID}/activities", h.List)
}

// ─── DTOs ────────────────────────────────────────────────────────────────

type currencyDTO struct {
	ID   string `json:"id,omitempty"`
	Code string `json:"code"`
	Name string `json:"name,omitempty"`
}

type symbolTypeDTO struct {
	ID          string `json:"id,omitempty"`
	Code        string `json:"code"`
	Description string `json:"description,omitempty"`
	IsSupported bool   `json:"is_supported"`
}

type exchangeDTO struct {
	ID      string `json:"id,omitempty"`
	Code    string `json:"code"`
	MICCode string `json:"mic_code,omitempty"`
	Name    string `json:"name,omitempty"`
	Suffix  string `json:"suffix,omitempty"`
}

type symbolDTO struct {
	ID          string        `json:"id,omitempty"`
	Symbol      string        `json:"symbol"`
	RawSymbol   string        `json:"raw_symbol,omitempty"`
	Description string        `json:"description,omitempty"`
	Name        string        `json:"name,omitempty"`
	Type        symbolTypeDTO `json:"type"`
	Exchange    exchangeDTO   `json:"exchange"`
	Currency    currencyDTO   `json:"currency"`
	FIGICode    string        `json:"figi_code,omitempty"`
}

type optionSymbolDTO struct {
	ID             string    `json:"id,omitempty"`
	Ticker         string    `json:"ticker"`
	OptionType     string    `json:"option_type"`
	StrikePrice    float64   `json:"strike_price"`
	ExpirationDate string    `json:"expiration_date"`
	IsMiniOption   bool      `json:"is_mini_option"`
	Underlying     symbolDTO `json:"underlying_symbol"`
}

type activityDTO struct {
	ID                  string           `json:"id"`
	Symbol              *symbolDTO       `json:"symbol"`
	OptionSymbol        *optionSymbolDTO `json:"option_symbol"`
	Price               float64          `json:"price"`
	Units               float64          `json:"units"`
	Amount              float64          `json:"amount"`
	Currency            currencyDTO      `json:"currency"`
	Type                string           `json:"type"`
	Subtype             *string          `json:"subtype"`
	RawType             string           `json:"raw_type,omitempty"`
	OptionType          *string          `json:"option_type"`
	Description         string           `json:"description,omitempty"`
	TradeDate           time.Time        `json:"trade_date"`
	SettlementDate      *time.Time       `json:"settlement_date"`
	Fee                 float64          `json:"fee"`
	FxRate              *float64         `json:"fx_rate"`
	Institution         string           `json:"institution,omitempty"`
	ExternalReferenceID string           `json:"external_reference_id,omitempty"`
	ProviderType        string           `json:"provider_type,omitempty"`
	SourceSystem        string           `json:"source_system,omitempty"`
	SourceRecordID      string           `json:"source_record_id,omitempty"`
	SourceGroupID       *string          `json:"source_group_id"`
	MappingMetadata     map[string]any   `json:"mapping_metadata"`
	NeedsReview         bool             `json:"needs_review"`
}

type paginationDTO struct {
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	HasMore bool `json:"has_more"`
}

type activitiesResponse struct {
	Activities []activityDTO `json:"activities"`
	Pagination paginationDTO `json:"pagination"`
}

func toCurrencyDTO(c brokerage.Currency) currencyDTO {
	return currencyDTO{Code: c.Code, Name: c.Name}
}

func toSymbolDTO(s brokerage.Symbol) symbolDTO {
	return symbolDTO{
		Symbol:      s.Symbol,
		RawSymbol:   s.RawSymbol,
		Description: s.Description,
		Name:        s.Name,
		Type:        symbolTypeDTO{Code: s.Type.Code, Description: s.Type.Description, IsSupported: s.Type.IsSupported},
		Exchange:    exchangeDTO{Code: s.Exchange.Code, MICCode: s.Exchange.MICCode, Name: s.Exchange.Name, Suffix: s.Exchange.Suffix},
		Currency:    toCurrencyDTO(s.Currency),
		FIGICode:    s.FIGICode,
	}
}

func toOptionSymbolDTO(o brokerage.OptionSymbol) optionSymbolDTO {
	return optionSymbolDTO{
		Ticker:         o.Ticker,
		OptionType:     string(o.OptionType),
		StrikePrice:    o.StrikePrice,
		ExpirationDate: o.ExpirationDate.Format("2006-01-02"),
		IsMiniOption:   o.IsMiniOption,
		Underlying:     toSymbolDTO(o.Underlying),
	}
}

func toActivityDTO(a brokerage.Activity) activityDTO {
	dto := activityDTO{
		ID:                  a.ID,
		Price:               a.Price,
		Units:               a.Units,
		Amount:              a.Amount,
		Currency:            toCurrencyDTO(a.Currency),
		Type:                string(a.Type),
		RawType:             a.RawType,
		Description:         a.Description,
		TradeDate:           a.TradeDate,
		SettlementDate:      a.SettlementDate,
		Fee:                 a.Fee,
		FxRate:              a.FxRate,
		Institution:         a.Institution,
		ExternalReferenceID: a.ExternalReferenceID,
		ProviderType:        a.ProviderType,
		SourceSystem:        a.SourceSystem,
		SourceRecordID:      a.SourceRecordID,
		NeedsReview:         a.NeedsReview,
	}
	if a.Symbol != nil {
		s := toSymbolDTO(*a.Symbol)
		dto.Symbol = &s
	}
	if a.OptionSymbol != nil {
		o := toOptionSymbolDTO(*a.OptionSymbol)
		dto.OptionSymbol = &o
	}
	if a.Subtype != "" {
		s := a.Subtype
		dto.Subtype = &s
	}
	if a.OptionType != "" {
		s := a.OptionType
		dto.OptionType = &s
	}
	if a.SourceGroupID != "" {
		s := a.SourceGroupID
		dto.SourceGroupID = &s
	}
	return dto
}

// List handles paginated activity queries with optional date filters.
func (h *ActivityHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	q := r.URL.Query()

	offset, err := parseIntDefault(q.Get("offset"), 0)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "invalid_request", "INVALID_OFFSET", "offset must be an integer")
		return
	}
	limit, err := parseIntDefault(q.Get("limit"), appbrokerage.DefaultActivityLimit)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "invalid_request", "INVALID_LIMIT", "limit must be an integer")
		return
	}
	startDate, err := parseDate(q.Get("start_date"))
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "invalid_request", "INVALID_START_DATE", "start_date must be YYYY-MM-DD")
		return
	}
	endDate, err := parseDate(q.Get("end_date"))
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "invalid_request", "INVALID_END_DATE", "end_date must be YYYY-MM-DD")
		return
	}

	res, err := h.svc.List(r.Context(), appbrokerage.ActivityQuery{
		AccountID: accountID,
		StartDate: startDate,
		EndDate:   endDate,
		Offset:    offset,
		Limit:     limit,
	})
	if err != nil {
		if errors.Is(err, appbrokerage.ErrAccountNotFound) {
			middleware.WriteError(w, http.StatusNotFound, "not_found", "ACCOUNT_NOT_FOUND", "account not found")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "internal_error", "internal_error", err.Error())
		return
	}
	out := activitiesResponse{
		Activities: make([]activityDTO, 0, len(res.Items)),
		Pagination: paginationDTO{Offset: res.Offset, Limit: res.Limit, Total: res.Total, HasMore: res.HasMore},
	}
	for _, a := range res.Items {
		out.Activities = append(out.Activities, toActivityDTO(a))
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}

func parseIntDefault(raw string, def int) (int, error) {
	if raw == "" {
		return def, nil
	}
	return strconv.Atoi(raw)
}

func parseDate(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
