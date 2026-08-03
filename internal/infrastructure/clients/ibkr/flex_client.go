package ibkr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

const (
	maxFlexWindowDays           = 365
	maxFlexSnapshotLookbackDays = 10
)

// FlexClient downloads end-of-day account, holdings, and activity data from
// the IBKR Flex Web Service without requiring IB Gateway or TWS.
type FlexClient struct {
	api       *flexAPI
	config    config.IBKRFlexConfig
	accountID string
	log       zerolog.Logger
	now       func() time.Time

	reportDayMu sync.RWMutex
	reportDay   time.Time
}

// NewFlex constructs the standalone IBKR Flex client. The Flex token is held
// only in memory and is never added to logs or domain records.
func NewFlex(accountID string, cfg config.IBKRFlexConfig, log zerolog.Logger, doer HTTPDoer) (*FlexClient, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, errors.New("ibkr flex: account ID is required")
	}
	api, err := newFlexAPI(
		cfg.BaseURL, cfg.Token, cfg.QueryID, doer,
		cfg.RequestTimeout, cfg.PollInterval, cfg.PollTimeout,
	)
	if err != nil {
		return nil, err
	}
	client := &FlexClient{
		api: api, config: cfg, accountID: strings.TrimSpace(accountID),
		log: log, now: func() time.Time { return time.Now().UTC() },
	}
	log.Info().Str("account", maskFlexIdentifier(accountID)).
		Time("history_start", cfg.HistoryStartDate).Dur("sync_interval", cfg.SyncInterval).
		Msg("IBKR Flex history client configured")
	return client, nil
}

// ID returns the stable direct-IBKR integration slug.
func (c *FlexClient) ID() string { return "ibkr" }

// SyncInterval returns the configured end-of-day Flex refresh cadence.
func (c *FlexClient) SyncInterval() time.Duration { return c.config.SyncInterval }

// Fetch satisfies BrokerClient and retrieves the latest closed-day snapshot.
func (c *FlexClient) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	return c.FetchAccountSnapshot(ctx)
}

// FetchAccountSnapshot obtains the latest closed-day account, cash, NAV, and
// open positions directly from Flex. Activity history is streamed separately.
func (c *FlexClient) FetchAccountSnapshot(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	today := beginningOfUTCDay(c.now())
	var lastUnavailable error
	for daysBack := 1; daysBack <= maxFlexSnapshotLookbackDays; daysBack++ {
		reportDay := today.AddDate(0, 0, -daysBack)
		if reportDay.Weekday() == time.Saturday || reportDay.Weekday() == time.Sunday {
			continue
		}

		report, err := c.api.fetch(ctx, reportDay, reportDay)
		if err != nil {
			if isFlexStatementUnavailable(err) {
				lastUnavailable = err
				c.log.Debug().Str("account", maskFlexIdentifier(c.accountID)).
					Time("report_day", reportDay).
					Msg("IBKR Flex snapshot is unavailable; trying the previous business day")
				continue
			}
			return domainsync.BrokerSnapshot{}, fmt.Errorf("latest closed-day Flex snapshot: %w", err)
		}
		if accountErr := validateFlexAccount(report, c.accountID); accountErr != nil {
			return domainsync.BrokerSnapshot{}, accountErr
		}
		if sectionErr := validateFlexSnapshotSections(report); sectionErr != nil {
			return domainsync.BrokerSnapshot{}, sectionErr
		}
		snapshot, err := mapFlexSnapshot(report, c.accountID, c.config.BaseCurrency, c.now())
		if err != nil {
			return domainsync.BrokerSnapshot{}, err
		}
		c.setLatestReportDay(reportDay)
		c.log.Info().Str("account", maskFlexIdentifier(c.accountID)).
			Time("report_day", reportDay).
			Int("balances", len(snapshot.Holdings[0].Balances)).
			Int("positions", len(snapshot.Holdings[0].Positions)).
			Int("option_positions", len(snapshot.Holdings[0].OptionPositions)).
			Msg("processed latest available IBKR Flex snapshot")
		return snapshot, nil
	}

	if lastUnavailable == nil {
		lastUnavailable = errors.New("no business day was eligible")
	}
	return domainsync.BrokerSnapshot{}, fmt.Errorf(
		"latest available Flex snapshot was not found within %d days: %w",
		maxFlexSnapshotLookbackDays, lastUnavailable,
	)
}

