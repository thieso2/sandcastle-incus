package images

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

type SyncRequest struct {
	SourceRef string
}

type SyncPlan struct {
	SourceRef   string `json:"sourceRef"`
	Alias       string `json:"alias"`
	Template    string `json:"template"`
	Description string `json:"description"`
}

type SyncResult struct {
	SyncPlan
	Fingerprint string `json:"fingerprint"`
	Action      string `json:"action"`
}

type Manager interface {
	SyncImage(context.Context, SyncPlan) (SyncResult, error)
}

func PlanSync(admin config.Admin, request SyncRequest) (SyncPlan, error) {
	if err := admin.Validate(); err != nil {
		return SyncPlan{}, err
	}
	source := strings.TrimSpace(request.SourceRef)
	if source == "" {
		return SyncPlan{}, fmt.Errorf("image source reference is required")
	}
	template, alias, err := templateAlias(admin, source)
	if err != nil {
		return SyncPlan{}, err
	}
	return SyncPlan{
		SourceRef:   source,
		Alias:       alias,
		Template:    template,
		Description: "Sandcastle " + template + " image synced from " + source,
	}, nil
}

func templateAlias(admin config.Admin, source string) (string, string, error) {
	baseStem := aliasStem(admin.Images.Base)
	aiStem := aliasStem(admin.Images.AI)
	sourceStem := aliasStem(source)
	switch sourceStem {
	case baseStem:
		return "base", strings.TrimSpace(admin.Images.Base), nil
	case aiStem:
		return "ai", strings.TrimSpace(admin.Images.AI), nil
	default:
		return "", "", fmt.Errorf("image source %q does not match configured Sandcastle base or AI image names", source)
	}
}

func aliasStem(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, ":"); ok {
		return before
	}
	return value
}
