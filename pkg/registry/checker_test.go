package registry

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHarborChecker_ImageExists_404_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")
	checker := &HarborChecker{client: newClientWithHTTP(ts.Client()), scheme: "http"}

	ok, err := checker.ImageExists(context.Background(), host+"/library/tool:latest", "sha256:abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for 404")
	}
}

func TestHarborChecker_ImageExists_401_IsNotNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No WWW-Authenticate header -> no usable challenge -> 401 passes through.
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")
	checker := &HarborChecker{client: newClientWithHTTP(ts.Client()), scheme: "http"}

	ok, err := checker.ImageExists(context.Background(), host+"/library/tool:latest", "sha256:abc")
	if err == nil {
		t.Fatal("expected error for 401, got nil (401 must not be classified as not-found)")
	}
	if ok {
		t.Error("expected ok=false for 401")
	}
}

func TestHarborChecker_ImageExists_401WithChallenge_RetriesWithAnonymousToken(t *testing.T) {
	mux := http.NewServeMux()
	registryTS := httptest.NewServer(mux)
	defer registryTS.Close()

	authTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"anon-token"}`))
	}))
	defer authTS.Close()

	mux.HandleFunc("/v2/library/tool/manifests/sha256:abc", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer anon-token" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q,service="registry"`, authTS.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})

	host := strings.TrimPrefix(registryTS.URL, "http://")
	checker := &HarborChecker{client: newClientWithHTTP(registryTS.Client()), scheme: "http"}

	ok, err := checker.ImageExists(context.Background(), host+"/library/tool:latest", "sha256:abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected ok=true after anonymous token retry")
	}
}

func TestHarborChecker_UsesConfiguredScheme(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	httpChecker := &HarborChecker{client: newClientWithHTTP(ts.Client()), scheme: "http"}
	if ok, err := httpChecker.ImageExists(context.Background(), host+"/library/tool:latest", "sha256:abc"); err != nil || !ok {
		t.Fatalf("scheme=http against a plain-http test server: ok=%v err=%v", ok, err)
	}

	httpsChecker := &HarborChecker{client: newClientWithHTTP(ts.Client()), scheme: "https"}
	if _, err := httpsChecker.ImageExists(context.Background(), host+"/library/tool:latest", "sha256:abc"); err == nil {
		t.Fatal("expected error when scheme=https is used against a plain-http test server")
	}
}

func TestHarborChecker_ReferrerExists_500_IsIndeterminate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")
	checker := &HarborChecker{client: newClientWithHTTP(ts.Client()), scheme: "http"}

	ok, err := checker.ReferrerExists(context.Background(), host+"/library/tool:latest", "sha256:abc")
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if ok {
		t.Error("expected ok=false for 500")
	}
}

func TestHarborChecker_PullReachable_200_ReturnsTrue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")
	checker := &HarborChecker{client: newClientWithHTTP(ts.Client()), scheme: "http"}

	ok, err := checker.PullReachable(context.Background(), host+"/library/tool:latest", "sha256:abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected ok=true for 200")
	}
}

// secondPageMarker is the query value the paginating stubs use to mark the
// continuation page.
const secondPageMarker = "p1"

// ── ReferrerExists: spec-type honesty ─────────────────────────────────────────
//
// IntegrityHealth=Healthy claims the expected ToolSpec spec referrer was observed.
// These tests pin that claim to the exact semantic kind: a ToolProfile referrer
// (pushed to the same subject digest by pkg/oras) or any unrelated artifact must
// never satisfy it, while a legacy referrer whose kind lives only in
// config.mediaType must still be recognized.

