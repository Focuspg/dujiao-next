package zovocard

import "testing"

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"type":"gpt_direct.completed","event_id":"gpt_direct.completed:981","order_id":981,"status":"completed"}`)
	sig := Sign("whsec_test", body)
	if err := VerifySignature("whsec_test", body, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifySignature("whsec_test", body, "  "+sig+"\n"); err != nil {
		t.Fatalf("whitespace should be tolerated: %v", err)
	}
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-3] = 'X'
	for name, tc := range map[string]struct {
		secret string
		body   []byte
		sig    string
	}{
		"tampered body": {"whsec_test", tampered, sig},
		"wrong secret":  {"whsec_other", body, sig},
		"empty sig":     {"whsec_test", body, ""},
		"not hex":       {"whsec_test", body, "zz"},
		"short":         {"whsec_test", body, sig[:10]},
	} {
		if err := VerifySignature(tc.secret, tc.body, tc.sig); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if err := VerifySignature("", body, sig); err == nil {
		t.Fatal("missing secret must fail closed")
	}
}

func TestParseDirectEventAndDedupKey(t *testing.T) {
	body := []byte(`{"type":"gpt_direct.progress","order_id":7,"status":"processing","stage":"paying","occurred_at":"2026-07-28T10:05:00Z"}`)
	evt, ok, err := ParseDirectEvent(body)
	if err != nil || !ok {
		t.Fatalf("parse: %v %v", ok, err)
	}
	k1 := evt.DedupKey(BodyHash(body))
	if k1 != evt.DedupKey(BodyHash(body)) || k1 == "" {
		t.Fatal("dedup key must be stable")
	}
	evt.EventID = "gpt_direct.progress:7:3"
	if evt.DedupKey("x") != "gpt_direct.progress:7:3" {
		t.Fatal("event_id must win")
	}
	if _, ok, _ := ParseDirectEvent([]byte(`{"event":"card_transaction","auth_id":"1"}`)); ok {
		t.Fatal("card events are not direct events")
	}
	if got := (DirectEvent{}).DedupKey("abc"); got != "sha256:abc" {
		t.Fatalf("fallback = %s", got)
	}
}

func TestOutcomeMapping(t *testing.T) {
	cases := map[string]Outcome{
		"queued": OutcomePending, "awaiting_card": OutcomePending, "funding_pending": OutcomePending,
		"completed": OutcomeSucceeded, "COMPLETED": OutcomeSucceeded,
		"declined": OutcomeFailed, "failed_precharge": OutcomeFailed, "failed": OutcomeFailed,
		"cancelled": OutcomeCancelled, "requires_action": OutcomeUnrecognized, "plus_paid": OutcomeUnrecognized, "": OutcomeUnrecognized,
	}
	for status, want := range cases {
		if got := OutcomeOf(status); got != want {
			t.Errorf("%q: %s, want %s", status, got, want)
		}
	}
	if !(Order{Status: "failed_precharge"}).FailedBeforeCharge() || (Order{Status: "declined"}).FailedBeforeCharge() {
		t.Fatal("before-charge detection wrong")
	}
	if !(Order{Status: "queued"}).Cancellable() || (Order{Status: "processing"}).Cancellable() {
		t.Fatal("cancellable detection wrong")
	}
}
