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
	if err := validateDomainLabels(domain, value, "project domain"); err != nil {
		return "", err
	}
	allowedSuffixes, err := normalizePolicySuffixes("allowed", policy.AllowedSuffixes)
	if err != nil {
		return "", err
	}
	deniedSuffixes, err := normalizePolicySuffixes("denied", policy.DeniedSuffixes)
	if err != nil {
		return "", err
	}
	labels := strings.Split(domain, ".")
	finalLabel := labels[len(labels)-1]
	if !suffixAllowed(domain, allowedSuffixes) {
		if specialUseName := deniedSpecialUseDomain(domain); specialUseName != "" {
			return "", fmt.Errorf("project domain %q uses denied special-use suffix %q", domain, specialUseName)
		}
		if publicTLDs[finalLabel] {
			return "", fmt.Errorf("project domain %q uses denied public TLD %q", domain, finalLabel)
		}
	}
	for _, suffix := range deniedSuffixes {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return "", fmt.Errorf("project domain %q uses admin-denied suffix %q", domain, suffix)
		}
	}
	return domain, nil
}

func NormalizePolicySuffix(value string) (string, error) {
	suffix := strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), "."), ".")
	if suffix == "" {
		return "", fmt.Errorf("domain suffix is required")
	}
	if err := validateDomainLabels(suffix, value, "domain suffix"); err != nil {
		return "", err
	}
	return suffix, nil
}

func normalizePolicySuffixes(kind string, suffixes []string) ([]string, error) {
	output := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		if strings.TrimSpace(suffix) == "" {
			continue
		}
		normalized, err := NormalizePolicySuffix(suffix)
		if err != nil {
			return nil, fmt.Errorf("invalid %s domain suffix %q: %w", kind, suffix, err)
		}
		output = append(output, normalized)
	}
	return output, nil
}

func suffixAllowed(domain string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	return false
}

func deniedSpecialUseDomain(domain string) string {
	for name := range specialUseNames {
		if domain == name || strings.HasSuffix(domain, "."+name) {
			return name
		}
	}
	return ""
}

func validateDomainLabels(domain string, original string, label string) error {
	if strings.ContainsAny(domain, "/ ") {
		return fmt.Errorf("invalid %s %q", label, original)
	}
	labels := strings.Split(domain, ".")
	for _, part := range labels {
		if part == "" || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return fmt.Errorf("invalid %s %q", label, original)
		}
		for _, r := range part {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("invalid %s %q", label, original)
			}
		}
	}
	return nil
}