// referrerFixture builds a registry stub serving one referrers index plus the
// referrer manifests it lists. descriptors are written into the index verbatim;
// manifests maps a referrer digest to the manifest body served for it.
func referrerFixture(t *testing.T, descriptors, manifests map[string]string) (host string, calls *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		descs := make([]string, 0, len(descriptors))
		for _, d := range descriptors {
			descs = append(descs, d)
		}
		_, _ = fmt.Fprintf(w, `{"manifests":[%s]}`, strings.Join(descs, ","))
	})
	for digest, body := range manifests {
		mux.HandleFunc("/v2/library/tool/manifests/"+digest, func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			_, _ = w.Write([]byte(body))
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://"), &n
}

// referrerExists runs the check under test against a stub registry at host.
func referrerExists(t *testing.T, host string) (bool, error) {
	t.Helper()
	c := &HarborChecker{client: newClientWithHTTP(http.DefaultClient), scheme: "http"}
	return c.ReferrerExists(context.Background(), host+"/library/tool:latest", "sha256:subject")
}

func TestReferrerExists_TypedToolSpecDescriptor_IsHealthyEvidence(t *testing.T) {
	host, _ := referrerFixture(t, map[string]string{
		"spec": `{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}`,
	}, nil)

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("a corrected typed ToolSpec descriptor must be recognized directly from the index")
	}
}

func TestReferrerExists_LegacyToolSpec_IsRecognizedViaConfigMediaType(t *testing.T) {
	// sori's current push path stamps a generic artifactType on every referrer and
	// records the real kind in config.mediaType. Such an artifact is valid and must
	// not be reported absent just because the producer wire format is not migrated.
	host, calls := referrerFixture(t, map[string]string{
		"spec": `{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.oci.image.manifest.v1+json"}`,
	}, map[string]string{
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": `{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json"}}`,
	})

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("legacy ToolSpec referrer must be recognized through bounded manifest inspection")
	}
	if calls.Load() == 0 {
		t.Error("expected the legacy descriptor to be resolved by a manifest fetch")
	}
}

func TestReferrerExists_ToolProfileOnly_IsNotHealthyEvidence(t *testing.T) {
	// The regression that motivated this packet: pkg/oras.PushToolProfileReferrer
	// attaches to the same subject digest, so counting any referrer made a tool
	// whose ToolSpec push failed reconcile to Healthy.
	host, _ := referrerFixture(t, map[string]string{
		"profile": `{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.oci.image.manifest.v1+json"}`,
	}, map[string]string{
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": `{"config":{"mediaType":"application/vnd.nodevault.toolprofile.v1+json"}}`,
	})

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("a ToolProfile referrer alone must never prove the spec referrer exists")
	}
}

func TestReferrerExists_TypedToolProfile_IsDecidedWithoutFetch(t *testing.T) {
	host, calls := referrerFixture(t, map[string]string{
		"profile": `{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}`,
	}, map[string]string{
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": `{"config":{"mediaType":"application/vnd.nodevault.toolprofile.v1+json"}}`,
	})

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("typed ToolProfile must not satisfy the spec-referrer check")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("a typed descriptor is decidable from the index; got %d manifest fetches", got)
	}
}

func TestReferrerExists_UnrelatedReferrerOnly_IsNotHealthyEvidence(t *testing.T) {
	host, _ := referrerFixture(t, map[string]string{
		"other": `{"digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","artifactType":"application/vnd.oci.image.manifest.v1+json"}`,
	}, map[string]string{
		"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc": `{"config":{"mediaType":"application/vnd.example.something-else.v1+json"}}`,
	})

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("an unrelated artifact must never prove the spec referrer exists")
	}
}

