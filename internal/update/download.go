package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// binaryName is the single file inside every release tarball.
const binaryName = "sandcastle"

// checksumsAsset is GoReleaser's unified SHA-256 manifest asset.
const checksumsAsset = "checksums.txt"

// maxBinarySize caps download/extract sizes as a sanity bound (the real
// tarballs are ~10MB).
const maxBinarySize = 512 << 20

// AssetName returns the release tarball name for a platform, matching the
// GoReleaser name_template sandcastle-{{ .Os }}-{{ .Arch }}.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("sandcastle-%s-%s.tar.gz", goos, goarch)
}

// ParseChecksums parses sha256sum-format output (checksums.txt) into a
// map of file name → lowercase hex digest. Malformed lines are skipped.
func ParseChecksums(data []byte) map[string]string {
	sums := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return sums
}

// FetchBinary downloads the platform tarball for the release, verifies its
// SHA-256 against the release's checksums.txt, and returns the extracted
// sandcastle binary.
func (c *Checker) FetchBinary(ctx context.Context, rel Release, goos, goarch string) ([]byte, error) {
	asset := AssetName(goos, goarch)
	assetURL, ok := rel.AssetURL(asset)
	if !ok {
		return nil, fmt.Errorf("release %s has no asset %s", rel.TagName, asset)
	}
	sumsURL, ok := rel.AssetURL(checksumsAsset)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.TagName, checksumsAsset)
	}

	sumsData, err := c.download(ctx, sumsURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", checksumsAsset, err)
	}
	want, ok := ParseChecksums(sumsData)[asset]
	if !ok {
		return nil, fmt.Errorf("%s has no entry for %s", checksumsAsset, asset)
	}

	archive, err := c.download(ctx, assetURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s: got %x, want %s", asset, got, want)
	}
	return extractBinary(archive)
}

func (c *Checker) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBinarySize))
}

func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open tarball: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("tarball has no %s binary", binaryName)
		}
		if err != nil {
			return nil, fmt.Errorf("read tarball: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && strings.TrimPrefix(hdr.Name, "./") == binaryName {
			return io.ReadAll(io.LimitReader(tr, maxBinarySize))
		}
	}
}
