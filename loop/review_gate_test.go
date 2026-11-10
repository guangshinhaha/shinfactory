package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/internal/taskreg"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
	"github.com/guangshinhaha/shinfactory/skill"
)

const fixtureHead = "abc123def456"

type gateHarness struct {
	*landingHarness
	n       int // sessions completed so far
	pending []func()
}

// newGateHarness builds a PR-mode loop whose order runs the given stages.
func newGateHarness(t *testing.T, tier string, keys ...string) *gateHarness {
	t.Helper()
	old := headSHA
	headSHA = func(string) (string, error) { return fixtureHead, nil }
	t.Cleanup(func() { headSHA = old })

	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	order := Order{ID: "42", Title: "Gate fixture", Rationale: "builder reasoning", Tier: tier, Status: OrderStatusActive}
	for _, k := range keys {
		order.Stages = append(order.Stages, Stage{TaskKey: k, Skill: k, Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending})
	}
	ordersPath := writeTestOrders(t, runtimeDir, OrdersFile{Orders: []Order{order}})
	reg := taskreg.NewFromSkills([]skill.SkillMeta{
		{Name: "schedule", Path: "/skills/schedule", Frontmatter: skill.Frontmatter{Schedule: "x"}},
		{Name: "execute", Path: "/skills/execute", Frontmatter: skill.Frontmatter{Schedule: "x"}},
		{Name: "quality", Path: "/skills/quality", Frontmatter: skill.Frontmatter{Schedule: "x"}},
		{Name: "verify", Path: "/skills/verify", Frontmatter: skill.Frontmatter{Schedule: "x"}},
		{Name: "reflect", Path: "/skills/reflect", Frontmatter: skill.Frontmatter{Schedule: "x"}},
	})
	lh := &landingHarness{rt: newMockRuntime(), wt: &fakeWorktree{}, ar: &fakeAdapterRunner{}, ln: &fakeLander{}}
	cfg := config.DefaultConfig()
	cfg.Mode = "auto"
	cfg.Landing.Mode = config.LandingModePR
	lh.l = New(projectDir, "noodle", cfg, Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": lh.rt},
		Worktree:   lh.wt,
		Adapter:    lh.ar,
		Lander:     lh.ln,
		OrdersFile: ordersPath,
		Mise:       &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{{ID: "1", Title: "t", Status: "open"}}}},
		Monitor:    fakeMonitor{},
		Registry:   reg,
		Now:        time.Now,
	})
	return &gateHarness{landingHarness: lh}
}

// next dispatches the next stage and completes it, returning its prompt.
func (h *gateHarness) next(t *testing.T) string {
	t.Helper()
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("spawn cycle: %v", err)
	}
	if len(h.rt.sessions) != h.n+1 {
		t.Fatalf("sessions = %d, want %d (nothing dispatched)", len(h.rt.sessions), h.n+1)
	}
	prompt := h.rt.calls[h.n].Prompt
	// The stage's agent writes its file while it runs, after dispatch.
	for _, w := range h.pending {
		w()
	}
	h.pending = nil
	h.rt.sessions[h.n].complete("completed")
	h.n++
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("completion cycle: %v", err)
	}
	h.settleEvidence(t)
	return prompt
}

func (h *gateHarness) order(t *testing.T) Order {
	t.Helper()
	o, ok := h.l.orderByID("42")
	if !ok {
		t.Fatal("order 42 missing from orders.json")
	}
	return o
}

// write queues a gate file for the stage that dispatches next.
func (h *gateHarness) write(t *testing.T, round int, name, body string) {
	h.pending = append(h.pending, func() { h.writeNow(t, round, name, body) })
}

func (h *gateHarness) writeNow(t *testing.T, round int, name, body string) {
	t.Helper()
	dir := h.l.gateDir("42", round)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *gateHarness) review(t *testing.T, round int, findings string) {
	h.write(t, round, reviewFileName, "Covered: whole diff, high level, correctness and blast radius\nFindings: "+findings+"\n")
}

func (h *gateHarness) verdict(t *testing.T, round int, verdict, head string) {
	b, _ := json.Marshal(map[string]string{"head_sha": head, "verdict": verdict, "evidence": "ran the fixture"})
	h.write(t, round, verifyFileName, string(b))
}

