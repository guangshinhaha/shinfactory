package dispatcher

import (
	"bufio"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/guangshinhaha/shinfactory/parse"
)

const zcodeHeadlessOutput = `{
  "sessionId": "sess_1",
  "response": "DONE",
  "usage": {
    "inputTokens": 36112,
    "outputTokens": 60
  }
}
`

func zcodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(zcodeStream(strings.NewReader(raw)))
	for sc.Scan() {
		var v map[string]any
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("line %q is not one JSON object: %v", sc.Text(), err)
		}
		lines = append(lines, v)
	}
	return lines
}

func TestZcodeStreamEmitsInitThenResult(t *testing.T) {
	lines := zcodeLines(t, zcodeHeadlessOutput)
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want init and result", lines)
	}
	if lines[0]["type"] != "system" || lines[0]["subtype"] != "init" {
		t.Errorf("first line = %v, want system/init", lines[0])
	}
	if lines[1]["type"] != "result" || lines[1]["is_error"] != false || lines[1]["result"] != "DONE" {
		t.Errorf("second line = %v, want a successful result", lines[1])
	}
}

func TestZcodeStreamParsesAsCompletedClaudeTurn(t *testing.T) {
	reg := parse.NewRegistry()
	var types []parse.EventType
	sc := bufio.NewScanner(zcodeStream(strings.NewReader(zcodeHeadlessOutput)))
	for sc.Scan() {
		_, events, err := reg.ParseLine(sc.Bytes())
		if err != nil {
			t.Fatalf("parse %q: %v", sc.Text(), err)
		}
		for _, e := range events {
			types = append(types, e.Type)
		}
	}
	if !slices.Contains(types, parse.EventInit) || !slices.Contains(types, parse.EventResult) {
		t.Fatalf("canonical events = %v, want init and result", types)
	}
}

func TestZcodeStreamErrorBecomesFailedResult(t *testing.T) {
	lines := zcodeLines(t, `{"sessionId":"sess_1","error":"model quota exhausted"}`)
	if len(lines) != 2 || lines[1]["is_error"] != true {
		t.Fatalf("lines = %v, want an error result", lines)
	}
}

func TestZcodeStreamWithNoOutputEmitsOnlyInit(t *testing.T) {
	lines := zcodeLines(t, "")
	if len(lines) != 1 || lines[0]["subtype"] != "init" {
		t.Fatalf("lines = %v, want only init (no result: the run produced nothing)", lines)
	}
}

func TestZcodeCommandRunsHeadlessInWorktree(t *testing.T) {
	d := NewProcessDispatcher(ProcessDispatcherConfig{RuntimeDir: t.TempDir(), RuntimeKind: "process"})
	req := DispatchRequest{Provider: "zcode", Model: "GLM-5.3", WorktreePath: "/w/t"}
	cmd, err := d.buildCmd(req, "", "the prompt")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cmd.Args, " ")
	for _, frag := range []string{"zcode -p the prompt", "--cwd /w/t", "--mode yolo", "--json"} {
		if !strings.Contains(got, frag) {
			t.Errorf("args = %q, missing %q", got, frag)
		}
	}
	if strings.Contains(got, "--model") {
		t.Errorf("args = %q, zcode picks its model from its own config", got)
	}
}

func TestZcodePromptInlinesSystemPrompt(t *testing.T) {
	system, final := composePrompts("zcode", "build it", "PREAMBLE", "SKILL")
	if system != "" {
		t.Errorf("system prompt = %q, want empty: zcode has no system-prompt flag", system)
	}
	if !strings.HasPrefix(final, "PREAMBLE\n\nSKILL") || !strings.HasSuffix(final, "build it") {
		t.Errorf("final prompt = %q, want preamble and skill before the task", final)
	}
}
