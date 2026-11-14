package dispatcher

import (
	"encoding/json"
	"io"
)

// zcodeStream turns zcode's headless --json output (one pretty-printed JSON
// object written when the run ends) into the claude stream-json lines the stamp
// processor already parses: an init line at once, then one result line.
func zcodeStream(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		writeJSONLine(pw, map[string]any{"type": "system", "subtype": "init"})

		dec := json.NewDecoder(r)
		var last map[string]any
		for {
			var v map[string]any
			if err := dec.Decode(&v); err != nil {
				break
			}
			last = v
		}
		_, _ = io.Copy(io.Discard, r)
		if last == nil {
			return
		}
		writeJSONLine(pw, zcodeResultLine(last))
	}()
	return pr
}

func zcodeResultLine(out map[string]any) map[string]any {
	line := map[string]any{
		"type":       "result",
		"subtype":    "success",
		"is_error":   false,
		"result":     out["response"],
		"session_id": out["sessionId"],
	}
	if msg, failed := out["error"]; failed && msg != nil {
		line["subtype"] = "error"
		line["is_error"] = true
		line["error"] = msg
	}
	if usage, ok := out["usage"].(map[string]any); ok {
		line["usage"] = map[string]any{
			"input_tokens":  usage["inputTokens"],
			"output_tokens": usage["outputTokens"],
		}
	}
	return line
}

func writeJSONLine(w io.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}