func (h *gateHarness) failedWith(t *testing.T, want string) {
	t.Helper()
	o := h.order(t)
	if o.Status != OrderStatusFailed {
		t.Fatalf("order status = %q, want failed (%s)", o.Status, want)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("PR merged although the order failed")
	}
}

func TestHighOrderRunsReviewAndVerifyBeforeShip(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")

	if p := h.next(t); strings.Contains(p, "Fix round") {
		t.Fatalf("first execute prompt mentions a fix round: %s", p)
	}
	if len(h.ln.pushes) != 1 || len(h.ln.merged) != 0 {
		t.Fatalf("after execute: pushes=%d merged=%d, want 1 draft push and no merge", len(h.ln.pushes), len(h.ln.merged))
	}

	h.review(t, 0, "none")
	qp := h.next(t)
	if !strings.Contains(qp, "round-0/review.md") {
		t.Fatalf("review prompt does not name its findings file: %s", qp)
	}
	if len(h.ln.merged) != 0 || len(h.wt.merged) != 0 {
		t.Fatal("something merged before verify")
	}

	h.verdict(t, 0, VerdictPass, fixtureHead)
	vp := h.next(t)
	if !strings.Contains(vp, "round-0/verify.json") {
		t.Fatalf("verify prompt does not name its verdict file: %s", vp)
	}
	if strings.Contains(vp, "builder reasoning") {
		t.Fatalf("verify prompt carries the builder's rationale: %s", vp)
	}
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want exactly 1 after verify passed", len(h.ln.merged))
	}
}

func TestVerifyFailSendsOrderBackToExecuteOnSameBranch(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictFail, fixtureHead)
	h.next(t)

	o := h.order(t)
	if o.Round != 1 {
		t.Fatalf("round = %d, want 1", o.Round)
	}
	// The same cycle already redispatched execute; later stages wait behind it.
	if o.Stages[0].Status != StageStatusActive || o.Stages[1].Status != StageStatusPending || o.Stages[2].Status != StageStatusPending {
		t.Fatalf("stage statuses after FAIL = %v, want execute rerunning and review, verify pending", o.Stages)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("PR merged after a FAIL")
	}

	// The fix round reruns execute in the same worktree and reviews again.
	p := h.next(t)
	if !strings.Contains(p, "Fix round 1 of 2") || !strings.Contains(p, "round-0/verify.json") {
		t.Fatalf("fix-round execute prompt = %s", p)
	}
	if h.rt.calls[3].WorktreePath != h.rt.calls[0].WorktreePath {
		t.Fatal("fix round did not reuse the order's worktree")
	}
	h.review(t, 1, "none")
	h.next(t)
	h.verdict(t, 1, VerdictPassNotes, fixtureHead)
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1 after the fix round passed", len(h.ln.merged))
	}
}

func TestThirdRoundStopsTheOrderAndFlagsAPerson(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	for round := 0; round < 2; round++ {
		h.next(t) // execute
		h.review(t, round, "\n- the thing is broken")
		h.next(t) // quality finds issues -> back to execute
		if got := h.order(t).Round; got != round+1 {
			t.Fatalf("round after findings = %d, want %d", got, round+1)
		}
	}
	h.next(t) // execute, third time
	h.review(t, 2, "\n- still broken")
	h.next(t) // quality finds issues again: no rounds left

	o := h.order(t)
	if o.Status != OrderStatusFailed {
		t.Fatalf("order status = %q, want failed after round 2 still had findings", o.Status)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("PR merged although the order stopped")
	}
	orders, err := readOrders(h.l.deps.OrdersFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders.ActionNeeded) != 1 || !strings.Contains(orders.ActionNeeded[0], "fix rounds") {
		t.Fatalf("action_needed = %v, want one line flagging the stopped order", orders.ActionNeeded)
	}
}

func TestEmptyReviewCountsAsNotRun(t *testing.T) {
	cases := map[string]string{
		"missing":     "",
		"empty":       " \n",
		"no coverage": "Findings: none\n",
		"no findings": "Covered: the whole diff\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newGateHarness(t, "medium", "execute", "quality")
			h.next(t)
			if name != "missing" {
				h.write(t, 0, reviewFileName, body)
			}
			h.next(t)
			h.failedWith(t, "review not run")
		})
	}
}

