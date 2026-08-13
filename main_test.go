package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestProxyForwardsPostBody(t *testing.T) {
	payload := `{"event":"test"}`

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("CDN unexpected request: %v\n", r.URL)
	}))
	defer cdn.Close()

	trackingAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}

func mustParseUrl(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatal(err)
	}
	return u
}
