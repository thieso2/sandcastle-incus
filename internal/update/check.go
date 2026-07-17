package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Repo is the GitHub repository releases are cut from.
const Repo = "thieso2/sandcastle-incus"

const defaultAPIBaseURL = "https://api.github.com"

// Release is the subset of the GitHub release object the updater needs.
type Release struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// AssetURL returns the download URL for the named asset.
func (r Release) AssetURL(name string) (string, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL, true
		}
	}
	return "", false
}

// Checker performs release lookups against the GitHub API and maintains the
// cached check State on disk.
type Checker struct {
	// APIBaseURL overrides the GitHub API base (tests); empty means api.github.com.
	APIBaseURL string
	// StatePath is where the cached check state lives; empty means Check
	// does not persist (ResolveRelease never persists).
	StatePath string
	// HTTPClient overrides the default 30s-timeout client.
	HTTPClient *http.Client
}

// Check fetches the latest release, honouring the stored ETag, and persists
// the refreshed State. A 304 keeps the cached result and only bumps
// CheckedAt. Errors leave the prior state untouched.
func (c *Checker) Check(ctx context.Context, now time.Time) (State, error) {
	st, err := LoadState(c.StatePath)
	if err != nil {
		return State{}, err
	}
	req, err := c.newRequest(ctx, "/repos/"+Repo+"/releases/latest")
	if err != nil {
		return State{}, err
	}
	if st.ETag != "" {
		req.Header.Set("If-None-Match", st.ETag)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return State{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		st.CheckedAt = now
	case http.StatusOK:
		var rel Release
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return State{}, fmt.Errorf("decode release: %w", err)
		}
		st = State{
			CheckedAt:   now,
			ETag:        resp.Header.Get("ETag"),
			LatestTag:   rel.TagName,
			LatestURL:   rel.HTMLURL,
			PublishedAt: rel.PublishedAt,
			NoticedAt:   st.NoticedAt,
		}
	default:
		return State{}, fmt.Errorf("release check: unexpected status %s", resp.Status)
	}
	if c.StatePath != "" {
		if err := SaveState(c.StatePath, st); err != nil {
			return State{}, err
		}
	}
	return st, nil
}

// ResolveRelease fetches release metadata for a pinned tag, or the latest
// release when tag is empty. It never touches the cached state.
func (c *Checker) ResolveRelease(ctx context.Context, tag string) (Release, error) {
	path := "/repos/" + Repo + "/releases/latest"
	if tag != "" {
		path = "/repos/" + Repo + "/releases/tags/" + tag
	}
	req, err := c.newRequest(ctx, path)
	if err != nil {
		return Release{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Release{}, fmt.Errorf("resolve release %s: %s: %s", path, resp.Status, body)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("decode release: %w", err)
	}
	return rel, nil
}

func (c *Checker) newRequest(ctx context.Context, path string) (*http.Request, error) {
	base := c.APIBaseURL
	if base == "" {
		base = defaultAPIBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

func (c *Checker) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}
