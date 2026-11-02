package promise

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// TestTermsAcceptTheLongestPolicyVersion pins the exact policyVersion budget.
func TestTermsAcceptTheLongestPolicyVersion(t *testing.T) {
	tm := terms()
	tm.PolicyVersion = strings.Repeat("v", shared.MaxID)
	if err := validateTerms(tm); err != nil {
		t.Fatalf("a MaxID policy version is valid: %v", err)
	}
}

// TestMakeAcceptsTheLongestBookID pins the exact bookId budget: two MaxID ids
// joined by one colon.
func TestMakeAcceptsTheLongestBookID(t *testing.T) {
	in := making()
	in.BookID = strings.Repeat("b", 2*shared.MaxID+1)
	if _, _, err := Make(in, t0); err != nil {
		t.Fatalf("the longest bookId is valid: %v", err)
	}
}

// TestConfirmAcceptsTheLargestGroupNo pins the groupNo upper bound.
func TestConfirmAcceptsTheLargestGroupNo(t *testing.T) {
	if _, err := provisional(t).Confirm("ord-1", shared.MaxLines, t0.Add(time.Minute)); err != nil {
		t.Fatalf("groupNo MaxLines is valid: %v", err)
	}
}

// TestEveryTransitionBumpsTheVersion: each accepted transition of the promise
// increments its version by exactly one.
func TestEveryTransitionBumpsTheVersion(t *testing.T) {
	unkeepable := atRisk(t)
	if _, err := unkeepable.MarkUnkeepable(t0.Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	eq(t, "version after MarkUnkeepable", unkeepable.Version(), 4)

	judged := confirmed(t)
	if _, err := judged.Judge(Verdict{DeliveredAt: latest}, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	eq(t, "version after Judge", judged.Version(), 3)

	cancelled := confirmed(t)
	if _, err := cancelled.Cancel("CUSTOMER_REQUEST", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	eq(t, "version after Cancel", cancelled.Version(), 3)
}

// TestRestoreNumbersRevisionsFromOne pins the numbering contract of the
// revision log: the first stored revision is number one.
func TestRestoreNumbersRevisionsFromOne(t *testing.T) {
	p := confirmed(t)
	if _, err := p.Revise(revising(), t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	eq(t, "revisions", len(p.Revisions()), 1)
	if _, err := Restore(p.Snapshot()); err != nil {
		t.Fatalf("a revision numbered one restores: %v", err)
	}
}

// TestOnlyTheThreeCausesAreValid pins the judgement causes.
func TestOnlyTheThreeCausesAreValid(t *testing.T) {
	eq(t, "LATE", CauseLate.valid(), true)
	eq(t, "LOST", CauseLost.valid(), true)
	eq(t, "RETURNED", CauseReturned.valid(), true)
	eq(t, "an unknown cause", Cause("MYSTERY").valid(), false)
}
