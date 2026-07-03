package incusx

import (
	"fmt"
	"strings"
)

// applianceStartScript builds a shell script that reliably enables and starts a
// systemd unit inside a freshly-launched appliance CT.
//
// Fresh CTs race: systemd (PID 1) may still be booting when we exec, so a plain
// `systemctl enable --now` can be silently dropped — the unit ends up
// `disabled`/`dead` even though the deploy looked successful. So we:
//  1. wait for the CT's systemd to reach `running`/`degraded`,
//  2. enable + (re)start the unit,
//  3. poll until it reports `active`, failing the script if it never comes up.
//
// `prep` runs before the unit starts (e.g. creating config/state dirs).
func applianceStartScript(prep []string, unit string) string {
	lines := []string{"set -e"}
	lines = append(lines, prep...)
	lines = append(lines,
		// Wait (bounded) for systemd to finish booting before touching units.
		`i=0; while [ $i -lt 60 ]; do case "$(systemctl is-system-running 2>/dev/null)" in running|degraded) break;; esac; i=$((i+1)); sleep 1; done`,
		"systemctl daemon-reload",
		"systemctl enable "+unit,
		"systemctl restart "+unit,
		// Wait (bounded) for the unit to actually become active.
		fmt.Sprintf(`i=0; while [ $i -lt 60 ]; do [ "$(systemctl is-active %s 2>/dev/null)" = active ] && break; i=$((i+1)); sleep 1; done`, unit),
		"systemctl is-active "+unit,
	)
	return strings.Join(lines, "\n")
}
