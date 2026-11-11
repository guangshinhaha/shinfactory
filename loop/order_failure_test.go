package loop

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reasons below are the real failures of 2026-11-10 (#161): three
// test-first rejections that ended their orders on attempt 1, and a review
// stage that wrote no findings file. Each order vanished without a trace.
var incidentTestFirstCommits = map[string]string{
	"131": "116a36c1",
	"152": "6bf42d21",
	"135": "502dc336",
}

func nonTestFirstCommit(sha string) func(string) ([]commitFiles, error) {
	return func(string) ([]commitFiles, error) {
		return []commitFiles{{SHA: sha + "0000000000000000000000000000000000", Files: []string{"loop/x.go"}}}, nil
	}
}

// stops returns how many times the backlog stop adapter ran for order 42.
func (h *gateHarness) stops() int {
	n := 0
	for _, id := range h.ar.stopCalls {
		if id == "42" {
			n++
		}
	}
	return n
}

// nextUntilFailed runs stages until the order fails, which a rejected stage does
// once its attempts run out.
func (h *gateHarness) nextUntilFailed(t *testing.T) {
	t.Helper()
	for i := 0; i < 10; i++ {
		if o, ok := h.l.orderByID("42"); ok && o.Status == OrderStatusFailed {
			return
		}
		h.next(t)
	}
}

func (h *gateHarness) retrying(t *testing.T, stage string, attempts int) {
	t.Helper()
	o := h.order(t)
	if o.Status == OrderStatusFailed {
		t.Fatalf("order failed on attempt %d; want it sent back to %s", attempts, stage)
	}
	for _, s := range o.Stages {
		if s.TaskKey == stage && s.Status != StageStatusPending && s.Status != StageStatusActive {
			t.Fatalf("%s stage status = %q after attempt %d, want pending or active again", stage, s.Status, attempts)
		}
	}
	// The retry launches in the same cycle that rejected the stage, so the
	// ledger already counts the relaunch.
	if got := h.l.orderSpendOf("42").Attempts[stage]; got != attempts+1 {
		t.Fatalf("%s launches = %d after rejection %d, want %d counted", stage, got, attempts, attempts+1)
	}
	if n := h.stops(); n != 0 {
		t.Fatalf("stop adapter ran %d times while the order was still retrying", n)
	}
}

func TestTestFirstRejectionSendsExecuteBackWithTheExactReason(t *testing.T) {
	for id, sha := range incidentTestFirstCommits {
		t.Run("order "+id, func(t *testing.T) {
			h := evidenceHarness(t, "bug", "")
			branchCommits = nonTestFirstCommit(sha)
			h.next(t)
			h.retrying(t, "execute", 1)

			prompt := h.next(t) // the retry's prompt
			want := "bug fix has no test-first commit: commit " + sha + " changes non-test files before any commit that touches only tests"
			if !strings.Contains(prompt, want) {
				t.Fatalf("retry prompt lacks the exact reason %q:\n%s", want, prompt)
			}
			if !strings.Contains(prompt, "test-only commit") || !strings.Contains(prompt, "reset") {
				t.Fatalf("retry prompt does not say how to fix it (reset to a test-only commit, then the fix):\n%s", prompt)
			}
		})
	}
}

func TestEvidenceFailureStopsForAPersonWhenAttemptsRunOut(t *testing.T) {
	h := evidenceHarness(t, "bug", "")
	branchCommits = nonTestFirstCommit("116a36c1")
	max := h.l.config.Spend.MaxAttempts
	for i := 1; i < max; i++ {
		h.next(t)
		h.retrying(t, "execute", i)
	}
	h.next(t) // attempt max: the cap is spent
	h.failedWith(t, "attempts exhausted")
	if n := h.stops(); n != 1 {
		t.Fatalf("stop adapter ran %d times for the exhausted order, want exactly 1", n)
	}
}

func TestReviewWithNoFindingsFileIsRetriedOnceBeforeItFails(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t) // execute
	h.next(t) // review writes nothing
	h.retrying(t, "quality", 1)

	prompt := h.next(t) // the one retry; still no file
	if !strings.Contains(prompt, "review wrote no findings file") {
		t.Fatalf("review retry prompt does not carry the reason:\n%s", prompt)
	}
	h.failedWith(t, "second review without a file")
	if n := h.stops(); n != 1 {
		t.Fatalf("stop adapter ran %d times, want exactly 1 after the retry also failed", n)
	}
}