func TestListedFindingsUnderNoneStillSendBack(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	h.write(t, 0, reviewFileName, "Covered: diff\nFindings: none\n- but this is broken\n")
	h.next(t)
	if got := h.order(t).Round; got != 1 {
		t.Fatalf("round = %d, want 1: a listed finding must not pass as clean", got)
	}
}

func TestVerdictForAnotherHeadFails(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, "0000stale")
	h.next(t)
	h.failedWith(t, "stale verdict")
}

func TestStagesThatCouldShipUnreviewedWorkFail(t *testing.T) {
	cases := []struct {
		tier string
		keys []string
	}{
		{"high", []string{"execute", "quality"}},           // no verify
		{"high", []string{"execute"}},                      // nothing
		{"medium", []string{"execute"}},                    // no review
		{"", []string{"execute", "reflect"}},               // unset tier counts as medium
		{"medium", []string{"quality", "execute"}},         // review before the last build
		{"high", []string{"execute", "verify", "quality"}}, // verify before review
	}
	for _, c := range cases {
		h := newGateHarness(t, c.tier, c.keys...)
		h.next(t)
		h.failedWith(t, strings.Join(c.keys, ","))
	}
}

func TestTierNoneShipsWithoutReview(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1: tier none has no gate", len(h.ln.merged))
	}
}

func TestMediumTierNeedsNoVerify(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1 after a clean review", len(h.ln.merged))
	}
}

func TestStageAfterVerifyCannotShipANewHead(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify", "reflect")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	headSHA = func(string) (string, error) { return "newcommit", nil } // reflect committed
	h.next(t)
	h.failedWith(t, "head moved after verify")
}

// realGateHarness runs the gate against the real lander: a stub gh on PATH,
// a real git worktree and a bare remote. Nothing here reaches GitHub.
func realGateHarness(t *testing.T) (*gateHarness, string) {
	t.Helper()
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	headSHA = gitHeadSHA
	h.l.deps.Lander = ghLander{}
	branch := h.l.cookWorktreeName("42", 0, "")
	t.Setenv("STUB_BRANCH", branch)
	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	work := h.l.worktreePath(branch)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "init", "-b", branch)
	gitIn(t, work, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-m", "execute")
	return h, work
}

func TestHighOrderThroughStubGHMergesOnlyAfterVerifyAtHead(t *testing.T) {
	logPath := stubGH(t)
	merges := func() int {
		data, _ := os.ReadFile(logPath)
		return strings.Count(string(data), "pr merge")
	}

	// A verdict for another head never merges.
	h, _ := realGateHarness(t)
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	if merges() != 0 {
		t.Fatal("gh pr merge ran before verify")
	}
	h.verdict(t, 0, VerdictPass, "deadbeef")
	h.next(t)
	h.failedWith(t, "verdict for another head")
	if merges() != 0 {
		t.Fatal("gh pr merge ran on a verdict for another head")
	}

	// A verdict at the real head merges, once.
	h2, work := realGateHarness(t)
	h2.next(t)
	h2.review(t, 0, "none")
	h2.next(t)
	h2.verdict(t, 0, VerdictPass, gitIn(t, work, "rev-parse", "HEAD"))
	h2.next(t)
	if merges() != 1 {
		data, _ := os.ReadFile(logPath)
		t.Fatalf("gh pr merge ran %d times, want 1 after verify at head\n%s", merges(), data)
	}
}

