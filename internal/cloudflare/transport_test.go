// -------------------------------------------------------------------------------
// HTTP Transport Seam Tests
//
// Author: Alex Freidah
//
// Exercises the httpDoer seam directly with a hand-rolled fake. The httptest
// servers used elsewhere can return any status code but cannot make Do itself
// fail (a dialled-but-dropped connection, DNS failure, etc.); injecting a fake
// doer covers that transport-error branch and confirms the injected transport
// is the one actually used.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Compile-time assertion: the real *http.Client satisfies the seam.
var _ httpDoer = (*http.Client)(nil)

// fakeDoer is a stand-in HTTP transport that returns a canned response or error
// and records how it was called.
type fakeDoer struct {
	resp    *http.Response
	err     error
	calls   int
	lastReq *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.calls++
	f.lastReq = req
	return f.resp, f.err
}

// jsonResponse builds a 200 response carrying the given body.
func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestClient_TransportError(t *testing.T) {
	doer := &fakeDoer{err: errors.New("connection refused")}
	c := &Client{
		apiToken:   "token",
		endpoint:   "http://cf.test/graphql",
		httpClient: doer,
	}

	_, err := c.QueryFirewallEvents(context.Background(), "zone1", time.Now().Add(-time.Hour), time.Now())
	if err == nil {
		t.Fatal("expected error when transport fails")
	}
	if doer.calls != 1 {
		t.Errorf("transport called %d times, want 1 (transport errors are not retried)", doer.calls)
	}
}

func TestClient_InjectedDoerIsUsed(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{
				"zones": []map[string]any{
					{"firewallEventsAdaptive": []FirewallEvent{
						{Action: "block", ClientIP: "1.2.3.4", Datetime: "2026-03-14T12:00:00Z"},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	doer := &fakeDoer{resp: jsonResponse(string(body))}
	c := &Client{
		apiToken:   "token",
		endpoint:   "http://cf.test/graphql",
		httpClient: doer,
	}

	events, err := c.QueryFirewallEvents(context.Background(), "zone1", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryFirewallEvents() error = %v", err)
	}
	if doer.calls != 1 {
		t.Fatalf("transport called %d times, want 1", doer.calls)
	}
	if len(events) != 1 || events[0].Action != "block" {
		t.Errorf("events = %+v, want one block event", events)
	}
	// The injected transport must carry the bearer token the client sets.
	if got := doer.lastReq.Header.Get("Authorization"); got != "Bearer token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer token")
	}
}
