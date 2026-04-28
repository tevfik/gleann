package a2a

import (
	"context"
	"fmt"

	"github.com/qmuntal/stateless"
)

// ── A2A Task FSM ─────────────────────────────────────────────────
// Uses qmuntal/stateless to enforce the A2A task lifecycle:
//
//   SUBMITTED ──start──▶ WORKING ──complete──▶ COMPLETED
//                            │
//                            ├──fail──▶ FAILED
//                            │
//                            ├──cancel──▶ CANCELED
//                            │
//                            └──require_input──▶ INPUT_REQUIRED ──resume──▶ WORKING

// FSM triggers for task state transitions.
const (
	triggerStart        = "start"
	triggerComplete     = "complete"
	triggerFail         = "fail"
	triggerCancel       = "cancel"
	triggerRequireInput = "require_input"
	triggerResume       = "resume"
)

// NewTaskFSM creates a stateless FSM for a task with the given initial state.
func NewTaskFSM(initialState TaskState) *stateless.StateMachine {
	sm := stateless.NewStateMachineWithExternalStorage(
		func(_ context.Context) (stateless.State, error) { return initialState, nil },
		func(_ context.Context, s stateless.State) error {
			initialState = s.(TaskState)
			return nil
		},
		stateless.FiringQueued,
	)

	sm.Configure(TaskStateSubmitted).
		Permit(triggerStart, TaskStateWorking)

	sm.Configure(TaskStateWorking).
		Permit(triggerComplete, TaskStateCompleted).
		Permit(triggerFail, TaskStateFailed).
		Permit(triggerCancel, TaskStateCanceled).
		Permit(triggerRequireInput, TaskStateInputRequired)

	sm.Configure(TaskStateInputRequired).
		Permit(triggerResume, TaskStateWorking).
		Permit(triggerCancel, TaskStateCanceled).
		Permit(triggerFail, TaskStateFailed)

	sm.Configure(TaskStateCompleted) // terminal
	sm.Configure(TaskStateFailed)    // terminal
	sm.Configure(TaskStateCanceled)  // terminal

	return sm
}

// TaskFSMTransition fires the appropriate trigger for moving to targetState.
// Returns an error if the transition is invalid from the current state.
func TaskFSMTransition(sm *stateless.StateMachine, targetState TaskState) error {
	trigger, err := triggerForA2ATarget(targetState)
	if err != nil {
		return err
	}
	return sm.Fire(trigger)
}

func triggerForA2ATarget(target TaskState) (string, error) {
	switch target {
	case TaskStateWorking:
		return triggerStart, nil
	case TaskStateCompleted:
		return triggerComplete, nil
	case TaskStateFailed:
		return triggerFail, nil
	case TaskStateCanceled:
		return triggerCancel, nil
	case TaskStateInputRequired:
		return triggerRequireInput, nil
	default:
		return "", fmt.Errorf("no trigger for target state %q", target)
	}
}

// TaskFSMResume fires the resume trigger to move from INPUT_REQUIRED → WORKING.
func TaskFSMResume(sm *stateless.StateMachine) error {
	return sm.Fire(triggerResume)
}

// TaskFSMGraph returns a DOT graph representation for debugging.
func TaskFSMGraph() string {
	sm := NewTaskFSM(TaskStateSubmitted)
	return sm.ToGraph()
}
