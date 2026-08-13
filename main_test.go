package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type SegmentServer int

const (
	CDN SegmentServer = iota
	TrackingAPI
)

func TestSegmentReverseProxy(t *testing.T) {
	cases := []struct {
		url            string
		expectedServer SegmentServer
	}{
		{"/v1/projects", CDN},
		{"/analytics.js/v1", CDN},
		{"/v1/import", TrackingAPI},
		{"/v1/pixel", TrackingAPI},
	}
	for _, c := range cases {
		cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.expectedServer == CDN {
				fmt.Fprintln(w, "Hello, client")
			} else {
				t.Errorf("CDN unexpected request: %v\n", r.URL)
			}
		}))

		trackingAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.expectedServer == TrackingAPI {
				fmt.Fprintln(w, "Hello, client")
			} else {
				t.Errorf("Tracking API unexpected request: %v\n", r.URL)
			}
		}))

		proxy := httptest.NewServer(NewSegmentReverseProxy(mustParseUrl(cdn.URL), mustParseUrl(trackingAPI.URL)))

		_, err := http.Get(proxy.URL + c.url)
		if err != nil {
			t.Fatal(err)
		}

		cdn.Close()
		trackingAPI.Close()
	}
}

func TestBufferRequestBody(t *testing.T) {
	payload := `{"event":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/t", strings.NewReader(payload))

	bufferRequestBody(req)

	if req.GetBody == nil {
		t.Fatal("expected GetBody to be set so the transport can replay the request")
	}
	if req.ContentLength != int64(len(payload)) {
		t.Errorf("expected ContentLength %v, got %v", len(payload), req.ContentLength)
	}

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != payload {
		t.Errorf("expected body %q, got %q", payload, string(body))
	}

	replay, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := ioutil.ReadAll(replay)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != payload {
		t.Errorf("expected replayed body %q, got %q", payload, string(replayed))
	}
}

func TestBufferRequestBodyLeavesLargeBodyStreaming(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), maxBufferedBody+1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/t", bytes.NewReader(payload))
	contentLength := req.ContentLength

	bufferRequestBody(req)

	if req.GetBody != nil {
		t.Error("expected GetBody to stay nil so an oversized body is not held in memory")
	}
	if req.ContentLength != contentLength {
		t.Errorf("expected ContentLength to stay %v, got %v", contentLength, req.ContentLength)
	}

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("expected the oversized body to be readable in full: got %v bytes, want %v", len(body), len(payload))
	}
}

func TestBufferRequestBodySkipsRequestsItCannotHelp(t *testing.T) {
	withoutBody := httptest.NewRequest(http.MethodGet, "/v1/projects", nil)
	withoutBody.Body = nil
	bufferRequestBody(withoutBody)
	if withoutBody.GetBody != nil {
		t.Error("expected GetBody to stay nil for a request without a body")
	}

	replayable := httptest.NewRequest(http.MethodPost, "/v1/t", strings.NewReader("body"))
	replayable.GetBody = func() (io.ReadCloser, error) {
		return ioutil.NopCloser(strings.NewReader("sentinel")), nil
	}
	bufferRequestBody(replayable)
	replay, err := replayable.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := ioutil.ReadAll(replay)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != "sentinel" {
		t.Errorf("expected an existing GetBody to be left alone, got %q", string(replayed))
	}
}

// TestDirectorBuffersRequestBody guards the wiring: bufferRequestBody is useless
// unless the director actually calls it before the transport sends the request.
func TestDirectorBuffersRequestBody(t *testing.T) {
	handler := NewSegmentReverseProxy(mustParseUrl("https://cdn.example.com"), mustParseUrl("https://api.example.com"))
	proxy, ok := handler.(*httputil.ReverseProxy)
	if !ok {
		t.Fatalf("expected a *httputil.ReverseProxy, got %T", handler)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/t", strings.NewReader(`{"event":"test"}`))
	proxy.Director(req)

	if req.GetBody == nil {
		t.Fatal("director must buffer the body so the transport can replay it after a GOAWAY")
	}
}

func TestProxyForwardsPostBody(t *testing.T) {
	payload := `{"event":"test"}`

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("CDN unexpected request: %v\n", r.URL)
	}))
	defer cdn.Close()

	var hits int32
	trackingAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Tracking API failed to read body: %v", err)
			return
		}
		if string(body) != payload {
			t.Errorf("expected forwarded body %q, got %q", payload, string(body))
		}
	}))
	defer trackingAPI.Close()

	proxy := httptest.NewServer(NewSegmentReverseProxy(mustParseUrl(cdn.URL), mustParseUrl(trackingAPI.URL)))
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/v1/t", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %v, got %v", http.StatusOK, resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected the Tracking API to be called once, got %v calls", got)
	}
}

// TestProxyForwardsOversizedPostBody makes sure the size cap streams a large body
// through untouched instead of truncating it at maxBufferedBody.
func TestProxyForwardsOversizedPostBody(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), maxBufferedBody+1024)

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("CDN unexpected request: %v\n", r.URL)
	}))
	defer cdn.Close()

	received := make(chan []byte, 1)
	trackingAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Tracking API failed to read body: %v", err)
		}
		received <- body
	}))
	defer trackingAPI.Close()

	proxy := httptest.NewServer(NewSegmentReverseProxy(mustParseUrl(cdn.URL), mustParseUrl(trackingAPI.URL)))
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/v1/t", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %v, got %v", http.StatusOK, resp.StatusCode)
	}

	body := <-received
	if !bytes.Equal(body, payload) {
		t.Errorf("expected the Tracking API to receive all %v bytes, got %v", len(payload), len(body))
	}
}

func mustParseUrl(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatal(err)
	}
	return u
}