func TestReferrerExists_SpecAlongsideProfile_IsHealthyEvidence(t *testing.T) {
	host, _ := referrerFixture(t, map[string]string{
		"profile": `{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.oci.image.manifest.v1+json"}`,
		"spec":    `{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.oci.image.manifest.v1+json"}`,
	}, map[string]string{
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": `{"config":{"mediaType":"application/vnd.nodevault.toolprofile.v1+json"}}`,
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": `{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json"}}`,
	})

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("the spec referrer must still be found when other referrers are attached too")
	}
}

func TestReferrerExists_EmptyIndex_IsConfirmedAbsence(t *testing.T) {
	host, _ := referrerFixture(t, nil, nil)

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("an empty referrers index is a confirmed absence")
	}
}

func TestReferrerExists_ManifestFetch5xx_IsIndeterminateNotAbsent(t *testing.T) {
	// A legacy descriptor we cannot resolve must fail closed. Reporting absence
	// here would write a false Partial over a possibly-Healthy entry.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	ok, err := referrerExists(t, host)
	if err == nil {
		t.Fatal("expected an indeterminate error when a referrer manifest cannot be read")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_ManifestFetch401_IsIndeterminateNotAbsent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(w http.ResponseWriter, _ *http.Request) {
		// No WWW-Authenticate -> no usable challenge -> the 401 passes through.
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	ok, err := referrerExists(t, host)
	if err == nil {
		t.Fatal("an auth challenge must not be classified as a missing spec referrer")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_InspectionBudgetExceeded_IsIndeterminateNotAbsent(t *testing.T) {
	descs := make([]string, 0, maxReferrerInspections+1)
	for i := 0; i <= maxReferrerInspections; i++ {
		descs = append(descs, fmt.Sprintf(
			`{"digest":"sha256:%064d","artifactType":"application/vnd.oci.image.manifest.v1+json"}`, i))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"manifests":[%s]}`, strings.Join(descs, ","))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	ok, err := referrerExists(t, host)
	if err == nil {
		t.Fatal("exceeding the inspection budget must be indeterminate, not a confirmed absence")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_VanishedReferrer_IsNotAnError(t *testing.T) {
	// Listed then deleted between the two calls: not the spec referrer, but also
	// not an infrastructure failure.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	ok, err := referrerExists(t, host)
	if err != nil {
		t.Fatalf("a 404 on a listed referrer is a confirmed absence, not an error: %v", err)
	}
	if ok {
		t.Error("expected ok=false")
	}
}

func TestReferrerExists_MatchBeforeBudget_WinsOverLargeListing(t *testing.T) {
	// The budget bounds fetches, so a spec referrer resolved before the budget is
	// spent must still be reported present even when the listing is oversized.
	descs := []string{`{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.oci.image.manifest.v1+json"}`}
	for i := 0; i <= maxReferrerInspections; i++ {
		descs = append(descs, fmt.Sprintf(
			`{"digest":"sha256:%064d","artifactType":"application/vnd.oci.image.manifest.v1+json"}`, i))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"manifests":[%s]}`, strings.Join(descs, ","))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("a spec referrer resolved within the budget must be reported present")
	}
}

func TestReferrerExists_SpecOnSecondPage_IsFoundByTraversal(t *testing.T) {
	// Codex P2: a ToolSpec descriptor on a later page must be found, not reported
	// absent. Page 1 carries only a ToolProfile and a rel="next" link.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == secondPageMarker {
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
			return
		}
		w.Header().Set("Link", fmt.Sprintf(
			`</v2/library/tool/referrers/sha256:subject?last=%s>; rel="next"`, secondPageMarker))
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("the spec referrer on page 2 must be found by following rel=\"next\"")
	}
}

func TestReferrerExists_AllPagesReadNoMatch_IsConfirmedAbsence(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == secondPageMarker {
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","artifactType":"application/vnd.nodevault.dataspec.v1+json"}]}`))
			return
		}
		w.Header().Set("Link", fmt.Sprintf(
			`</v2/library/tool/referrers/sha256:subject?last=%s>; rel="next"`, secondPageMarker))
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("exhausting the listing is a confirmed absence, not an error: %v", err)
	}
	if ok {
		t.Error("expected ok=false once every page is read without a spec referrer")
	}
}

