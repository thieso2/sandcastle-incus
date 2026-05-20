package domain

import (
	"fmt"
	"strings"
)

type Policy struct {
	DeniedSuffixes []string
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
	if deniedFinalLabels[finalLabel] {
		return "", fmt.Errorf("project domain %q uses denied suffix %q", domain, finalLabel)
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

var deniedFinalLabels = map[string]bool{
	// IANA special-use and infrastructure names.
	"arpa":      true,
	"example":   true,
	"home":      true,
	"invalid":   true,
	"localhost": true,
	"local":     true,
	"onion":     true,
	"test":      true,
	// Common public TLD snapshot. The refresh command will replace this with a
	// generated IANA snapshot in a later slice.
	"app":   true,
	"biz":   true,
	"cloud": true,
	"club":  true,
	"co":    true,
	"com":   true,
	"dev":   true,
	"edu":   true,
	"gov":   true,
	"info":  true,
	"io":    true,
	"me":    true,
	"mil":   true,
	"net":   true,
	"org":   true,
	"site":  true,
	"store": true,
	"tech":  true,
	"us":    true,
}
