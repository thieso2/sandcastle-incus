package domain

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	IANAAlphaTLDURL          = "https://data.iana.org/TLD/tlds-alpha-by-domain.txt"
	DefaultTLDSnapshotOutput = "internal/domain/tld_snapshot_generated.go"

	IANASpecialUseDomainCSVURL          = "https://www.iana.org/assignments/special-use-domain-names/special-use-domain.csv"
	DefaultSpecialUseSnapshotOutputPath = "internal/domain/special_use_snapshot_generated.go"
)

type RefreshRequest struct {
	SourceURL  string `json:"sourceURL"`
	OutputPath string `json:"outputPath"`
	DryRun     bool   `json:"dryRun"`
}

type RefreshResult struct {
	SourceURL  string   `json:"sourceURL"`
	OutputPath string   `json:"outputPath"`
	Count      int      `json:"count"`
	TLDs       []string `json:"tlds,omitempty"`
	Written    bool     `json:"written"`
}

type SpecialUseRefreshRequest struct {
	SourceURL  string `json:"sourceURL"`
	OutputPath string `json:"outputPath"`
	DryRun     bool   `json:"dryRun"`
}

type SpecialUseRefreshResult struct {
	SourceURL  string   `json:"sourceURL"`
	OutputPath string   `json:"outputPath"`
	Count      int      `json:"count"`
	Names      []string `json:"names,omitempty"`
	Written    bool     `json:"written"`
}

type DenyListRefreshRequest struct {
	TLDSourceURL         string `json:"tldSourceURL"`
	TLDOutputPath        string `json:"tldOutputPath"`
	SpecialUseSourceURL  string `json:"specialUseSourceURL"`
	SpecialUseOutputPath string `json:"specialUseOutputPath"`
	DryRun               bool   `json:"dryRun"`
}

type DenyListRefreshResult struct {
	TLD        RefreshResult           `json:"tld"`
	SpecialUse SpecialUseRefreshResult `json:"specialUse"`
}

func RefreshDenyListSnapshots(ctx context.Context, client *http.Client, request DenyListRefreshRequest) (DenyListRefreshResult, error) {
	tld, err := RefreshTLDSnapshot(ctx, client, RefreshRequest{
		SourceURL:  request.TLDSourceURL,
		OutputPath: request.TLDOutputPath,
		DryRun:     request.DryRun,
	})
	if err != nil {
		return DenyListRefreshResult{}, err
	}
	specialUse, err := RefreshSpecialUseSnapshot(ctx, client, SpecialUseRefreshRequest{
		SourceURL:  request.SpecialUseSourceURL,
		OutputPath: request.SpecialUseOutputPath,
		DryRun:     request.DryRun,
	})
	if err != nil {
		return DenyListRefreshResult{}, err
	}
	return DenyListRefreshResult{TLD: tld, SpecialUse: specialUse}, nil
}

func RefreshTLDSnapshot(ctx context.Context, client *http.Client, request RefreshRequest) (RefreshResult, error) {
	sourceURL := strings.TrimSpace(request.SourceURL)
	if sourceURL == "" {
		sourceURL = IANAAlphaTLDURL
	}
	outputPath := strings.TrimSpace(request.OutputPath)
	if outputPath == "" {
		outputPath = DefaultTLDSnapshotOutput
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return RefreshResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return RefreshResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RefreshResult{}, fmt.Errorf("fetch IANA TLD list: %s", resp.Status)
	}
	tlds, err := ParseIANAAlphaTLDs(resp.Body)
	if err != nil {
		return RefreshResult{}, err
	}
	source, err := GenerateTLDSnapshotSource(tlds)
	if err != nil {
		return RefreshResult{}, err
	}
	result := RefreshResult{
		SourceURL:  sourceURL,
		OutputPath: outputPath,
		Count:      len(tlds),
		TLDs:       tlds,
		Written:    !request.DryRun,
	}
	if request.DryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return RefreshResult{}, err
	}
	if err := os.WriteFile(outputPath, source, 0o644); err != nil {
		return RefreshResult{}, err
	}
	return result, nil
}

