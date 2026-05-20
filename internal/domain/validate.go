package domain

import (
	"fmt"
	"strings"
)

type Policy struct {
	AllowedSuffixes []string
	DeniedSuffixes  []string
}

func ValidateProjectDomain(value string, policy Policy) (string, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	if strings.ContainsAny(domain, "/ ") {
		return "", fmt.Errorf("invalid project domain %q", value)
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid project domain %q", value)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", fmt.Errorf("invalid project domain %q", value)
			}
		}
	}
	finalLabel := labels[len(labels)-1]
	if !suffixAllowed(domain, policy.AllowedSuffixes) {
		if specialUseFinalLabels[finalLabel] {
			return "", fmt.Errorf("project domain %q uses denied suffix %q", domain, finalLabel)
		}
		if publicTLDs[finalLabel] {
			return "", fmt.Errorf("project domain %q uses denied public TLD %q", domain, finalLabel)
		}
	}
	for _, suffix := range policy.DeniedSuffixes {
		suffix = strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), "."), ".")
		if suffix == "" {
			continue
		}
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return "", fmt.Errorf("project domain %q uses admin-denied suffix %q", domain, suffix)
		}
	}
	return domain, nil
}

func suffixAllowed(domain string, suffixes []string) bool {
	for _, suffix := range suffixes {
		suffix = strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), "."), ".")
		if suffix == "" {
			continue
		}
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	return false
}

var specialUseFinalLabels = map[string]bool{
	// IANA special-use and infrastructure names.
	"arpa":      true,
	"example":   true,
	"home":      true,
	"invalid":   true,
	"localhost": true,
	"local":     true,
	"onion":     true,
	"test":      true,
}
