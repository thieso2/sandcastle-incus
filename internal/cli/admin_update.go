package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/update"
)

// componentUnits maps a global component kind to the service units its
// update restarts.
var componentUnits = map[string][]string{
	meta.KindAuthApp: {"sandcastle-auth-app.service"},
	meta.KindBroker:  {"sandcastle-broker.service"},
}

// newAdminUpdateCommand is the operator-facing `sc-adm update` (#124 §4):
// by default it updates the global components (auth-app, broker) from the
// wanted GitHub release; --check shows the fleet table from the stamps;
// --tenants/--all-tenants force-roll tenant sidecars (operator override).
func newAdminUpdateCommand(config commandConfig) *cobra.Command {
	var check, yes, allTenants bool
	var pin string
	var tenants []string
	command := &cobra.Command{
		Use:   "update",
		Short: "Update the deployment's global components (and optionally tenant sidecars) from a GitHub release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			creator := config.tenantCreator
			components, err := creator.ListBinaryVersions()
			if err != nil {
				return err
			}
			prefix := naming.NormalizeV2Prefix(config.adminConfig.IncusProjectPrefix)
			scoped := filterInstallComponents(components, prefix, config.adminConfig.InfrastructureProject)

			checker := &update.Checker{}
			release, releaseErr := checker.ResolveRelease(ctx, update.NormalizeTag(pin))

			if check {
				renderFleetTable(config, scoped, release.TagName, releaseErr)
				return nil
			}
			if releaseErr != nil {
				return fmt.Errorf("resolve release: %w", releaseErr)
			}

			targets, err := selectUpdateTargets(scoped, tenants, allTenants)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				return fmt.Errorf("no updatable components found for prefix %q — is this the right admin remote?", prefix)
			}
			fmt.Fprintf(config.stdout, "Updating to %s:\n", release.TagName)
			for _, t := range targets {
				fmt.Fprintf(config.stdout, "  %s %s/%s\n", t.Kind, t.Project, t.Instance)
			}
			if !yes {
				ok, err := confirmMissingYesNamed(config, "Proceed?",
					"refusing to update without confirmation; pass --yes to proceed non-interactively", "update canceled")
				if err != nil || !ok {
					return err
				}
			}

			// Download once per architecture (appliances are linux; the arch
			// comes from each instance, not from this workstation).
			binaries := map[string][]byte{}
			fetchFor := func(arch string) ([]byte, error) {
				if arch == "" {
					// Never guess: from a darwin/arm64 workstation a GOARCH
					// fallback would push an arm64 binary into an amd64 appliance.
					return nil, fmt.Errorf("instance architecture unknown — cannot pick a release asset")
				}
				if b, ok := binaries[arch]; ok {
					return b, nil
				}
				fmt.Fprintf(config.stdout, "Downloading %s...\n", update.AssetName("linux", arch))
				b, err := checker.FetchBinary(ctx, release, "linux", arch)
				if err != nil {
					return nil, err
				}
				binaries[arch] = b
				return b, nil
			}

			// Per-component apply; keep going on failure and report a summary —
			// the command is idempotent, so a re-run repairs a partial update.
			failed := 0
			for _, t := range targets {
				err := func() error {
					binary, err := fetchFor(t.Architecture)
					if err != nil {
						return err
					}
					if t.Kind == meta.KindSidecar {
						_, err = creator.UpdateTenantSidecar(prefix, t.Tenant, binary, release.TagName)
						return err
					}
					return creator.UpdateApplianceBinary(t.Project, t.Instance, binary, release.TagName, componentUnits[t.Kind]...)
				}()
				if err != nil {
					failed++
					fmt.Fprintf(config.stdout, "  FAIL %s %s/%s: %v\n", t.Kind, t.Project, t.Instance, err)
					continue
				}
				fmt.Fprintf(config.stdout, "  ok   %s %s/%s → %s\n", t.Kind, t.Project, t.Instance, release.TagName)
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d components failed to update — re-run to repair", failed, len(targets))
			}
			fmt.Fprintf(config.stdout, "All %d components updated to %s.\n", len(targets), release.TagName)
			return nil
		},
	}
	command.Flags().BoolVar(&check, "check", false, "show the fleet version table; change nothing")
	command.Flags().BoolVar(&yes, "yes", false, "apply without prompting")
	command.Flags().StringVar(&pin, "version", "", "release tag to install (vX.Y.Z; default: latest); an older tag rolls back")
	command.Flags().StringSliceVar(&tenants, "tenants", nil, "also force-roll these tenants' sidecars (normally tenant-managed via sc update)")
	command.Flags().BoolVar(&allTenants, "all-tenants", false, "also force-roll every tenant sidecar")
	return command
}

