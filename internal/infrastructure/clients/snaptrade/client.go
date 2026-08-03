// Package snaptrade implements read-only import of Interactive Brokers
// accounts that have already been connected through SnapTrade.
package snaptrade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

type selectedAccount struct {
	account    rawAccount
	connection rawConnection
}

// Client is the SnapTrade BrokerClient and optional paged-history client.
type Client struct {
	config config.SnapTradeConfig
	api    *apiClient
	log    zerolog.Logger

	selectedMu sync.RWMutex
	selected   map[string]selectedAccount
	refreshMu  sync.Mutex
	refreshed  map[string]time.Time
}

// New constructs a SnapTrade client. A nil doer creates a private reusable
// http.Client with the configured timeout.
func New(cfg config.SnapTradeConfig, log zerolog.Logger, doer HTTPDoer) (*Client, error) {
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, errors.New("snaptrade: invalid base URL")
	}
	if doer == nil {
		doer = &http.Client{Timeout: cfg.RequestTimeout}
	}
	clk := realClock{}
	sleep := sleeper(contextSleep)
	limiter := newRateLimiter(cfg, clk, sleep)
	api := &apiClient{
		baseURL: baseURL, doer: doer, signer: signer{auth: cfg, clock: clk},
		limiter: limiter, config: cfg, log: log, sleep: sleep, jitter: boundedJitter,
	}
	client := &Client{
		config: cfg, api: api, log: log, selected: make(map[string]selectedAccount),
		refreshed: make(map[string]time.Time),
	}
	log.Info().Str("auth_mode", cfg.AuthMode).Str("package", cfg.Package).
		Msg("SnapTrade client configured")
	if (cfg.Package == authModePersonal) != (cfg.AuthMode == authModePersonal) {
		log.Warn().Str("auth_mode", cfg.AuthMode).Str("package", cfg.Package).
			Msg("SnapTrade authentication mode and package profile are unusual")
	}
	return client, nil
}

// ID returns the stable integration slug.
func (c *Client) ID() string { return "snaptrade" }

// SyncInterval returns the validated per-client synchronization cadence.
func (c *Client) SyncInterval() time.Duration { return c.config.SyncInterval }

// Fetch satisfies BrokerClient. History is deliberately omitted here and is
// streamed through StreamActivities when the application detects the optional
// PagedActivityClient interface.
func (c *Client) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	return c.FetchAccountSnapshot(ctx)
}

