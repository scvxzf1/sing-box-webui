package supervisor

import "testing"

func TestStateValidity(t *testing.T) {
	t.Parallel()
	for _, state := range []State{StateStopped, StateStarting, StateRunning, StateStopping, StateFailed} {
		if !state.Valid() {
			t.Fatalf("state %q should be valid", state)
		}
	}
	if State("unknown").Valid() {
		t.Fatal("unknown state should not be valid")
	}
}
