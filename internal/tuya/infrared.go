package tuya

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Value encodings for the infrared AC API. The v2.0 endpoints take and return
// these as strings.
var (
	ModeCodes = map[string]string{"cool": "0", "heat": "1", "auto": "2", "fan": "3", "dry": "4"}
	WindCodes = map[string]string{"auto": "0", "low": "1", "mid": "2", "high": "3"}
)

func reverse(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

var (
	ModeNames = reverse(ModeCodes)
	WindNames = reverse(WindCodes)
)

type Remote struct {
	// The API returns ids as numbers or strings depending on the version.
	RemoteID   FlexString `json:"remote_id"`
	RemoteName string     `json:"remote_name"`
	CategoryID FlexString `json:"category_id"`
	BrandName  string     `json:"brand_name"`
}

// Remotes lists the virtual remotes added under an IR hub. AC remotes have
// category "5".
func (c *Client) Remotes(ctx context.Context, infraredID string) ([]Remote, error) {
	var out []Remote
	err := c.Call(ctx, http.MethodGet, "/v2.0/infrareds/"+url.PathEscape(infraredID)+"/remotes", nil, nil, &out)
	return out, err
}

// ACKeys returns the raw key list of an AC remote, for diagnostics.
func (c *Client) ACKeys(ctx context.Context, infraredID, remoteID string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.Call(ctx, http.MethodGet, "/v2.0/infrareds/"+url.PathEscape(infraredID)+"/remotes/"+url.PathEscape(remoteID)+"/keys", nil, nil, &out)
	return out, err
}

func acPath(infraredID, remoteID string) string {
	return "/v2.0/infrareds/" + url.PathEscape(infraredID) + "/air-conditioners/" + url.PathEscape(remoteID)
}

// ACCommand sends one AC setting, e.g. code "power" with value "0".
func (c *Client) ACCommand(ctx context.Context, infraredID, remoteID, code, value string) error {
	return c.Call(ctx, http.MethodPost, acPath(infraredID, remoteID)+"/command", nil,
		map[string]string{"code": code, "value": value}, nil)
}

type ACScene struct {
	Power string `json:"power"`
	Mode  string `json:"mode"`
	Temp  string `json:"temp"`
	Wind  string `json:"wind"`
}

// ACScenes sends power, mode, temperature and fan in a single IR frame.
func (c *Client) ACScenes(ctx context.Context, infraredID, remoteID string, s ACScene) error {
	return c.Call(ctx, http.MethodPost, acPath(infraredID, remoteID)+"/scenes/command", nil, s, nil)
}

// ACStatus is the last state the cloud sent to this remote. It cannot see
// changes made with the physical remote.
type ACStatus struct {
	Power FlexString `json:"power"`
	Mode  FlexString `json:"mode"`
	Temp  FlexString `json:"temp"`
	Wind  FlexString `json:"wind"`
}

// FlexString accepts a JSON string or number, since the v1.0 and v2.0
// endpoints disagree on the type.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = FlexString(n.String())
	return nil
}

func (c *Client) ACStatus(ctx context.Context, infraredID, remoteID string) (ACStatus, error) {
	var out ACStatus
	err := c.Call(ctx, http.MethodGet, "/v2.0/infrareds/"+url.PathEscape(infraredID)+"/remotes/"+url.PathEscape(remoteID)+"/ac/status", nil, nil, &out)
	return out, err
}
