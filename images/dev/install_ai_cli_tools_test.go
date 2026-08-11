package dev

import (
	"os"
	"testing"
)

// Docker's build context is scoped per template (images/ai/, images/dev/),
// so install-ai-cli-tools.sh cannot be a single file both Dockerfiles COPY
// from (see implementation-notes.md) — it is instead kept as a deliberate,
// byte-identical duplicate in both directories. This test is the drift
// guard the plan calls for: it fails the moment the two copies diverge.
func TestInstallAICLIToolsScriptMatchesAIImage(t *testing.T) {
	devScript, err := os.ReadFile("install-ai-cli-tools.sh")
	if err != nil {
		t.Fatalf("reading images/dev/install-ai-cli-tools.sh: %v", err)
	}
	aiScript, err := os.ReadFile("../ai/install-ai-cli-tools.sh")
	if err != nil {
		t.Fatalf("reading images/ai/install-ai-cli-tools.sh: %v", err)
	}
	if string(devScript) != string(aiScript) {
		t.Errorf("images/dev/install-ai-cli-tools.sh and images/ai/install-ai-cli-tools.sh have diverged; keep them byte-identical")
	}
}