// FetchAccountSnapshot discovers IBKR accounts and refreshes their account,
// balance, and current-position snapshots. A failed account is isolated from
// other accounts and never emits an empty holdings replacement.
func (c *Client) FetchAccountSnapshot(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	var connections []rawConnection
	if err := c.api.get(ctx, "/api/v1/authorizations", "connections", "", nil, &connections); err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("list SnapTrade connections: %w", err)
	}
	var accounts []rawAccount
	if err := c.api.get(ctx, "/api/v1/accounts", "accounts", "", nil, &accounts); err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("list SnapTrade accounts: %w", err)
	}
	c.log.Info().Int("discovered_accounts", len(accounts)).Msg("discovered SnapTrade accounts")

	connectionByID := make(map[string]rawConnection, len(connections))
	for _, connection := range connections {
		connectionByID[connection.ID] = connection
	}
	allow := make(map[string]struct{}, len(c.config.AccountIDs))
	for _, id := range c.config.AccountIDs {
		allow[id] = struct{}{}
	}
	foundAllowed := make(map[string]bool, len(allow))
	selected := make(map[string]selectedAccount)
	for _, account := range accounts {
		connection, hasStructuredConnection := connectionByID[account.BrokerageAuthorization]
		isIBKR := false
		if hasStructuredConnection {
			isIBKR = isInteractiveBrokers(connection.Brokerage)
		} else {
			isIBKR = isInteractiveBrokersName(account.InstitutionName)
			connection = fallbackConnection(account)
		}
		if !isIBKR {
			continue
		}
		if len(allow) > 0 {
			if _, ok := allow[account.ID]; !ok {
				continue
			}
			foundAllowed[account.ID] = true
		}
		selected["snaptrade-"+account.ID] = selectedAccount{account: account, connection: connection}
	}
	for id := range allow {
		if !foundAllowed[id] {
			c.log.Warn().Str("account", maskIdentifier(id)).Msg("configured SnapTrade account was not found as an IBKR account")
		}
	}
	c.log.Info().Int("selected_ibkr_accounts", len(selected)).Msg("selected SnapTrade IBKR accounts")
	c.selectedMu.Lock()
	c.selected = selected
	c.selectedMu.Unlock()

	now := c.api.signer.clock.Now().UTC()
	connectionDomains := make([]brokerage.Connection, 0)
	seenConnections := make(map[string]bool)
	localAccounts := make([]brokerage.Account, 0, len(selected))
	holdings := make([]brokerage.Holdings, 0, len(selected))
	var partialErrors []error

	orderedIDs := make([]string, 0, len(selected))
	for localID := range selected {
		orderedIDs = append(orderedIDs, localID)
	}
	sort.Strings(orderedIDs)
	for _, localID := range orderedIDs {
		entry := selected[localID]
		if !seenConnections[entry.connection.ID] {
			connectionDomains = append(connectionDomains, mapConnection(entry.connection, now))
			seenConnections[entry.connection.ID] = true
			if err := c.maybeRefresh(ctx, entry.connection.ID); err != nil {
				partialErrors = append(partialErrors, err)
			}
		}

		if entry.connection.Disabled {
			localAccounts = append(localAccounts, mapAccount(entry.account, entry.connection, now))
			c.log.Warn().Str("account", maskIdentifier(entry.account.ID)).Msg("SnapTrade connection is disabled; preserving cached account data")
			continue
		}
		accountPath := "/api/v1/accounts/" + url.PathEscape(entry.account.ID)
		detail := entry.account
		if err := c.api.get(ctx, accountPath, "account_detail", entry.account.ID, nil, &detail); err != nil {
			localAccounts = append(localAccounts, mapAccount(entry.account, entry.connection, now))
			partialErrors = append(partialErrors, fmt.Errorf("account %s detail: %w", maskIdentifier(entry.account.ID), err))
			continue
		}
		if detail.BrokerageAuthorization == "" {
			detail.BrokerageAuthorization = entry.account.BrokerageAuthorization
		}
		if detail.InstitutionName == "" {
			detail.InstitutionName = entry.account.InstitutionName
		}
		mappedAccount := mapAccount(detail, entry.connection, now)
		if detail.SyncStatus.Holdings.HoldingsUnavailable {
			localAccounts = append(localAccounts, mappedAccount)
			c.log.Warn().Str("account", maskIdentifier(detail.ID)).Msg("SnapTrade reports holdings unavailable; preserving cached holdings")
			continue
		}

		var balances []rawBalance
		balanceErr := c.api.get(ctx, accountPath+"/balances", "balances", entry.account.ID, nil, &balances)
		var positionsResponse rawPositionsResponse
		positionErr := c.api.get(ctx, accountPath+"/positions/all", "positions", entry.account.ID, nil, &positionsResponse)
		if balanceErr != nil || positionErr != nil {
			localAccounts = append(localAccounts, mappedAccount)
			partialErrors = append(partialErrors, errors.Join(
				wrapAccountError(entry.account.ID, "balances", balanceErr),
				wrapAccountError(entry.account.ID, "positions", positionErr),
			))
			continue
		}
		positions, optionPositions := mapPositions(positionsResponse.Results)
		mappedAccount.LastHoldingsSync = &now
		mappedAccount.InitialHoldingsDone = true
		localAccounts = append(localAccounts, mappedAccount)
		holdings = append(holdings, brokerage.Holdings{
			AccountID: localID, Balances: mapBalances(balances), Positions: positions,
			OptionPositions: optionPositions, CapturedAt: now,
		})
	}

	snapshot := domainsync.BrokerSnapshot{
		Connections: connectionDomains, Accounts: localAccounts, Holdings: holdings,
		Activities: make(map[string][]brokerage.Activity),
	}
	if len(connectionDomains) > 0 {
		snapshot.Connection = connectionDomains[0]
	}
	return snapshot, errors.Join(partialErrors...)
}

// StreamActivities imports each requested account page by page, invoking the
// sink before requesting another page. Failed accounts do not stop others.
func (c *Client) StreamActivities(ctx context.Context, states []domainsync.ActivitySyncState, sink domainsync.ActivitySink) error {
	if sink == nil {
		return errors.New("snaptrade: activity sink is nil")
	}
	c.selectedMu.RLock()
	selected := make(map[string]selectedAccount, len(c.selected))
	for key, value := range c.selected {
		selected[key] = value
	}
	c.selectedMu.RUnlock()

	var accountErrors []error
	for _, state := range states {
		entry, ok := selected[state.AccountID]
		if !ok || entry.connection.Disabled {
			continue
		}
		startDate := c.config.HistoryStartDate
		offset := state.NextOffset
		mode := "initial"
		if state.InitialSyncCompleted {
			mode = "incremental"
			offset = 0
			if state.LastSuccessfulSync != nil {
				startDate = state.LastSuccessfulSync.Add(-c.config.IncrementalOverlap).UTC()
			}
		}
		c.log.Info().Str("account", maskIdentifier(entry.account.ID)).Str("sync_mode", mode).
			Int("offset", offset).Msg("starting SnapTrade activity import")
		if err := c.streamAccountActivities(ctx, state.AccountID, entry.account.ID, startDate, offset, mode == "initial", sink); err != nil {
			accountErrors = append(accountErrors, err)
		}
	}
	return errors.Join(accountErrors...)
}

