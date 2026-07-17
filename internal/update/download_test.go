package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetName(t *testing.T) {
	if got := AssetName("linux", "amd64"); got != "sandcastle-linux-amd64.tar.gz" {
		t.Fatalf("AssetName = %q", got)
	}
	if got := AssetName("darwin", "arm64"); got != "sandcastle-darwin-arm64.tar.gz" {
		t.Fatalf("AssetName = %q", got)
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte(`8b634263cd39e4582f997ff5309576b64bb50f3afff58c1abe07ad93396a6606  sandcastle-darwin-amd64.tar.gz
0c47c95fe30ce68f8edc5790bf6a54d7a0ac2b2255620aacc601c9d993797830  sandcastle-linux-amd64.tar.gz
`)
	sums := ParseChecksums(data)
	if got := sums["sandcastle-linux-amd64.tar.gz"]; got != "0c47c95fe30ce68f8edc5790bf6a54d7a0ac2b2255620aacc601c9d993797830" {
		t.Fatalf("linux sum = %q", got)
	}
	if len(sums) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(sums))
	}
}

func tarGzWithBinary(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchBinaryVerifiesChecksumAndExtracts(t *testing.T) {
	binary := []byte("#!fake-sandcastle-binary")
	archive := tarGzWithBinary(t, "sandcastle", binary)
	sum := sha256.Sum256(archive)

	mux := http.NewServeMux()
	mux.HandleFunc("/dl/sandcastle-linux-amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%x  sandcastle-linux-amd64.tar.gz\n", sum)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	rel := Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: "sandcastle-linux-amd64.tar.gz", BrowserDownloadURL: server.URL + "/dl/sandcastle-linux-amd64.tar.gz"},
			{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/dl/checksums.txt"},
		},
	}
	checker := &Checker{}
	got, err := checker.FetchBinary(t.Context(), rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("FetchBinary: %v", err)
	}
	if !bytes.Equal(got, binary) {
		t.Fatalf("extracted binary mismatch")
	}
}

func TestFetchBinaryRejectsChecksumMismatch(t *testing.T) {
	archive := tarGzWithBinary(t, "sandcastle", []byte("real"))
	mux := http.NewServeMux()
	mux.HandleFunc("/dl/sandcastle-linux-amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  sandcastle-linux-amd64.tar.gz")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	rel := Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: "sandcastle-linux-amd64.tar.gz", BrowserDownloadURL: server.URL + "/dl/sandcastle-linux-amd64.tar.gz"},
			{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/dl/checksums.txt"},
		},
	}
	checker := &Checker{}
	if _, err := checker.FetchBinary(t.Context(), rel, "linux", "amd64"); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestFetchBinaryMissingAsset(t *testing.T) {
	checker := &Checker{}
	rel := Release{TagName: "v0.2.0"}
	if _, err := checker.FetchBinary(t.Context(), rel, "plan9", "386"); err == nil {
		t.Fatal("expected missing-asset error")
	}
}