func RefreshSpecialUseSnapshot(ctx context.Context, client *http.Client, request SpecialUseRefreshRequest) (SpecialUseRefreshResult, error) {
	sourceURL := strings.TrimSpace(request.SourceURL)
	if sourceURL == "" {
		sourceURL = IANASpecialUseDomainCSVURL
	}
	outputPath := strings.TrimSpace(request.OutputPath)
	if outputPath == "" {
		outputPath = DefaultSpecialUseSnapshotOutputPath
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return SpecialUseRefreshResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return SpecialUseRefreshResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SpecialUseRefreshResult{}, fmt.Errorf("fetch IANA special-use domain list: %s", resp.Status)
	}
	names, err := ParseIANASpecialUseDomains(resp.Body)
	if err != nil {
		return SpecialUseRefreshResult{}, err
	}
	source, err := GenerateSpecialUseSnapshotSource(names)
	if err != nil {
		return SpecialUseRefreshResult{}, err
	}
	result := SpecialUseRefreshResult{
		SourceURL:  sourceURL,
		OutputPath: outputPath,
		Count:      len(names),
		Names:      names,
		Written:    !request.DryRun,
	}
	if request.DryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return SpecialUseRefreshResult{}, err
	}
	if err := os.WriteFile(outputPath, source, 0o644); err != nil {
		return SpecialUseRefreshResult{}, err
	}
	return result, nil
}

func ParseIANAAlphaTLDs(reader io.Reader) ([]string, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		label := strings.ToLower(line)
		if !validTLDLabel(label) {
			return nil, fmt.Errorf("invalid TLD label %q", line)
		}
		seen[label] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("IANA TLD list is empty")
	}
	tlds := make([]string, 0, len(seen))
	for tld := range seen {
		tlds = append(tlds, tld)
	}
	sort.Strings(tlds)
	return tlds, nil
}

func ParseIANASpecialUseDomains(reader io.Reader) ([]string, error) {
	records, err := csv.NewReader(reader).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("IANA special-use domain list is empty")
	}
	nameColumn := -1
	for i, header := range records[0] {
		if strings.EqualFold(strings.TrimSpace(header), "name") {
			nameColumn = i
			break
		}
	}
	if nameColumn < 0 {
		return nil, fmt.Errorf("IANA special-use domain list missing Name column")
	}
	seen := map[string]bool{}
	for _, record := range records[1:] {
		if nameColumn >= len(record) {
			continue
		}
		name := normalizeSpecialUseName(record[nameColumn])
		if name == "" {
			continue
		}
		if !validDomainName(name) {
			return nil, fmt.Errorf("invalid special-use domain name %q", record[nameColumn])
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("IANA special-use domain list is empty")
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func GenerateTLDSnapshotSource(tlds []string) ([]byte, error) {
	var builder bytes.Buffer
	builder.WriteString("// Code generated by sandcastle admin tld refresh; DO NOT EDIT.\n")
	builder.WriteString("package domain\n\n")
	builder.WriteString("var publicTLDs = map[string]bool{\n")
	for _, tld := range tlds {
		if !validTLDLabel(tld) {
			return nil, fmt.Errorf("invalid TLD label %q", tld)
		}
		fmt.Fprintf(&builder, "\t%q: true,\n", strings.ToLower(tld))
	}
	builder.WriteString("}\n")
	return format.Source(builder.Bytes())
}

func GenerateSpecialUseSnapshotSource(names []string) ([]byte, error) {
	var builder bytes.Buffer
	builder.WriteString("// Code generated by sandcastle admin tld refresh; DO NOT EDIT.\n")
	builder.WriteString("package domain\n\n")
	builder.WriteString("var specialUseNames = map[string]bool{\n")
	for _, name := range names {
		name = normalizeSpecialUseName(name)
		if !validDomainName(name) {
			return nil, fmt.Errorf("invalid special-use domain name %q", name)
		}
		fmt.Fprintf(&builder, "\t%q: true,\n", name)
	}
	builder.WriteString("}\n")
	return format.Source(builder.Bytes())
}

func validTLDLabel(label string) bool {
	if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for _, r := range label {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func normalizeSpecialUseName(value string) string {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.TrimSuffix(name, "(deprecated)")
	name = strings.TrimSpace(name)
	return strings.TrimSuffix(name, ".")
}

func validDomainName(name string) bool {
	if name == "" {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !validTLDLabel(label) {
			return false
		}
	}
	return true
}