func TestReferrerExists_UnboundedPagination_IsIndeterminateNotAbsent(t *testing.T) {
	// A registry that always advertises another page must not be walked forever,
	// and giving up must not look like a confirmed absence.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `</v2/library/tool/referrers/sha256:subject?last=x>; rel="next"`)
		_, _ = w.Write([]byte(`{"manifests":[]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err == nil {
		t.Fatal("outrunning the page budget must be indeterminate, not a confirmed absence")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_NextLinkOffHost_IsIndeterminateNotAbsent(t *testing.T) {
	// A continuation pointing at another host must not be followed, but refusing it
	// must not be reported as the end of the listing either — the spec referrer may
	// be on the page we declined to read.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<http://attacker.example/v2/library/tool/referrers/sha256:subject>; rel="next"`)
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err == nil {
		t.Fatal("an advertised but unfollowable continuation must be indeterminate, not a confirmed absence")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_NextLinkSameOriginDifferentSpelling_IsFollowed(t *testing.T) {
	// Uppercased host and an explicitly spelled default port denote the same
	// origin; rejecting them would turn a readable continuation into an error.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == secondPageMarker {
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
			return
		}
		host, port, _ := net.SplitHostPort(r.Host)
		w.Header().Set("Link", fmt.Sprintf(
			`<http://%s:%s/v2/library/tool/referrers/sha256:subject?last=%s>; rel="next"`,
			strings.ToUpper(host), port, secondPageMarker))
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("an equivalent origin spelled differently must still be followed: %v", err)
	}
	if !ok {
		t.Error("expected the spec referrer on the continuation page to be found")
	}
}

func TestReferrerExists_PaginatedWithMatchOnFirstPage_IsHealthyEvidence(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `</v2/library/tool/referrers/sha256:subject?n=1&last=x>; rel="next"`)
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("pagination must not matter once the spec referrer is found: %v", err)
	}
	if !ok {
		t.Error("expected ok=true")
	}
}

func TestNextPageURL_RelationSpellings(t *testing.T) {
	const current = "http://reg.example/v2/library/tool/referrers/sha256:subject"
	for _, tc := range []struct {
		name  string
		links []string
		want  string
	}{
		{"quoted", []string{`</v2/next>; rel="next"`}, "http://reg.example/v2/next"},
		{"bare", []string{`</v2/next>; rel=next`}, "http://reg.example/v2/next"},
		{"uppercase", []string{`</v2/next>; rel=NEXT`}, "http://reg.example/v2/next"},
		{"relation list", []string{`</v2/next>; rel="prev next"`}, "http://reg.example/v2/next"},
		{"spaced equals", []string{`</v2/next>; rel = next`}, "http://reg.example/v2/next"},
		{"other relation only", []string{`</v2/prev>; rel="prev"`}, ""},
		{"no link header", []string{``}, ""},
		{"comma inside target", []string{`</v2/n,ext>; rel=next`}, "http://reg.example/v2/n,ext"},
		{"second entry is next", []string{`</v2/prev>; rel="prev", </v2/next>; rel="next"`}, "http://reg.example/v2/next"},
		{"next in a later Link field", []string{`</v2/prev>; rel="prev"`, `</v2/next>; rel="next"`}, "http://reg.example/v2/next"},
		{"no next across fields", []string{`</v2/prev>; rel="prev"`, `</v2/first>; rel="first"`}, ""},
		{
			"semicolon inside a quoted parameter",
			[]string{`</v2/previous>; rel=prev; title="x; rel=next", </v2/actual>; rel=next`},
			"http://reg.example/v2/actual",
		},
		{
			"escaped quote inside a parameter",
			[]string{`</v2/previous>; rel=prev; title="x\"", </v2/actual>; rel=next`},
			"http://reg.example/v2/actual",
		},
		{
			"comma inside a quoted parameter",
			[]string{`</v2/previous>; rel=prev; title="x, rel=next", </v2/actual>; rel=next`},
			"http://reg.example/v2/actual",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nextPageURL(current, tc.links)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("nextPageURL(%q) = %q, want %q", tc.links, got, tc.want)
			}
		})
	}
}

func TestReferrerExists_BareRelNext_IsTraversed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == secondPageMarker {
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
			return
		}
		w.Header().Set("Link", fmt.Sprintf(
			`</v2/library/tool/referrers/sha256:subject?last=%s>; rel=next`, secondPageMarker))
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("an unquoted rel=next continuation must be traversed")
	}
}