// StreamActivities downloads the statement in inclusive windows of at most
// 365 days, persisting every window before requesting the next one.
func (c *FlexClient) StreamActivities(ctx context.Context, states []domainsync.ActivitySyncState, sink domainsync.ActivitySink) error {
	if sink == nil {
		return errors.New("ibkr flex: activity sink is nil")
	}
	localAccountID := "ibkr-" + c.accountID
	var matchingState *domainsync.ActivitySyncState
	for index := range states {
		if states[index].AccountID == localAccountID {
			state := states[index]
			matchingState = &state
			break
		}
	}
	if matchingState == nil {
		return fmt.Errorf("ibkr flex: configured account %s was not returned by the Flex snapshot", maskFlexIdentifier(c.accountID))
	}

	state := *matchingState
	initial := !state.InitialSyncCompleted || state.FirstTransactionDate == nil
	if !initial && state.LastSuccessfulSync != nil && c.now().Sub(*state.LastSuccessfulSync) < c.config.SyncInterval {
		c.log.Debug().Str("account", maskFlexIdentifier(c.accountID)).
			Time("last_sync", *state.LastSuccessfulSync).Dur("sync_interval", c.config.SyncInterval).
			Msg("IBKR Flex activity history is not due; closed-day holdings were refreshed")
		return nil
	}
	start := beginningOfUTCDay(c.config.HistoryStartDate)
	if initial {
		start = start.AddDate(0, 0, state.NextOffset)
	} else if state.LastSuccessfulSync != nil {
		start = beginningOfUTCDay(state.LastSuccessfulSync.Add(-c.config.IncrementalOverlap))
	}
	historyStart := beginningOfUTCDay(c.config.HistoryStartDate)
	if start.Before(historyStart) {
		start = historyStart
	}
	// Reuse the exact statement day accepted by IBKR for the holdings
	// snapshot. A separate "yesterday" calculation can land on a weekend or
	// holiday and make an otherwise successful initial backfill fail at its
	// final window with Flex error 1003.
	endLimit := c.latestReportDay()
	if start.After(endLimit) {
		start = endLimit
	}

	mode := "incremental"
	if initial {
		mode = "initial"
	}
	c.log.Info().Str("account", maskFlexIdentifier(c.accountID)).Str("sync_mode", mode).
		Time("from", start).Time("through", endLimit).Int("offset_days", state.NextOffset).
		Msg("starting IBKR Flex activity import")

	firstSeen := state.FirstTransactionDate
	window := 0
	for from := start; !from.After(endLimit); {
		if err := ctx.Err(); err != nil {
			return err
		}
		to := from.AddDate(0, 0, maxFlexWindowDays-1)
		if to.After(endLimit) {
			to = endLimit
		}
		report, err := c.api.fetch(ctx, from, to)
		if err != nil {
			return fmt.Errorf("account %s Flex window %s to %s: %w",
				maskFlexIdentifier(c.accountID), from.Format("2006-01-02"), to.Format("2006-01-02"), err)
		}
		if accountErr := validateFlexAccount(report, c.accountID); accountErr != nil {
			return accountErr
		}
		mapped := mapFlexReport(report, c.accountID, c.config.BaseCurrency)
		for index := range mapped.Activities {
			candidate := mapped.Activities[index].TradeDate
			if firstSeen == nil || candidate.Before(*firstSeen) {
				value := candidate
				firstSeen = &value
			}
		}
		complete := !to.Before(endLimit)
		nextOffset := 0
		if initial && !complete {
			nextOffset = daysBetween(historyStart, to.AddDate(0, 0, 1))
		}
		checkpointDate := firstSeen
		if initial && complete && checkpointDate == nil {
			// The schema has no separate "coverage started at" marker. Retaining
			// this date prevents an empty account from repeating a full backfill.
			value := historyStart
			checkpointDate = &value
		}
		c.log.Info().Str("account", maskFlexIdentifier(c.accountID)).Int("window", window+1).
			Time("from", from).Time("to", to).Int("records", len(report.Records)).
			Int("mapped", len(mapped.Activities)).Int("skipped", mapped.Skipped).
			Int("needs_review", mapped.Review).Bool("complete", complete).
			Msg("processed IBKR Flex statement window")
		if err := sink(ctx, domainsync.ActivityPage{
			AccountID: localAccountID, Items: mapped.Activities, NextOffset: nextOffset,
			InitialSync: initial, Complete: complete, FirstTransactionDate: checkpointDate,
		}); err != nil {
			return fmt.Errorf("account %s persist Flex window ending %s: %w",
				maskFlexIdentifier(c.accountID), to.Format("2006-01-02"), err)
		}
		if complete {
			c.log.Info().Str("account", maskFlexIdentifier(c.accountID)).Str("sync_mode", mode).
				Msg("completed IBKR Flex activity import")
			return nil
		}
		from = to.AddDate(0, 0, 1)
		window++
	}
	return nil
}

func beginningOfUTCDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func (c *FlexClient) setLatestReportDay(value time.Time) {
	c.reportDayMu.Lock()
	c.reportDay = beginningOfUTCDay(value)
	c.reportDayMu.Unlock()
}

func (c *FlexClient) latestReportDay() time.Time {
	c.reportDayMu.RLock()
	value := c.reportDay
	c.reportDayMu.RUnlock()
	if !value.IsZero() {
		return value
	}

	// StreamActivities is normally called immediately after a successful
	// snapshot. Keep a deterministic business-day fallback for direct calls
	// in tests and for any future caller that invokes the paging port alone.
	value = beginningOfUTCDay(c.now()).AddDate(0, 0, -1)
	for value.Weekday() == time.Saturday || value.Weekday() == time.Sunday {
		value = value.AddDate(0, 0, -1)
	}
	return value
}

func daysBetween(from, to time.Time) int {
	from = beginningOfUTCDay(from)
	to = beginningOfUTCDay(to)
	return int(to.Sub(from) / (24 * time.Hour))
}

var (
	_ domainsync.BrokerClient          = (*FlexClient)(nil)
	_ domainsync.PagedActivityClient   = (*FlexClient)(nil)
	_ domainsync.ScheduledBrokerClient = (*FlexClient)(nil)
	_ HTTPDoer                         = (*http.Client)(nil)
)