func (c *Client) streamAccountActivities(
	ctx context.Context,
	localAccountID, remoteAccountID string,
	startDate time.Time,
	offset int,
	initial bool,
	sink domainsync.ActivitySink,
) error {
	pageNumber := offset/c.config.PageSize + 1
	for {
		query := url.Values{}
		query.Set("startDate", startDate.UTC().Format("2006-01-02"))
		query.Set("offset", strconv.Itoa(offset))
		query.Set("limit", strconv.Itoa(c.config.PageSize))
		var page rawActivityPage
		endpoint := "/api/v1/accounts/" + url.PathEscape(remoteAccountID) + "/activities"
		if err := c.api.get(ctx, endpoint, "activities", remoteAccountID, query, &page); err != nil {
			return fmt.Errorf("account %s activities offset %d: %w", maskIdentifier(remoteAccountID), offset, err)
		}

		mapped := make([]brokerage.Activity, 0, len(page.Data))
		var firstDate *time.Time
		reviewCount := 0
		for index, raw := range page.Data {
			activity, err := mapActivity(localAccountID, remoteAccountID, raw)
			if err != nil {
				c.log.Warn().Err(mapActivityError(remoteAccountID, index, err)).Msg("skipping invalid SnapTrade activity")
				continue
			}
			if firstDate == nil || activity.TradeDate.Before(*firstDate) {
				value := activity.TradeDate
				firstDate = &value
			}
			if activity.NeedsReview {
				reviewCount++
			}
			mapped = append(mapped, activity)
		}
		nextOffset := offset + len(page.Data)
		complete := len(page.Data) == 0 || len(page.Data) < c.config.PageSize
		if page.Pagination.Total != nil {
			complete = nextOffset >= *page.Pagination.Total
		}
		if nextOffset <= offset && !complete {
			return fmt.Errorf("account %s activities pagination made no progress at offset %d", maskIdentifier(remoteAccountID), offset)
		}
		c.log.Info().Str("account", maskIdentifier(remoteAccountID)).Int("page", pageNumber).
			Int("offset", offset).Int("items", len(page.Data)).Int("mapped", len(mapped)).
			Int("needs_review", reviewCount).Bool("complete", complete).
			Msg("processed SnapTrade activity page")
		if err := sink(ctx, domainsync.ActivityPage{
			AccountID: localAccountID, Items: mapped, NextOffset: nextOffset,
			InitialSync: initial, Complete: complete, FirstTransactionDate: firstDate,
		}); err != nil {
			return fmt.Errorf("account %s persist activities offset %d: %w", maskIdentifier(remoteAccountID), offset, err)
		}
		if complete {
			c.log.Info().Str("account", maskIdentifier(remoteAccountID)).Msg("completed SnapTrade activity import")
			return nil
		}
		offset = nextOffset
		pageNumber++
	}
}

func (c *Client) maybeRefresh(ctx context.Context, authorizationID string) error {
	operations := []struct {
		enabled  bool
		key      string
		path     string
		category string
	}{
		{c.config.AllowManualRefresh, "holdings:", "/refresh", "manual_refresh"},
		{c.config.AllowTransactionSync, "transactions:", "/transactions/sync", "transaction_sync"},
	}
	var operationErrors []error
	for _, operation := range operations {
		if !operation.enabled || !c.refreshDue(operation.key+authorizationID) {
			continue
		}
		c.log.Info().Str("authorization", maskIdentifier(authorizationID)).Str("operation", operation.category).
			Msg("explicit SnapTrade refresh requested")
		path := "/api/v1/authorizations/" + url.PathEscape(authorizationID) + operation.path
		if err := c.api.post(ctx, path, operation.category, ""); err != nil {
			operationErrors = append(operationErrors, fmt.Errorf("SnapTrade %s for authorization %s: %w", operation.category, maskIdentifier(authorizationID), err))
			continue
		}
		c.refreshMu.Lock()
		c.refreshed[operation.key+authorizationID] = c.api.signer.clock.Now()
		c.refreshMu.Unlock()
	}
	return errors.Join(operationErrors...)
}

func (c *Client) refreshDue(key string) bool {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	last := c.refreshed[key]
	return last.IsZero() || c.api.signer.clock.Now().Sub(last) >= c.config.ManualRefreshCooldown
}

func fallbackConnection(account rawAccount) rawConnection {
	return rawConnection{
		ID:        account.BrokerageAuthorization,
		Brokerage: rawBrokerage{Slug: ibkrBrokerageSlug, Name: account.InstitutionName, DisplayName: account.InstitutionName, Enabled: true},
		Name:      account.InstitutionName,
	}
}

func wrapAccountError(accountID, operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("account %s %s: %w", maskIdentifier(accountID), operation, err)
}

var (
	_ domainsync.BrokerClient          = (*Client)(nil)
	_ domainsync.PagedActivityClient   = (*Client)(nil)
	_ domainsync.ScheduledBrokerClient = (*Client)(nil)
)