func TestReferrerExists_UnreadableNeighbourBeforeSpec_StillFindsSpec(t *testing.T) {
	// A persistent 5xx on an unrelated artifact must not stop the walk before the
	// readable ToolSpec that follows it, or FastRun could never recognize it.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[` +
			`{"digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","artifactType":"application/vnd.oci.image.manifest.v1+json"},` +
			`{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:1111111111111111111111111111111111111111111111111111111111111111", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("an unreadable unrelated referrer must not mask a readable ToolSpec: %v", err)
	}
	if !ok {
		t.Error("expected the ToolSpec after the failing descriptor to be found")
	}
}

func TestReferrerExists_UnreadableNeighbourAndNoSpec_IsIndeterminate(t *testing.T) {
	// Same failure, but nothing else matches: the unread descriptor could have
	// been the ToolSpec, so this must stay indeterminate rather than absent.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[` +
			`{"digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","artifactType":"application/vnd.oci.image.manifest.v1+json"},` +
			`{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:1111111111111111111111111111111111111111111111111111111111111111", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err == nil {
		t.Fatal("an unread descriptor must keep the outcome indeterminate, not absent")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_BudgetSpentThenTypedSpecOnNextPage_IsFound(t *testing.T) {
	// Exhausting the fetch budget must not abandon the walk: a typed ToolSpec on a
	// later page needs no fetch at all.
	descs := make([]string, 0, maxReferrerInspections+1)
	for i := 0; i <= maxReferrerInspections; i++ {
		descs = append(descs, fmt.Sprintf(
			`{"digest":"sha256:%064d","artifactType":"application/vnd.oci.image.manifest.v1+json"}`, i))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == secondPageMarker {
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
			return
		}
		w.Header().Set("Link", fmt.Sprintf(
			`</v2/library/tool/referrers/sha256:subject?last=%s>; rel="next"`, secondPageMarker))
		_, _ = fmt.Fprintf(w, `{"manifests":[%s]}`, strings.Join(descs, ","))
	})
	mux.HandleFunc("/v2/library/tool/manifests/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.example.other.v1+json"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("a typed ToolSpec on a later page must be found even after the fetch budget is spent")
	}
}

func TestReferrerExists_NextInLaterLinkField_IsTraversed(t *testing.T) {
	// The continuation arrives in a second Link header field; reading only the
	// first would report a confirmed absence for a ToolSpec that does exist.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == secondPageMarker {
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
			return
		}
		w.Header().Add("Link", `</v2/library/tool/referrers/sha256:subject>; rel="prev"`)
		w.Header().Add("Link", fmt.Sprintf(
			`</v2/library/tool/referrers/sha256:subject?last=%s>; rel="next"`, secondPageMarker))
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifactType":"application/vnd.nodevault.toolprofile.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("a continuation advertised in a later Link field must still be followed")
	}
}

func TestReferrerExists_UntypedDescriptorWithoutDigest_IsIndeterminate(t *testing.T) {
	// Nothing can ever identify this descriptor, so it must not be counted as a
	// nonmatch and turned into a confirmed absence.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err == nil {
		t.Fatal("a descriptor with no digest to inspect must be indeterminate, not a nonmatch")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_ManifestWithoutAnyKind_IsIndeterminate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err == nil {
		t.Fatal("a manifest identifying no kind must be indeterminate, not a nonmatch")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
}

func TestReferrerExists_ManifestWithForeignKind_IsCleanNonmatch(t *testing.T) {
	// A referrer that identifies itself as something else IS determined, so the
	// listing can still end in a confirmed absence.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.example.sbom.v1+json"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("an identified foreign artifact is a determined nonmatch: %v", err)
	}
	if ok {
		t.Error("expected ok=false")
	}
}

func TestReferrerExists_TypedToolSpecWithoutDigest_IsIndeterminate(t *testing.T) {
	// A descriptor claiming the ToolSpec kind but naming no manifest is malformed
	// and must not be accepted as proof of Healthy.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if ok {
		t.Error("a digest-less ToolSpec descriptor must not prove the spec referrer exists")
	}
	if err == nil {
		t.Error("expected an indeterminate error for a malformed descriptor")
	}
}

func TestReferrerExists_SpecOnPageWithUnusableContinuation_IsFound(t *testing.T) {
	// The page already proves the ToolSpec exists, so an off-origin continuation
	// never needs following and must not discard that evidence.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<http://attacker.example/v2/next>; rel="next"`)
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("evidence on the current page must survive an unusable continuation: %v", err)
	}
	if !ok {
		t.Error("expected the ToolSpec on this page to be found")
	}
}

