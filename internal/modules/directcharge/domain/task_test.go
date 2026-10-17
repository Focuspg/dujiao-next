package domain

import "testing"

func TestTerminalStatesOnlyMoveToReview(t *testing.T) {
	for _, s := range []string{TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed, TaskClosed} {
		if !IsTerminal(s) || HoldsAccountLock(s) {
			t.Fatalf("%s should be terminal and release the account", s)
		}
		for _, to := range []string{TaskReady, TaskSubmitting, TaskProcessing, TaskSucceeded} {
			if CanTransition(s, to) {
				t.Errorf("%s -> %s must not be allowed", s, to)
			}
		}
		if !CanTransition(s, TaskReview) {
			t.Errorf("%s -> review must be allowed", s)
		}
	}
}

func TestUnknownSubmitCannotStartNewAttemptDirectly(t *testing.T) {
	if CanTransition(TaskSubmitUnknown, TaskSubmitting) {
		t.Fatal("submit_unknown must be resolved before a new submit")
	}
	if !MayHaveUpstreamAttempt(TaskSubmitUnknown) || !MayHaveUpstreamAttempt(TaskProcessing) {
		t.Fatal("open attempt detection wrong")
	}
	if MayHaveUpstreamAttempt(TaskReady) || MayHaveUpstreamAttempt(TaskWaitingCredential) {
		t.Fatal("ready/waiting have no open attempt")
	}
}

func TestUnpaidTaskCannotSubmit(t *testing.T) {
	if CanTransition(TaskAwaitingPayment, TaskSubmitting) || CanTransition(TaskAwaitingPayment, TaskProcessing) {
		t.Fatal("unpaid task must not reach upstream")
	}
}

func TestEveryStatusHasTransitions(t *testing.T) {
	all := []string{TaskAwaitingPayment, TaskReady, TaskPreflighting, TaskWaitingCredential, TaskSubmitting, TaskSubmitUnknown,
		TaskProcessing, TaskRequiresAction, TaskReview, TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed, TaskClosed}
	for _, s := range all {
		if len(taskTransitions[s]) == 0 {
			t.Errorf("%s has no transitions", s)
		}
		for _, to := range taskTransitions[s] {
			if _, ok := taskTransitions[to]; !ok {
				t.Errorf("%s -> unknown status %s", s, to)
			}
		}
	}
}

func TestClientRequestIDFitsUpstreamLimit(t *testing.T) {
	id := BuildClientRequestID("prod01", 4294967295, 99)
	if len(id) > 80 || id != "rc-prod01-4294967295-99" {
		t.Fatalf("unexpected id %q", id)
	}
}

func TestSpecCredentialModes(t *testing.T) {
	s := &Spec{}
	if !s.AllowsCredentialMode(CredentialModeSession) || s.AllowsCredentialMode(CredentialModeAccessToken) {
		t.Fatal("default must allow session only")
	}
	s.CredentialModes = []string{CredentialModeAccessToken}
	if s.AllowsCredentialMode(CredentialModeSession) || !s.AllowsCredentialMode(CredentialModeAccessToken) {
		t.Fatal("configured modes not respected")
	}
}