// postedComments returns the PR comment bodies the stub gh recorded.
func postedComments(logPath string) []string {
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(logPath), "comments.log"))
	var out []string
	for _, c := range strings.Split(string(data), "\n---\n") {
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// verdict-check on gantry reads a PR comment, so the verifier's file has to
// reach the PR, byte for byte, at the head it judged, before the merge.
func TestVerifyVerdictIsPostedOnPRBeforeMerge(t *testing.T) {
	for _, verdict := range []string{VerdictPass, VerdictPassNotes, VerdictFail} {
		t.Run(verdict, func(t *testing.T) {
			logPath := stubGH(t)
			h, work := realGateHarness(t)
			head := gitIn(t, work, "rev-parse", "HEAD")
			h.next(t)
			h.review(t, 0, "none")
			h.next(t)
			// The verifier's own file, written the way a verifier would.
			body := `{"head_sha": "` + head + `", "verdict": "` + verdict + `", "evidence": "ran the fixture, saw it"}` + "\n"
			h.write(t, 0, verifyFileName, body)
			h.next(t)

			comments := postedComments(logPath)
			if len(comments) != 1 {
				calls, _ := os.ReadFile(logPath)
				t.Fatalf("PR got %d comments, want the verifier's verdict once\ncalls:\n%s", len(comments), calls)
			}
			if !strings.Contains(comments[0], strings.TrimSpace(body)) {
				t.Fatalf("comment does not carry the verifier's file verbatim:\n%s\nwant it to contain\n%s", comments[0], body)
			}
			calls, _ := os.ReadFile(logPath)
			if verdict != VerdictFail {
				if c, m := strings.Index(string(calls), "pr comment"), strings.Index(string(calls), "pr merge"); c < 0 || m < 0 || c > m {
					t.Fatalf("verdict comment must come before the merge\n%s", calls)
				}
			} else if strings.Contains(string(calls), "pr merge") {
				t.Fatalf("FAIL verdict merged\n%s", calls)
			}
		})
	}
}

func TestTierAndRoundSurviveOnDiskAndRestart(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictFail, fixtureHead)
	h.next(t)

	orders, err := readOrders(h.l.deps.OrdersFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := orders.Orders[0]; got.Tier != "high" || got.Round != 1 {
		t.Fatalf("orders.json has tier=%q round=%d, want high and 1", got.Tier, got.Round)
	}
	if node := h.l.canonical.Orders["42"]; node.Tier != "high" || node.Round != 1 {
		t.Fatalf("canonical order has tier=%q round=%d, want high and 1", node.Tier, node.Round)
	}
}

func TestStaleReviewFileIsNotThisStagesReview(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.writeNow(t, 0, reviewFileName, "Covered: an earlier run\nFindings: none\n")
	h.next(t)
	h.next(t) // the reviewer writes nothing
	h.failedWith(t, "stale review")
}

func TestGateDirIsPerOrder(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	a, b := h.l.gateDir("a/b", 0), h.l.gateDir("a_b", 0)
	if a == b {
		t.Fatalf("orders a/b and a_b share %s", a)
	}
	for _, id := range []string{"..", "a/../..", "a/b"} {
		d := h.l.gateDir(id, 0)
		if !strings.HasPrefix(d, filepath.Join(h.l.runtimeDir, "gate")+string(filepath.Separator)) || filepath.Dir(filepath.Dir(d)) != filepath.Join(h.l.runtimeDir, "gate") {
			t.Fatalf("gateDir(%q) = %s, want a direct child of .noodle/gate", id, d)
		}
	}
}

func TestParseReview(t *testing.T) {
	clean := []string{
		"Covered: diff\nFindings: none",
		"Covered: diff\nFindings: None.",
		"Covered: diff\n**Findings:** none",
		"Covered: diff\nFindings: (none)",
		"Covered: diff\nFindings:\nnone",
		"Findings: none\nCovered: diff",
		"Covered: diff\r\nFindings: none\r\n",
	}
	for _, c := range clean {
		if r, err := parseReview([]byte(c)); err != nil || !r.Clean {
			t.Errorf("%q: clean=%v err=%v, want clean", c, r.Clean, err)
		}
	}
	dirty := []string{
		"Covered: diff\nFindings:\n- bug",
		"Covered: diff\nFindings: none\n- bug",
		"Covered: diff\nFindings: none\n+ bug",
		"Covered: diff\nFindings: none\nThe auth check is missing.",
		"Covered: diff\nFindings: none\n" + strings.Repeat("x", 70000) + "\n- real bug",
		"Covered: diff\nFindings: the login is broken",
	}
	for _, c := range dirty {
		if r, err := parseReview([]byte(c)); err != nil || r.Clean {
			t.Errorf("%.60q: clean=%v err=%v, want findings", c, r.Clean, err)
		}
	}
	for _, bad := range []string{"", "Findings: none", "Covered: diff", "Covered: d\nFindings: none\nFindings: none"} {
		if _, err := parseReview([]byte(bad)); err == nil {
			t.Errorf("%q parsed, want an error (not a review)", bad)
		}
	}
}

func TestCommitsAfterReviewDoNotShip(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality", "reflect")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	headSHA = func(string) (string, error) { return "commit-after-review", nil }
	h.next(t)
	h.failedWith(t, "head moved after review")
}

func TestMissingReviewFileFailsBeforeAnyStageMessageCanPark(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	// The reviewer's blocking stage message would park the stage; the gate has
	// to run first, so a missing review file fails before anything parks.
	h.next(t)
	h.failedWith(t, "no review file")
}

func TestAmendKeepsRoundAndTier(t *testing.T) {
	existing := Order{ID: "42", Tier: "high", Round: 2, Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Status: StageStatusPending}}}
	incoming := Order{ID: "42", Tier: "none", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Status: StageStatusPending}, {TaskKey: "reflect", Skill: "reflect", Status: StageStatusPending}}}
	merged, ok := mergeAmendedActiveOrder(existing, incoming)
	if !ok || merged.Tier != "high" || merged.Round != 2 {
		t.Fatalf("amended order tier=%q round=%d ok=%v, want high, 2", merged.Tier, merged.Round, ok)
	}
}