func TestReferrerExists_ForeignTypedDescriptor_IsSettledWithoutFetch(t *testing.T) {
	// A descriptor stating a meaningful foreign artifactType has already answered
	// the question. It must not be re-opened by reading config.mediaType, or a
	// manifest whose config claims the ToolSpec type could falsely prove Healthy.
	fetched := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff","artifactType":"application/vnd.cncf.notary.signature"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", func(w http.ResponseWriter, _ *http.Request) {
		fetched++
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("a foreign-typed referrer must not prove the spec referrer exists")
	}
	if fetched != 0 {
		t.Errorf("a foreign-typed descriptor is settled by the listing; got %d manifest fetches", fetched)
	}
}

func TestReferrerKind_ForeignManifestArtifactType_WinsOverConfig(t *testing.T) {
	// Same precedence one level down: the manifest's own artifactType is
	// authoritative, and config.mediaType is consulted only for the legacy
	// generic value.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/sha256:2222222222222222222222222222222222222222222222222222222222222222", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"artifactType":"application/vnd.cncf.notary.signature","config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("a manifest declaring a foreign artifactType must not match on its config mediaType")
	}
}

func TestReferrerExists_MalformedDescriptorDigest_IsIndeterminate(t *testing.T) {
	// A syntactically invalid digest cannot be fetched meaningfully — the request
	// would 404 and look like a clean nonmatch — so it must stay indeterminate.
	fetched := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"not-a-digest","artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`))
	})
	mux.HandleFunc("/v2/library/tool/manifests/not-a-digest", func(w http.ResponseWriter, _ *http.Request) {
		fetched++
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err == nil {
		t.Fatal("a malformed descriptor digest must be indeterminate, not a confirmed absence")
	}
	if ok {
		t.Error("expected ok=false alongside the error")
	}
	if fetched != 0 {
		t.Errorf("a malformed digest should not be fetched at all; got %d fetches", fetched)
	}
}

func TestReferrerExists_TypedToolSpecMalformedDigest_IsIndeterminate(t *testing.T) {
	// A typed ToolSpec descriptor still has to identify a manifest; a malformed
	// digest identifies nothing and must not prove Healthy.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[{"digest":"not-a-digest","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if ok {
		t.Error("a ToolSpec descriptor with a malformed digest must not prove the spec referrer exists")
	}
	if err == nil {
		t.Error("expected an indeterminate error")
	}
}

func TestReferrerExists_ResponseOmittingManifests_IsIndeterminate(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty object", `{}`},
		{"null manifests", `{"manifests":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
			if err == nil {
				t.Fatal("a response carrying no manifests array is not absence evidence")
			}
			if ok {
				t.Error("expected ok=false alongside the error")
			}
		})
	}
}

func TestReferrerExists_ExplicitlyEmptyManifests_IsConfirmedAbsence(t *testing.T) {
	// The counterpart: an explicit empty array IS valid absence evidence.
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/referrers/sha256:subject", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifests":[]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ok, err := referrerExists(t, strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("an explicit empty listing is a confirmed absence: %v", err)
	}
	if ok {
		t.Error("expected ok=false")
	}
}
