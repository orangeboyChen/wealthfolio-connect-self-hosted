package ibkr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
)

const flexBaseSummaryCurrency = "BASE_SUMMARY"

func validateFlexSnapshotSections(report flexReport) error {
	missing := make([]string, 0, 3)
	if report.Elements["OpenPositions"] == 0 {
		missing = append(missing, "Open Positions")
	}
	if report.Elements["CashReport"] == 0 {
		missing = append(missing, "Cash Report")
	}
	if report.Elements["NetAssetValue"] == 0 && report.Elements["EquitySummaryInBase"] == 0 {
		missing = append(missing, "Net Asset Value (NAV) Summary in Base")
	}
	if len(missing) > 0 {
		return fmt.Errorf("ibkr flex: query is missing snapshot sections: %s", strings.Join(missing, ", "))
	}
	return nil
}

func mapFlexSnapshot(report flexReport, remoteAccountID, baseCurrency string, capturedAt time.Time) (domainsync.BrokerSnapshot, error) {
	baseCurrency = strings.ToUpper(strings.TrimSpace(baseCurrency))
	localAccountID := "ibkr-" + remoteAccountID
	securities := make(map[string]map[string]string)
	var accountInfo map[string]string
	var navTotal float64
	var navFound bool
	var navDate time.Time
	balancesByCurrency := make(map[string]brokerage.Balance)
	positions := make([]brokerage.Position, 0)
	optionPositions := make([]brokerage.OptionPosition, 0)

	for _, record := range report.Records {
		if !flexRecordMatchesAccount(record, remoteAccountID) {
			continue
		}
		switch strings.ToLower(record.Kind) {
		case flexKindSecurityInfo:
			if conid := flexAttr(record.Attrs, "conid", "conid1"); conid != "" {
				securities[conid] = record.Attrs
			}
		case "accountinformation":
			accountInfo = record.Attrs
		case "netassetvaluebyreportdateinbase", "equitysummarybyreportdateinbase":
			if value, ok := flexFloat(record.Attrs, "total"); ok {
				reportDate := parseFlexDate(flexAttr(record.Attrs, "reportdate", "date"))
				if !navFound || navDate.IsZero() || reportDate.After(navDate) {
					navTotal = value
					navFound = true
					navDate = reportDate
				}
			}
		case "cashreportcurrency":
			currency := strings.ToUpper(flexAttr(record.Attrs, "currency"))
			cash, ok := flexFloat(record.Attrs, "endingcash")
			if !ok || currency == "" {
				continue
			}
			if currency == flexBaseSummaryCurrency {
				continue
			}
			balancesByCurrency[currency] = brokerage.Balance{
				Currency: brokerage.Currency{Code: currency},
				Cash:     cash,
			}
		}
	}

	for _, record := range report.Records {
		if !strings.EqualFold(record.Kind, "OpenPosition") || !flexRecordMatchesAccount(record, remoteAccountID) {
			continue
		}
		units, ok := flexFloat(record.Attrs, "position", "quantity", "qty")
		if !ok || units == 0 {
			continue
		}
		attrs := record.Attrs
		if info := securities[flexAttr(record.Attrs, "conid", "conid1")]; info != nil {
			attrs = mergeFlexAttrs(info, record.Attrs)
		}
		currency := strings.ToUpper(flexAttr(attrs, "currency", "currencyprimary"))
		assetClass := strings.ToUpper(flexAttr(attrs, "assetcategory", "assetclass", "sectype"))
		markPrice, _ := flexFloat(record.Attrs, "markprice", "closeprice", "price")
		averagePrice, _ := flexFloat(record.Attrs, "costbasisprice", "openprice")
		if assetClass == flexAssetClassOption || assetClass == flexAssetClassFutureOption {
			option := flexOptionSymbol(attrs)
			if option == nil {
				return domainsync.BrokerSnapshot{}, fmt.Errorf("ibkr flex: open option position omitted contract fields for conid %s", maskFlexIdentifier(flexAttr(attrs, "conid")))
			}
			optionPositions = append(optionPositions, brokerage.OptionPosition{
				OptionSymbol: *option, Units: units, Price: markPrice,
				AveragePurchasePrice: averagePrice, Currency: brokerage.Currency{Code: currency},
			})
			continue
		}
		symbol := flexSymbol(attrs)
		if symbol == nil {
			return domainsync.BrokerSnapshot{}, fmt.Errorf("ibkr flex: open position omitted symbol fields for conid %s", maskFlexIdentifier(flexAttr(attrs, "conid")))
		}
		openPnL, _ := flexFloat(record.Attrs, "fifopnlunrealized", "unrealizedpnl")
		positions = append(positions, brokerage.Position{
			Symbol: *symbol, Units: units, Price: markPrice, OpenPnL: openPnL,
			AveragePurchasePrice: averagePrice, Currency: brokerage.Currency{Code: currency},
		})
	}

	if !navFound {
		return domainsync.BrokerSnapshot{}, errors.New("ibkr flex: NAV snapshot did not contain the Total field")
	}
	balances := make([]brokerage.Balance, 0, len(balancesByCurrency))
	for _, balance := range balancesByCurrency {
		balances = append(balances, balance)
	}
	accountType, rawAccountType := flexAccountType(accountInfo)
	connection := brokerage.Connection{
		ID: "ibkr-conn", AuthorizationID: "ibkr-auth", BrokerageName: flexInstitutionName,
		BrokerageSlug: "ibkr", DisplayName: flexInstitutionName, Name: "IBKR",
		LogoURL: flexLogoURL, SquareLogoURL: flexSquareLogoURL,
		Status: brokerage.ConnectionActive, UpdatedAt: capturedAt.UTC(),
	}
	account := brokerage.Account{
		ID: localAccountID, Name: "IBKR " + remoteAccountID, AccountNumber: remoteAccountID,
		Type: accountType, RawType: rawAccountType, Currency: baseCurrency,
		BalanceTotal: navTotal, BalanceCurrency: baseCurrency,
		BrokerageAuthorization: "ibkr-auth", InstitutionName: flexInstitutionName,
		SyncEnabled: true, IsPaper: strings.HasPrefix(remoteAccountID, "DU"), Status: "open",
		CreatedDate: capturedAt.UTC(), LastHoldingsSync: flexTimePointer(capturedAt.UTC()),
		InitialHoldingsDone: true,
	}
	holdings := brokerage.Holdings{
		AccountID: localAccountID, Balances: balances, Positions: positions,
		OptionPositions: optionPositions, CapturedAt: capturedAt.UTC(),
	}
	return domainsync.BrokerSnapshot{
		Connection: connection, Accounts: []brokerage.Account{account},
		Holdings: []brokerage.Holdings{holdings}, Activities: map[string][]brokerage.Activity{},
	}, nil
}

func flexRecordMatchesAccount(record flexRecord, expected string) bool {
	accountID := flexAttr(record.Attrs, "accountid", "account")
	return accountID == "" || accountID == expected
}

func flexAccountType(attrs map[string]string) (accountType brokerage.AccountType, rawType string) {
	rawType = strings.ToUpper(flexAttr(attrs, "accounttype", "type"))
	switch {
	case strings.Contains(rawType, "CASH"):
		return brokerage.AccountTypeCash, rawType
	case strings.Contains(rawType, "MARGIN"):
		return brokerage.AccountTypeMargin, rawType
	default:
		return brokerage.AccountTypeMargin, rawType
	}
}

func flexTimePointer(value time.Time) *time.Time {
	return &value
}
