// Package tuya is a minimal Tuya Cloud OpenAPI client: token handling, request
// signing, and the infrared AC endpoints.
package tuya

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoints maps a data center to its OpenAPI host. Brazilian accounts live
// in Western America ("us").
var Endpoints = map[string]string{
	"us":   "https://openapi.tuyaus.com",
	"us-e": "https://openapi-ueaz.tuyaus.com",
	"eu":   "https://openapi.tuyaeu.com",
	"eu-w": "https://openapi-weaz.tuyaeu.com",
	"cn":   "https://openapi.tuyacn.com",
	"in":   "https://openapi.tuyain.com",
	"sg":   "https://openapi-sg.iotbing.com",
}

const codeTokenInvalid = 1010

type Client struct {
	BaseURL string
	ID      string
	Secret  string
	HTTP    *http.Client
	Now     func() time.Time

	mu      sync.Mutex
	token   string
	refresh string
	expires time.Time
}

func NewClient(region, id, secret string) (*Client, error) {
	base, ok := Endpoints[region]
	if !ok {
		return nil, fmt.Errorf("unknown Tuya region %q", region)
	}
	return &Client{BaseURL: base, ID: id, Secret: secret, HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now}, nil
}

// APIError is a failure Tuya reported in the response body.
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tuya error %d: %s", e.Code, e.Msg)
}

type envelope struct {
	Success bool            `json:"success"`
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Result  json.RawMessage `json:"result"`
	T       int64           `json:"t"`
}

// Sign computes the Tuya HMAC-SHA256 signature for a request. pathQuery is
// the path plus the query string with keys sorted.
func Sign(secret, clientID, accessToken, t, method, pathQuery string, body []byte) string {
	sum := sha256.Sum256(body)
	stringToSign := method + "\n" + hex.EncodeToString(sum[:]) + "\n\n" + pathQuery
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(clientID + accessToken + t + stringToSign))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

func sortedPath(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+query.Get(k))
	}
	return path + "?" + strings.Join(parts, "&")
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, token string) (*envelope, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	pathQuery := sortedPath(path, query)
	t := strconv.FormatInt(c.Now().UnixMilli(), 10)
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+pathQuery, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("client_id", c.ID)
	req.Header.Set("sign", Sign(c.Secret, c.ID, token, t, method, pathQuery, payload))
	req.Header.Set("sign_method", "HMAC-SHA256")
	req.Header.Set("t", t)
	if token != "" {
		req.Header.Set("access_token", token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tuya http %d", resp.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("tuya: bad response: %w", err)
	}
	if !env.Success {
		return &env, &APIError{Code: env.Code, Msg: env.Msg}
	}
	return &env, nil
}

type tokenResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpireTime   int64  `json:"expire_time"`
	Expire       int64  `json:"expire"`
}

func (t tokenResult) lifetime() time.Duration {
	secs := t.ExpireTime
	if secs == 0 {
		secs = t.Expire
	}
	if secs <= 120 {
		secs = 120
	}
	return time.Duration(secs) * time.Second
}

// accessToken returns a valid token, fetching or refreshing it when needed.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.Now().Before(c.expires) {
		return c.token, nil
	}
	var env *envelope
	var err error
	if c.refresh != "" {
		env, err = c.do(ctx, http.MethodGet, "/v1.0/token/"+c.refresh, nil, nil, "")
	}
	if c.refresh == "" || err != nil {
		env, err = c.do(ctx, http.MethodGet, "/v1.0/token", url.Values{"grant_type": {"1"}}, nil, "")
	}
	if err != nil {
		c.token, c.refresh = "", ""
		return "", fmt.Errorf("tuya login: %w", err)
	}
	var tr tokenResult
	if err := json.Unmarshal(env.Result, &tr); err != nil || tr.AccessToken == "" {
		return "", fmt.Errorf("tuya login: no access token in response")
	}
	c.token, c.refresh = tr.AccessToken, tr.RefreshToken
	// Renew a minute early so a request never races the expiry.
	c.expires = c.Now().Add(tr.lifetime() - time.Minute)
	return c.token, nil
}

func (c *Client) invalidate() {
	c.mu.Lock()
	c.token, c.refresh = "", ""
	c.mu.Unlock()
}

// Call performs an authenticated request and decodes the result into out
// (which may be nil). It retries once with a fresh token when Tuya reports
// the token invalid.
func (c *Client) Call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		env, err := c.do(ctx, method, path, query, body, token)
		if apiErr, ok := err.(*APIError); ok && apiErr.Code == codeTokenInvalid && attempt == 0 {
			c.invalidate()
			continue
		}
		if err != nil {
			return err
		}
		if out != nil && len(env.Result) > 0 {
			return json.Unmarshal(env.Result, out)
		}
		return nil
	}
}