// filterInstallComponents keeps the fleet rows belonging to THIS install:
// several sandcastles can share one Incus host (--prefix), and updating a
// neighbour install's appliances would be a surprise.
func filterInstallComponents(components []incusx.ComponentVersion, prefix, infraProject string) []incusx.ComponentVersion {
	infraProject = strings.TrimSpace(infraProject)
	authAppProjects := map[string]bool{
		prefix + "-infra": true,
		// Legacy deploys (sc-adm auth-app deploy without --project) live in
		// "infrastructure" regardless of prefix.
		incusx.AuthAppDefaultProject: true,
	}
	if infraProject != "" {
		authAppProjects[infraProject] = true
	}
	var scoped []incusx.ComponentVersion
	for _, c := range components {
		switch c.Kind {
		case meta.KindAuthApp:
			if authAppProjects[c.Project] {
				scoped = append(scoped, c)
			}
		case meta.KindBroker:
			if c.Project == prefix+"-broker" || c.Project == incusx.BrokerProjectName {
				scoped = append(scoped, c)
			}
		case meta.KindSidecar:
			if c.Tenant != "" && c.Project == prefix+"-"+c.Tenant {
				scoped = append(scoped, c)
			}
		}
	}
	return scoped
}

// selectUpdateTargets picks what a run touches: all global components, plus
// the requested sidecars (--tenants names must exist; --all-tenants takes
// every sidecar in the install).
func selectUpdateTargets(scoped []incusx.ComponentVersion, tenants []string, allTenants bool) ([]incusx.ComponentVersion, error) {
	wanted := map[string]bool{}
	for _, t := range tenants {
		if s := strings.TrimSpace(t); s != "" {
			wanted[s] = true
		}
	}
	var targets []incusx.ComponentVersion
	found := map[string]bool{}
	for _, c := range scoped {
		switch c.Kind {
		case meta.KindAuthApp, meta.KindBroker:
			targets = append(targets, c)
		case meta.KindSidecar:
			if allTenants || wanted[c.Tenant] {
				targets = append(targets, c)
				found[c.Tenant] = true
			}
		}
	}
	var missing []string
	for t := range wanted {
		if !found[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no sidecar found for tenant(s): %s", strings.Join(missing, ", "))
	}
	return targets, nil
}

// renderFleetTable prints the stamp-based fleet table (#124 §4): one cheap
// listing, works for stopped instances; sidecars are marked tenant-managed.
func renderFleetTable(config commandConfig, components []incusx.ComponentVersion, latestTag string, releaseErr error) {
	if releaseErr != nil {
		fmt.Fprintf(config.stderr, "note: could not reach GitHub for the latest release: %v\n", releaseErr)
	}
	w := tabwriter.NewWriter(config.stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "COMPONENT\tPROJECT/INSTANCE\tVERSION\tSTATE\tNOTES")
	for _, c := range components {
		notes := fleetNotes(c, latestTag)
		name := c.Kind
		if c.Kind == meta.KindSidecar {
			name = "sidecar (" + c.Tenant + ")"
		}
		fmt.Fprintf(w, "%s\t%s/%s\t%s\t%s\t%s\n", name, c.Project, c.Instance, orUnknown(c.BinaryVersion), c.Status, notes)
	}
	w.Flush()
	if latestTag != "" {
		fmt.Fprintf(config.stdout, "\nLatest release: %s\n", latestTag)
	}
	fmt.Fprintln(config.stdout, "The image-builder appliance carries no sandcastle binary (podman-based) — nothing to update there.")
}

func fleetNotes(c incusx.ComponentVersion, latestTag string) string {
	var notes []string
	if c.TenantManaged {
		notes = append(notes, "tenant-managed")
	}
	switch {
	case c.BinaryVersion == "":
		// Missing stamp (pre-#124 deploy): unknown, treated as outdated;
		// the first update self-heals the stamp.
		notes = append(notes, "outdated (no stamp)")
	case latestTag != "" && update.IsNewer(latestTag, c.BinaryVersion):
		notes = append(notes, "outdated")
	case latestTag != "":
		notes = append(notes, "current")
	}
	return strings.Join(notes, ", ")
}