func TestStandaloneOrderWithoutTierIsNotGated(t *testing.T) {
	h := newGateHarness(t, "", "reflect")
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1: an untiered order with no execute stage is not a build order", len(h.ln.merged))
	}
}

func TestResubmittedFailedOrderKeepsTierAndRound(t *testing.T) {
	failed := Order{ID: "42", Tier: "high", Round: 2, Status: OrderStatusFailed, Stages: []Stage{{TaskKey: "execute", Skill: "execute", Status: StageStatusFailed}}}
	next := filepath.Join(t.TempDir(), "orders-next.json")
	body := `{"orders":[{"id":"42","tier":"none","stages":[{"do":"execute"}]}]}`
	if err := os.WriteFile(next, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := consumeOrdersNext(next, OrdersFile{Orders: []Order{failed}}, backlogForProposal(t, next))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Orders.Orders[0]; got.Tier != "high" || got.Round != 2 || got.Status != OrderStatusActive {
		t.Fatalf("resubmitted order tier=%q round=%d status=%q, want high, 2, active", got.Tier, got.Round, got.Status)
	}
}

// settleEvidence cycles until the repo checks running off the loop goroutine
// have been handled.
func (h *gateHarness) settleEvidence(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.l.evidence.checks) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("repo checks never finished")
		}
		time.Sleep(time.Millisecond)
		if err := h.l.Cycle(context.Background()); err != nil {
			t.Fatalf("evidence cycle: %v", err)
		}
	}
}

func TestVerdictCommentFailureStopsTheMerge(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	verdictPostBackoff = 0
	h.ln.verdictErr = errors.New("gh down")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	h.failedWith(t, "verdict comment failed")
	if h.ln.verdictTries != verdictPostAttempts {
		t.Fatalf("tried the comment %d times, want %d before failing", h.ln.verdictTries, verdictPostAttempts)
	}
}

func TestVerdictCommentFenceOutlastsBackticksInEvidence(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	body := `{"head_sha": "` + fixtureHead + `", "verdict": "PASS", "evidence": "ran ` + "```go" + ` code"}`
	h.write(t, 0, verifyFileName, body)
	h.next(t)
	if len(h.ln.verdicts) != 1 || !strings.Contains(h.ln.verdicts[0], "````json\n"+body+"\n````") {
		t.Fatalf("comment %q does not wrap the file in a four-backtick fence", h.ln.verdicts)
	}
}

func TestVerdictIsPostedOncePerFile(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	if len(h.ln.verdicts) != 1 || !strings.Contains(h.ln.verdicts[0], `"verdict":"PASS"`) {
		t.Fatalf("posted %q, want the verifier's PASS once", h.ln.verdicts)
	}
	cook := &cookHandle{worktreePath: "wt"}
	cook.worktreeName = h.l.cookWorktreeName("42", 0, "")
	order := Order{ID: "42"}
	v, err := h.l.readVerdict(order, cook)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.l.postVerdict(context.Background(), order, cook, v); err != nil {
		t.Fatal(err)
	}
	if len(h.ln.verdicts) != 1 {
		t.Fatalf("same verdict file posted %d times, want 1", len(h.ln.verdicts))
	}
}

func TestMediumTierPostsNoVerdict(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	if len(h.ln.verdicts) != 0 {
		t.Fatalf("medium tier posted %q, want nothing", h.ln.verdicts)
	}
}
