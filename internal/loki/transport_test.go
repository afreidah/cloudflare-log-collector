// -------------------------------------------------------------------------------
// HTTP Transport Seam Tests
//
// Author: Alex Freidah
//
// Exercises the httpDoer seam directly with a hand-rolled fake. The httptest
// servers used elsewhere cannot make Do itself fail; injecting a fake doer
// covers that transport-error branch and confirms the injected transport is the
// one actually used, carrying the multi-tenant org header.
// -------------------------------------------------------------------------------

package loki

import (
	"context"
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
	status  int
	err     error
	calls   int
	lastReq *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func testEntries() []Entry {
	return []Entry{NewEntry(time.Now().UTC(), `{"msg":"hello"}`)}
}

func TestClient_TransportError(t *testing.T) {
	doer := &fakeDoer{err: errors.New("connection refused")}
	c := &Client{endpoint: "http://loki.test", tenantID: "tenant1", httpClient: doer}

	err := c.Push(context.Background(), map[string]string{"job": "cloudflare"}, testEntries())
	if err == nil {
		t.Fatal("expected error when transport fails")
	}
	if doer.calls != 1 {
		t.Errorf("transport called %d times, want 1 (transport errors are not retried)", doer.calls)
	}
}

func TestClient_InjectedDoerIsUsed(t *testing.T) {
	doer := &fakeDoer{status: http.StatusNoContent}
	c := &Client{endpoint: "http://loki.test", tenantID: "tenant1", httpClient: doer}

	err := c.Push(context.Background(), map[string]string{"job": "cloudflare"}, testEntries())
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if doer.calls != 1 {
		t.Fatalf("transport called %d times, want 1", doer.calls)
	}
	// The injected transport must carry the tenant org header.
	if got := doer.lastReq.Header.Get("X-Scope-OrgID"); got != "tenant1" {
		t.Errorf("X-Scope-OrgID header = %q, want %q", got, "tenant1")
	}
}
