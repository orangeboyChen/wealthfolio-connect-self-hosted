package snaptrade

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

type clock interface {
	Now() time.Time
}

type realClock struct{}

// Now returns the current local wall-clock time.
func (realClock) Now() time.Time { return time.Now() }

type signer struct {
	auth  config.SnapTradeConfig
	clock clock
}

func (s signer) sign(path string, values url.Values, body any) (encodedQuery, signature string, err error) {
	query := cloneValues(values)
	query.Set("clientId", s.auth.ClientID)
	query.Set("timestamp", strconv.FormatInt(s.clock.Now().Unix(), 10))
	if s.auth.AuthMode == authModeCommercial {
		query.Set("userId", s.auth.UserID)
		query.Set("userSecret", s.auth.UserSecret)
	}
	rawQuery := query.Encode()
	canonical, err := canonicalSignaturePayload(path, rawQuery, body)
	if err != nil {
		return "", "", err
	}
	mac := hmac.New(sha256.New, []byte(s.auth.ConsumerKey))
	_, _ = mac.Write(canonical)
	signature = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return rawQuery, signature, nil
}

func canonicalSignaturePayload(path, rawQuery string, body any) ([]byte, error) {
	payload := map[string]any{
		"content": body,
		"path":    path,
		"query":   rawQuery,
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(payload)
	if err != nil {
		return nil, fmt.Errorf("canonicalize signature payload: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in)+4)
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}
