package background

import (
	"context"
	"fmt"

	"github.com/qmuntal/stateless"
)

// ── Background Task FSM ─────────────────────────────────────────
// Uses qmuntal/stateless to enforce background task transitions:
//
//   QUEUED ──start──▶ RUNNING ──complete──▶ COMPLETED
//                        │
//                        ├──fail──▶ FAILED
//                        │
//                        └──cancel──▶ CANCELLED
//
//   QUEUED ──cancel──▶ CANCELLED

const (
	bgTriggerStart    = "start"
	bgTriggerComplete = "complete"
	bgTriggerFail     = "fail"
	bgTriggerCancel   = "cancel"
)

// NewTaskFSM creates a stateless FSM for a background task.
func NewTaskFSM(initialStatus TaskStatus) *stateless.StateMachine {
	sm := stateless.NewStateMachineWithExternalStorage(
		func(_ context.Context) (stateless.State, error) { return initialStatus, nil },
		func(_ context.Context, s stateless.State) error {
			initialStatus = s.(TaskStatus)
			return nil
		},
		stateless.FiringQueued,
	)

	sm.Configure(StatusQueued).
		Permit(bgTriggerStart, StatusRunning).
		Permit(bgTriggerCancel, StatusCancelled)

	sm.Configure(StatusRunning).
		Permit(bgTriggerComplete, StatusCompleted).
		Permit(bgTriggerFail, StatusFailed).
		Permit(bgTriggerCancel, StatusCancelled)

	sm.Configure(StatusCompleted) // terminal
	sm.Configure(StatusFailed)    // terminal
	sm.Configure(StatusCancelled) // terminal

	return sm
}

// FireTaskTransition fires the trigger that moves to targetStatus.
func FireTaskTransition(sm *stateless.StateMachine, targetStatus TaskStatus) error {
	trigger, err := bgTriggerFor(targetStatus)
	if err != nil {
		return err
	}
	return sm.Fire(trigger)
}

func bgTriggerFor(target TaskStatus) (string, error) {
	switch target {
	case StatusRunning:
		return bgTriggerStart, nil
	case StatusCompleted:
		return bgTriggerComplete, nil
	case StatusFailed:
		return bgTriggerFail, nil
	case StatusCancelled:
		return bgTriggerCancel, nil
	default:
		return "", fmt.Errorf("no trigger for background task status %q", target)
	}
}

// TaskFSMGraph returns a DOT graph for debugging.
func TaskFSMGraph() string {
	sm := NewTaskFSM(StatusQueued)
	return sm.ToGraph()
}
