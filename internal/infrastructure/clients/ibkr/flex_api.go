package ibkr

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const flexMaxResponseBytes = 64 << 20

// HTTPDoer is the subset of http.Client used by the Flex Web Service client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type flexAPI struct {
	baseURL     *url.URL
	doer        HTTPDoer
	token       string
	queryID     string
	pollEvery   time.Duration
	pollTimeout time.Duration
	sendEvery   time.Duration
	sendMu      sync.Mutex
	lastSend    time.Time
}

type flexServiceResponse struct {
	XMLName       xml.Name
	Status        string `xml:"Status"`
	ReferenceCode string `xml:"ReferenceCode"`
	URL           string `xml:"url"`
	LegacyURL     string `xml:"Url"`
	ErrorCode     string `xml:"ErrorCode"`
	ErrorMessage  string `xml:"ErrorMessage"`
}

type flexServiceError struct {
	Code    string
	Message string
}

func (e *flexServiceError) Error() string {
	return fmt.Sprintf("ibkr flex: service error %s: %s", e.Code, e.Message)
}

type flexRecord struct {
	Kind  string
	Attrs map[string]string
}

type flexReport struct {
	Accounts           map[string]struct{}
	Elements           map[string]int
	AttributedElements map[string]int
	Records            []flexRecord
}

func newFlexAPI(rawBaseURL, token, queryID string, doer HTTPDoer, requestTimeout, pollEvery, pollTimeout time.Duration) (*flexAPI, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, errors.New("ibkr flex: invalid base URL")
	}
	if doer == nil {
		doer = &http.Client{Timeout: requestTimeout}
	}
	return &flexAPI{
		baseURL: baseURL, doer: doer, token: token, queryID: queryID,
		pollEvery: pollEvery, pollTimeout: pollTimeout, sendEvery: 6 * time.Second,
	}, nil
}

func (a *flexAPI) fetch(ctx context.Context, from, to time.Time) (flexReport, error) {
	if err := a.paceSend(ctx); err != nil {
		return flexReport{}, err
	}
	sendURL := a.endpoint("SendRequest")
	query := sendURL.Query()
	query.Set("t", a.token)
	query.Set("q", a.queryID)
	query.Set("v", "3")
	query.Set("fd", from.UTC().Format("20060102"))
	query.Set("td", to.UTC().Format("20060102"))
	sendURL.RawQuery = query.Encode()

	body, err := a.get(ctx, sendURL)
	if err != nil {
		return flexReport{}, fmt.Errorf("request statement: %w", err)
	}
	var response flexServiceResponse
	if decodeErr := xml.Unmarshal(body, &response); decodeErr != nil {
		return flexReport{}, fmt.Errorf("decode request response: %w", decodeErr)
	}
	if !strings.EqualFold(strings.TrimSpace(response.Status), "Success") {
		return flexReport{}, redactFlexError(flexResponseError(response), a.token)
	}
	if strings.TrimSpace(response.ReferenceCode) == "" {
		return flexReport{}, errors.New("ibkr flex: response omitted reference code")
	}

	responseURL := response.URL
	if responseURL == "" {
		responseURL = response.LegacyURL
	}
	statementURL, err := a.statementURL(responseURL)
	if err != nil {
		return flexReport{}, err
	}
	query = statementURL.Query()
	query.Set("t", a.token)
	query.Set("q", response.ReferenceCode)
	query.Set("v", "3")
	statementURL.RawQuery = query.Encode()

	deadline := time.Now().Add(a.pollTimeout)
	for {
		body, err = a.get(ctx, statementURL)
		if err != nil {
			return flexReport{}, fmt.Errorf("download statement: %w", err)
		}
		if looksLikeFlexReport(body) {
			report, decodeErr := decodeFlexReport(bytes.NewReader(body))
			if decodeErr != nil {
				return flexReport{}, fmt.Errorf("decode statement: %w", decodeErr)
			}
			return report, nil
		}

		response = flexServiceResponse{}
		if decodeErr := xml.Unmarshal(body, &response); decodeErr != nil {
			return flexReport{}, fmt.Errorf("decode statement response: %w", decodeErr)
		}
		if !isFlexPending(response) {
			return flexReport{}, redactFlexError(flexResponseError(response), a.token)
		}
		if !time.Now().Before(deadline) {
			return flexReport{}, fmt.Errorf("ibkr flex: statement was not ready within %s", a.pollTimeout)
		}
		if err := waitContext(ctx, a.pollEvery); err != nil {
			return flexReport{}, err
		}
	}
}

func (a *flexAPI) paceSend(ctx context.Context) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	if !a.lastSend.IsZero() {
		remaining := a.sendEvery - time.Since(a.lastSend)
		if remaining > 0 {
			if err := waitContext(ctx, remaining); err != nil {
				return err
			}
		}
	}
	a.lastSend = time.Now()
	return nil
}

