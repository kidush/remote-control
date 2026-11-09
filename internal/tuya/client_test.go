package tuya

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testID     = "client-id"
	testSecret = "secret"
)

// fakeTuya verifies every signature independently of the client's helper and
// records the requests it accepted.
type fakeTuya struct {
	t        *testing.T
	mu       sync.Mutex
	tokens   int
	reject   int // reject this many authenticated calls with code 1010
	requests []string
	bodies   []string
}

func (f *fakeTuya) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	pathQuery := r.URL.Path
	if q := r.URL.Query(); len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+q.Get(k))
		}
		pathQuery += "?" + strings.Join(parts, "&")
	}
	token := r.Header.Get("access_token")
	isToken := strings.HasPrefix(r.URL.Path, "/v1.0/token")
	if isToken && token != "" {
		f.t.Errorf("token request carried an access token")
	}
	want := Sign(testSecret, testID, token, r.Header.Get("t"), r.Method, pathQuery, body)
	if r.Header.Get("sign") != want || r.Header.Get("client_id") != testID || r.Header.Get("sign_method") != "HMAC-SHA256" {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "code": 1004, "msg": "sign invalid"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if isToken {
		f.tokens++
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{
			"access_token": "tok" + string(rune('0'+f.tokens)), "refresh_token": "ref", "expire_time": 7200,
		}})
		return
	}
	if f.reject > 0 {
		f.reject--
		json.NewEncoder(w).Encode(map[string]any{"success": false, "code": 1010, "msg": "token invalid"})
		return
	}
	f.requests = append(f.requests, r.Method+" "+pathQuery+" "+token)
	f.bodies = append(f.bodies, string(body))
	switch {
	case strings.HasSuffix(r.URL.Path, "/ac/status"):
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"power": "1", "mode": "0", "temp": "23", "wind": 2}})
	default:
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": true})
	}
}

func newTestClient(t *testing.T, f *fakeTuya) *Client {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, ID: testID, Secret: testSecret, HTTP: srv.Client(), Now: time.Now}
}

// Vectors computed independently with Python's hmac module, following the
// Tuya signing spec: client_id + access_token + t + stringToSign.
func TestSignKnownVectors(t *testing.T) {
	got := Sign("secret", "client-id", "", "1700000000000", "GET", "/v1.0/token?grant_type=1", nil)
	if want := "006E057FAC81BD9DA6A6197D53E3D8F17C84B9F7E6FBCCF24D6704CECD0DBC60"; got != want {
		t.Fatalf("token request sign = %s, want %s", got, want)
	}
	got = Sign("secret", "client-id", "tok", "1700000000000", "POST",
		"/v2.0/infrareds/ir1/air-conditioners/rm1/command", []byte(`{"code":"power","value":"0"}`))
	if want := "02EF161616F21F4E35EB99E9AE3AB89E30B2114EDE7FE07642DA3C81160EB38A"; got != want {
		t.Fatalf("command sign = %s, want %s", got, want)
	}
}

func TestSceneCommandSignedAndTokenCached(t *testing.T) {
	f := &fakeTuya{t: t}
	c := newTestClient(t, f)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := c.ACScenes(ctx, "ir1", "rm1", ACScene{Power: "1", Mode: "0", Temp: "22.0", Wind: "3"}); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokens != 1 {
		t.Fatalf("fetched %d tokens, want 1 (cached)", f.tokens)
	}
	if got := f.requests[0]; got != "POST /v2.0/infrareds/ir1/air-conditioners/rm1/scenes/command tok1" {
		t.Fatalf("request = %q", got)
	}
	if got := f.bodies[0]; got != `{"power":"1","mode":"0","temp":"22.0","wind":"3"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestRetriesOnceOnInvalidToken(t *testing.T) {
	f := &fakeTuya{t: t, reject: 1}
	c := newTestClient(t, f)
	if err := c.ACCommand(context.Background(), "ir1", "rm1", "power", "0"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || !strings.HasSuffix(f.requests[0], " tok2") {
		t.Fatalf("requests = %v, want one retry with a new token", f.requests)
	}
}

func TestGivesUpAfterSecondInvalidToken(t *testing.T) {
	f := &fakeTuya{t: t, reject: 2}
	c := newTestClient(t, f)
	err := c.ACCommand(context.Background(), "ir1", "rm1", "power", "0")
	if apiErr, ok := err.(*APIError); !ok || apiErr.Code != codeTokenInvalid {
		t.Fatalf("err = %v, want APIError 1010", err)
	}
}

func TestStatusAcceptsStringsAndNumbers(t *testing.T) {
	c := newTestClient(t, &fakeTuya{t: t})
	st, err := c.ACStatus(context.Background(), "ir1", "rm1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Power != "1" || st.Mode != "0" || st.Temp != "23" || st.Wind != "2" {
		t.Fatalf("status = %+v", st)
	}
}

func TestBadCredentialsSurface(t *testing.T) {
	f := &fakeTuya{t: t}
	c := newTestClient(t, f)
	c.Secret = "wrong"
	err := c.ACCommand(context.Background(), "ir1", "rm1", "power", "0")
	if err == nil || !strings.Contains(err.Error(), "tuya login") {
		t.Fatalf("err = %v, want a login failure", err)
	}
}

func TestRemotesAcceptNumericIDs(t *testing.T) {
	var rs []Remote
	raw := `[{"remote_id":"r1","remote_name":"Komeco","category_id":5,"brand_name":"Komeco"},{"remote_id":123,"category_id":"2"}]`
	if err := json.Unmarshal([]byte(raw), &rs); err != nil {
		t.Fatal(err)
	}
	if rs[0].CategoryID != "5" || rs[1].RemoteID != "123" || rs[1].CategoryID != "2" {
		t.Fatalf("remotes = %+v", rs)
	}
}
