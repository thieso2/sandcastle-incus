package domain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIANAAlphaTLDs(t *testing.T) {
	tlds, err := ParseIANAAlphaTLDs(strings.NewReader("# Version 2026050700\nCOM\nXN--P1AI\nORG\nCOM\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tlds, ",") != "com,org,xn--p1ai" {
		t.Fatalf("tlds = %#v", tlds)
	}
}

func TestParseIANAAlphaTLDsRejectsInvalidLabels(t *testing.T) {
	if _, err := ParseIANAAlphaTLDs(strings.NewReader("BAD_LABEL\n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseIANASpecialUseDomains(t *testing.T) {
	names, err := ParseIANASpecialUseDomains(strings.NewReader("Name,Reference\nALT.,[RFC9476]\neap-noob.arpa. (DEPRECATED),[RFC9140]\nLOCAL.,[RFC6762]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "alt,eap-noob.arpa,local" {
		t.Fatalf("names = %#v", names)
	}
}

func TestParseIANASpecialUseDomainsRejectsInvalidNames(t *testing.T) {
	if _, err := ParseIANASpecialUseDomains(strings.NewReader("Name,Reference\nbad_name.,[RFC]\n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerateTLDSnapshotSource(t *testing.T) {
	source, err := GenerateTLDSnapshotSource([]string{"com", "org"})
	if err != nil {
		t.Fatal(err)
	}
	content := string(source)
	if !strings.Contains(content, `"com": true`) || !strings.Contains(content, "Code generated") {
		t.Fatalf("source = %s", content)
	}
}

func TestGenerateSpecialUseSnapshotSource(t *testing.T) {
	source, err := GenerateSpecialUseSnapshotSource([]string{"local", "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	content := string(source)
	if !strings.Contains(content, `"local":`) || !strings.Contains(content, "specialUseNames") {
		t.Fatalf("source = %s", content)
	}
}

func TestRefreshTLDSnapshotWritesGeneratedSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Version 2026050700\nCOM\nORG\n"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "tld_snapshot_generated.go")
	result, err := RefreshTLDSnapshot(context.Background(), server.Client(), RefreshRequest{
		SourceURL:  server.URL,
		OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 || !result.Written {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"org": true`) {
		t.Fatalf("content = %s", string(content))
	}
}

func TestRefreshSpecialUseSnapshotWritesGeneratedSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Name,Reference\nLOCAL.,[RFC6762]\nTEST.,[RFC6761]\n"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "special_use_snapshot_generated.go")
	result, err := RefreshSpecialUseSnapshot(context.Background(), server.Client(), SpecialUseRefreshRequest{
		SourceURL:  server.URL,
		OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 || !result.Written {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"local": true`) {
		t.Fatalf("content = %s", string(content))
	}
}

func TestRefreshTLDSnapshotDryRunDoesNotWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("COM\n"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "tld_snapshot_generated.go")
	result, err := RefreshTLDSnapshot(context.Background(), server.Client(), RefreshRequest{
		SourceURL:  server.URL,
		OutputPath: output,
		DryRun:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("expected no output file, stat err = %v", err)
	}
}

func TestRefreshDenyListSnapshots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tlds":
			_, _ = w.Write([]byte("COM\nORG\n"))
		case "/special-use":
			_, _ = w.Write([]byte("Name,Reference\nLOCAL.,[RFC6762]\nTEST.,[RFC6761]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	result, err := RefreshDenyListSnapshots(context.Background(), server.Client(), DenyListRefreshRequest{
		TLDSourceURL:         server.URL + "/tlds",
		TLDOutputPath:        filepath.Join(dir, "tld_snapshot_generated.go"),
		SpecialUseSourceURL:  server.URL + "/special-use",
		SpecialUseOutputPath: filepath.Join(dir, "special_use_snapshot_generated.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TLD.Count != 2 || result.SpecialUse.Count != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRefreshDenyListSnapshotsDoesNotWritePartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tlds":
			_, _ = w.Write([]byte("COM\nORG\n"))
		case "/special-use":
			_, _ = w.Write([]byte("Name,Reference\nbad_name.,[RFC]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	tldOutput := filepath.Join(dir, "tld_snapshot_generated.go")
	specialUseOutput := filepath.Join(dir, "special_use_snapshot_generated.go")
	_, err := RefreshDenyListSnapshots(context.Background(), server.Client(), DenyListRefreshRequest{
		TLDSourceURL:         server.URL + "/tlds",
		TLDOutputPath:        tldOutput,
		SpecialUseSourceURL:  server.URL + "/special-use",
		SpecialUseOutputPath: specialUseOutput,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(tldOutput); !os.IsNotExist(err) {
		t.Fatalf("expected no partial TLD output, stat err = %v", err)
	}
	if _, err := os.Stat(specialUseOutput); !os.IsNotExist(err) {
		t.Fatalf("expected no special-use output, stat err = %v", err)
	}
}
