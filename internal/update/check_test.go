package update

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

const releaseJSON = `{
	"tag_name": "v0.2.0",
	"html_url": "https://github.com/thieso2/sandcastle-incus/releases/tag/v0.2.0",
	"published_at": "2026-07-10T09:00:00Z",
	"assets": [
		{"name": "sandcastle-linux-amd64.tar.gz", "browser_download_url": "https://github.com/thieso2/sandcastle-incus/releases/download/v0.2.0/sandcastle-linux-amd64.tar.gz"},
		{"name": "checksums.txt", "browser_download_url": "https://github.com/thieso2/sandcastle-incus/releases/download/v0.2.0/checksums.txt"}
	]
}`

func TestCheckFetchesAndCachesLatestRelease(t *testing.T) {
	var gotPath, gotIfNoneMatch string
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotPath = r.URL.Path
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		if gotIfNoneMatch == `W/"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"etag-1"`)
		w.Write([]byte(releaseJSON))
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "update-state.json")
	checker := &Checker{APIBaseURL: server.URL, StatePath: statePath}

	st, err := checker.Check(t.Context(), now)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if gotPath != "/repos/thieso2/sandcastle-incus/releases/latest" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if st.LatestTag != "v0.2.0" || st.ETag != `W/"etag-1"` {
		t.Fatalf("unexpected state %+v", st)
	}
	if want := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC); !st.PublishedAt.Equal(want) {
		t.Fatalf("published_at = %v, want %v", st.PublishedAt, want)
	}
	if !st.CheckedAt.Equal(now) {
		t.Fatalf("checked_at = %v, want %v", st.CheckedAt, now)
	}

	// Persisted: a reload sees the same tag.
	onDisk, err := LoadState(statePath)
	if err != nil || onDisk.LatestTag != "v0.2.0" {
		t.Fatalf("state not persisted: %+v err=%v", onDisk, err)
	}

	// Second check replays the ETag; a 304 keeps the cached tag and bumps CheckedAt.
	later := now.Add(25 * time.Hour)
	st2, err := checker.Check(t.Context(), later)
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if gotIfNoneMatch != `W/"etag-1"` {
		t.Fatalf("second request If-None-Match = %q", gotIfNoneMatch)
	}
	if st2.LatestTag != "v0.2.0" || !st2.CheckedAt.Equal(later) {
		t.Fatalf("304 handling wrong: %+v", st2)
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
}

func TestCheckServerErrorKeepsPriorState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // rate-limited
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "update-state.json")
	prior := State{CheckedAt: twoDays, LatestTag: "v0.1.5"}
	if err := SaveState(statePath, prior); err != nil {
		t.Fatal(err)
	}
	checker := &Checker{APIBaseURL: server.URL, StatePath: statePath}
	if _, err := checker.Check(t.Context(), now); err == nil {
		t.Fatal("expected error on 403")
	}
	onDisk, _ := LoadState(statePath)
	if onDisk.LatestTag != "v0.1.5" {
		t.Fatalf("prior state clobbered: %+v", onDisk)
	}
}

func TestResolveReleaseByTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/thieso2/sandcastle-incus/releases/tags/v0.2.0" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(releaseJSON))
	}))
	defer server.Close()

	checker := &Checker{APIBaseURL: server.URL}
	rel, err := checker.ResolveRelease(t.Context(), "v0.2.0")
	if err != nil {
		t.Fatalf("ResolveRelease: %v", err)
	}
	if rel.TagName != "v0.2.0" || len(rel.Assets) != 2 {
		t.Fatalf("unexpected release %+v", rel)
	}
	url, ok := rel.AssetURL("sandcastle-linux-amd64.tar.gz")
	if !ok || url != "https://github.com/thieso2/sandcastle-incus/releases/download/v0.2.0/sandcastle-linux-amd64.tar.gz" {
		t.Fatalf("AssetURL = %q, %v", url, ok)
	}
	if _, ok := rel.AssetURL("sandcastle-plan9-386.tar.gz"); ok {
		t.Fatal("unexpected asset match")
	}
}
