package machine

// Action is a machine lifecycle verb. The v1 lifecycle planner is gone; v2
// applies the action directly against the freeform Incus instance, so only the
// vocabulary survives.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionDelete  Action = "delete"
)