func TestReviewRetryThatWritesFindingsShips(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	h.next(t) // no file: retried
	h.review(t, 0, "none")
	h.next(t)
	if o, ok := h.l.orderByID("42"); ok && o.Status == OrderStatusFailed {
		t.Fatal("a review that wrote its file on the retry still failed the order")
	}
	if n := h.stops(); n != 0 {
		t.Fatalf("stop adapter ran %d times for an order that recovered", n)
	}
}

func TestStageCrashHandsTheOrderToAPerson(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.rt.sessions[0].complete("failed")
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.failedWith(t, "crashed stage")
	if n := h.stops(); n != 1 {
		t.Fatalf("stop adapter ran %d times for a crashed stage, want 1", n)
	}
}

// The invariant (#161): an order that leaves the active set ends in exactly one
// visible state: merged, retrying with the attempt counted, or handed to a
// person through the backlog stop adapter. A failure path that skipped the
// hand-off is what let four orders vanish. The scan is structural, so a failure
// path added later cannot skip it unnoticed: wherever code marks an order
// failed (the free failStage, EventStageFailed, Status = OrderStatusFailed), a
// hand-off call must follow in the same block. A hand-off in some other branch
// of the function does not count.
func TestEveryOrderFailurePathEndsVisible(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	failedSites := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name == "failStage" && fn.Recv == nil {
				continue // the pure state transition; its callers hand off
			}
			walkBlocks(fn.Body, func(block *ast.BlockStmt) {
				for i, stmt := range block.List {
					if !marksOrderFailed(stmt) {
						continue
					}
					failedSites++
					handed := false
					for _, later := range block.List[i:] {
						handed = handed || handsOff(later)
					}
					if !handed {
						t.Errorf("%s: %s marks an order failed at %s with no stopForPerson or stopFailedOrder after it in the same block, so the order can vanish",
							path, fn.Name.Name, fset.Position(stmt.Pos()))
					}
				}
			})
		}
	}
	if failedSites < 4 {
		t.Fatalf("found %d places that mark an order failed, want at least 4: the scan stopped matching the code", failedSites)
	}
}

// walkBlocks visits fn's blocks but not function literals' bodies: a failure
// marked inside a closure belongs to the statement that runs the closure.
func walkBlocks(root ast.Node, visit func(*ast.BlockStmt)) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BlockStmt:
			visit(v)
		}
		return true
	})
}

func marksOrderFailed(stmt ast.Stmt) bool {
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "failStage" {
				found = true
			}
		case *ast.SelectorExpr:
			if v.Sel.Name == "EventStageFailed" {
				found = true
			}
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Status" {
					for _, rhs := range v.Rhs {
						if id, ok := rhs.(*ast.Ident); ok && id.Name == "OrderStatusFailed" {
							found = true
						}
					}
				}
			}
		}
		return true
	})
	return found
}

func handsOff(stmt ast.Stmt) bool {
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "stopForPerson" || sel.Sel.Name == "stopFailedOrder") {
				found = true
			}
		}
		return true
	})
	return found
}

func TestDispatchFailureHandsTheOrderToAPerson(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.rt.dispatchErr = errors.New("worker-start refused")
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.failedWith(t, "dispatch failed")
	if n := h.stops(); n != 1 {
		t.Fatalf("stop adapter ran %d times for a dispatch failure, want 1", n)
	}
}

// A verdict stage (quality, verify) has no work to fix, so its stage_fail is
// not retried: the order goes to a person at once.
func TestQualityRejectionIsNotRetried(t *testing.T) {
	h := newGateHarness(t, "none", "execute", "quality")
	h.next(t) // execute ships
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	cook := &cookHandle{
		cookIdentity: cookIdentity{orderID: "42", stageIndex: 1, stage: Stage{TaskKey: "quality", Skill: "quality"}},
		worktreeName: "w",
	}
	if err := h.l.rejectStage(context.Background(), cook, "Rejected: Done when missed"); err != nil {
		t.Fatal(err)
	}
	h.failedWith(t, "quality rejection")
	if n := h.stops(); n != 1 {
		t.Fatalf("stop adapter ran %d times, want 1", n)
	}
}

// With both spend caps off a rejected stage still has a bound.
func TestRetriesAreBoundedWhenSpendCapsAreOff(t *testing.T) {
	h := evidenceHarness(t, "bug", "")
	h.l.config.Spend.MaxAttempts = 0
	h.l.config.Spend.MaxCostUSD = 0
	branchCommits = nonTestFirstCommit("116a36c1")
	h.nextUntilFailed(t)
	h.failedWith(t, "uncapped retries")
	if got := h.l.orderSpendOf("42").Attempts["execute"]; got > fallbackRetryAttempts {
		t.Fatalf("execute launched %d times, want at most %d", got, fallbackRetryAttempts)
	}
}
