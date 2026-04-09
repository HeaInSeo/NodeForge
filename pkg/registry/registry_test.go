package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── parseDestination ────────────────────────────────────────────────────────

func TestParseDestination_Valid(t *testing.T) {
	host, name, tag, err := parseDestination("10.96.0.1:5000/bwa:v0.7.17")
	if err != nil {
		t.Fatalf("parseDestination: %v", err)
	}
	if host != "10.96.0.1:5000" {
		t.Errorf("host: got %q", host)
	}
	if name != "bwa" {
		t.Errorf("name: got %q", name)
	}
	if tag != "v0.7.17" {
		t.Errorf("tag: got %q", tag)
	}
}

func TestParseDestination_NoSlash(t *testing.T) {
	_, _, _, err := parseDestination("nodestination")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseDestination_NoColon(t *testing.T) {
	_, _, _, err := parseDestination("host/imagewithoutcolontag")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── GetDigest ───────────────────────────────────────────────────────────────

func TestGetDigest_DockerContentDigestHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:headerhash")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newClientWithHTTP(ts.Client())
	host := strings.TrimPrefix(ts.URL, "http://")

	digest, err := c.GetDigest(context.Background(), host+"/img:latest")
	if err != nil {
		t.Fatalf("GetDigest: %v", err)
	}
	if digest != "sha256:headerhash" {
		t.Errorf("got %q, want %q", digest, "sha256:headerhash")
	}
}

func TestGetDigest_DigestFromJSONBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"config":{"digest":"sha256:bodyhash"}}`)
	}))
	defer ts.Close()

	c := newClientWithHTTP(ts.Client())
	host := strings.TrimPrefix(ts.URL, "http://")

	digest, err := c.GetDigest(context.Background(), host+"/img:v1")
	if err != nil {
		t.Fatalf("GetDigest: %v", err)
	}
	if digest != "sha256:bodyhash" {
		t.Errorf("got %q", digest)
	}
}

func TestGetDigest_NoDigest_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{}`)
	}))
	defer ts.Close()

	c := newClientWithHTTP(ts.Client())
	host := strings.TrimPrefix(ts.URL, "http://")

	_, err := c.GetDigest(context.Background(), host+"/img:v1")
	if err == nil {
		t.Fatal("expected error when no digest found")
	}
}

func TestGetDigest_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := newClientWithHTTP(ts.Client())
	host := strings.TrimPrefix(ts.URL, "http://")

	_, err := c.GetDigest(context.Background(), host+"/img:v1")
	if err == nil {
		t.Fatal("expected error for 500 response with no digest")
	}
}

func TestGetDigest_InvalidDestination(t *testing.T) {
	c := NewClient()
	_, err := c.GetDigest(context.Background(), "nodestination")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetDigest_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This handler should never be reached.
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newClientWithHTTP(ts.Client())
	host := strings.TrimPrefix(ts.URL, "http://")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.GetDigest(ctx, host+"/img:v1")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ─── NewClient ───────────────────────────────────────────────────────────────

func TestNewClient_UsesDefaultHTTPClient(t *testing.T) {
	c := NewClient()
	if c.http != http.DefaultClient {
		t.Error("NewClient should use http.DefaultClient")
	}
}