func (a *flexAPI) endpoint(operation string) url.URL {
	copyURL := *a.baseURL
	copyURL.Path = strings.TrimRight(copyURL.Path, "/") + "/" + operation
	return copyURL
}

func (a *flexAPI) statementURL(raw string) (url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return a.endpoint("GetStatement"), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return url.URL{}, errors.New("ibkr flex: response contained an invalid statement URL")
	}
	localHTTP := parsed.Scheme == "http" && a.baseURL.Scheme == "http" &&
		strings.EqualFold(parsed.Host, a.baseURL.Host)
	if parsed.Scheme != "https" && !localHTTP {
		return url.URL{}, errors.New("ibkr flex: response contained an unsafe statement URL")
	}
	baseHost := strings.ToLower(a.baseURL.Hostname())
	statementHost := strings.ToLower(parsed.Hostname())
	if statementHost != baseHost && !strings.HasSuffix(statementHost, ".interactivebrokers.com") {
		return url.URL{}, errors.New("ibkr flex: response statement host is not trusted")
	}
	return *parsed, nil
}

func (a *flexAPI) get(ctx context.Context, target url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build HTTP request: %w", err)
	}
	req.Header.Set("Accept", "application/xml, text/xml")
	req.Header.Set("User-Agent", "wealthfolio-connect-self-hosted/ibkr-flex")
	response, err := a.doer.Do(req)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, errors.New(redactFlexSecret(err.Error(), a.token))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if _, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); copyErr != nil {
			return nil, fmt.Errorf("discard HTTP error response: %w", copyErr)
		}
		return nil, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, flexMaxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read HTTP response: %w", err)
	}
	if len(body) > flexMaxResponseBytes {
		return nil, fmt.Errorf("HTTP response exceeds %d bytes", flexMaxResponseBytes)
	}
	return body, nil
}

func flexResponseError(response flexServiceResponse) error {
	code := strings.TrimSpace(response.ErrorCode)
	message := strings.TrimSpace(response.ErrorMessage)
	if code == "" && message == "" {
		return errors.New("ibkr flex: unsuccessful response without an error message")
	}
	return &flexServiceError{Code: code, Message: message}
}

func isFlexStatementUnavailable(err error) bool {
	var serviceErr *flexServiceError
	return errors.As(err, &serviceErr) && serviceErr.Code == "1003"
}

func isFlexPending(response flexServiceResponse) bool {
	switch strings.TrimSpace(response.ErrorCode) {
	case "1001", "1004", "1005", "1006", "1007", "1008", "1009", "1018", "1019", "1021":
		return true
	default:
		message := strings.ToLower(response.ErrorMessage)
		return strings.Contains(message, "in progress") || strings.Contains(message, "not ready")
	}
}

func looksLikeFlexReport(body []byte) bool {
	return bytes.Contains(body, []byte("<FlexQueryResponse")) || bytes.Contains(body, []byte("<FlexStatements"))
}

func decodeFlexReport(reader io.Reader) (flexReport, error) {
	decoder := xml.NewDecoder(reader)
	report := flexReport{
		Accounts: make(map[string]struct{}), Elements: make(map[string]int),
		AttributedElements: make(map[string]int),
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return report, nil
		}
		if err != nil {
			return flexReport{}, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := make(map[string]string, len(start.Attr))
		report.Elements[start.Name.Local]++
		for _, attr := range start.Attr {
			attrs[strings.ToLower(attr.Name.Local)] = strings.TrimSpace(attr.Value)
		}
		if len(attrs) > 0 {
			report.AttributedElements[start.Name.Local]++
		}
		if accountID := attrs["accountid"]; accountID != "" {
			report.Accounts[accountID] = struct{}{}
		}
		if len(attrs) > 0 && isFlexActivityRecord(start.Name.Local) {
			report.Records = append(report.Records, flexRecord{Kind: start.Name.Local, Attrs: attrs})
		}
	}
}

func isFlexActivityRecord(kind string) bool {
	switch strings.ToLower(kind) {
	case "trade", "cashtransaction", "corporateaction", "transfer", "tradetransfer", "optioneae",
		"conversionrate", flexKindSecurityInfo, "transactiontax", "brokeragefee", "clientfee",
		"transactionfee", "slbactivity", "debitcardactivity", "accountinformation",
		"openposition", "cashreportcurrency", "netassetvaluebyreportdateinbase",
		"equitysummarybyreportdateinbase":
		return true
	default:
		return false
	}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func redactFlexSecret(value, token string) string {
	if token == "" {
		return value
	}
	return strings.ReplaceAll(value, token, "[REDACTED]")
}

func redactFlexError(err error, token string) error {
	var serviceErr *flexServiceError
	if errors.As(err, &serviceErr) {
		return &flexServiceError{
			Code:    serviceErr.Code,
			Message: redactFlexSecret(serviceErr.Message, token),
		}
	}
	return errors.New(redactFlexSecret(err.Error(), token))
}
