package run

import (
	"encoding/json"
	"strings"
	"testing"
)

const testTicket = "tkt_" + "example0123456789abcdef"

func startMessage(serverURL string, edit func(map[string]any)) map[string]any {
	message := map[string]any{"action": "start_run", "run_id": json.Number("7"), "ticket": testTicket, "server_url": serverURL}
	if edit != nil {
		edit(message)
	}
	return message
}

func TestParseStartAcceptsOnlyTheTicketMessage(t *testing.T) {
	request, ok := ParseStart(startMessage("http://127.0.0.1:8000", nil))
	if !ok || request != (StartRequest{RunID: 7, Ticket: testTicket, ServerURL: "http://127.0.0.1:8000"}) {
		t.Fatalf("request = %+v, %v", request, ok)
	}
}

func TestStartRunWithExtraFieldsIsInvalid(t *testing.T) {
	for field, value := range map[string]any{
		"mcp_token": "tok", "model": "sonnet", "input": "x", "mcp_url": "http://127.0.0.1:8000/mcp", "prompt": "x", "result_id": json.Number("3"),
	} {
		if _, ok := ParseStart(startMessage("http://127.0.0.1:8000", func(m map[string]any) { m[field] = value })); ok {
			t.Errorf("%s accepted", field)
		}
	}
}

func TestStartRunWithBadTicketRunIDOrServerURLIsInvalid(t *testing.T) {
	cases := []func(map[string]any){
		func(m map[string]any) { delete(m, "ticket") },
		func(m map[string]any) { delete(m, "run_id"); m["other"] = 1 },
	}
	for _, ticket := range []any{"", "short", strings.Repeat("x", 129), strings.Repeat("a b", 10), nil, json.Number("5")} {
		cases = append(cases, func(m map[string]any) { m["ticket"] = ticket })
	}
	for _, runID := range []any{json.Number("0"), json.Number("-1"), "7", json.Number("7.0"), json.Number("7e0"), true, nil, 7.0} {
		cases = append(cases, func(m map[string]any) { m["run_id"] = runID })
	}
	for _, url := range []any{"http://leadin.example", "http://127.0.0.1:8000/api", "file:///x", nil, json.Number("1")} {
		cases = append(cases, func(m map[string]any) { m["server_url"] = url })
	}
	for i, edit := range cases {
		message := startMessage("http://127.0.0.1:8000", edit)
		if _, ok := ParseStart(message); ok {
			t.Errorf("case %d accepted: %v", i, message)
		}
	}
}

func TestStartRequestStringNeverContainsTheTicket(t *testing.T) {
	request, _ := ParseStart(startMessage("http://127.0.0.1:8000", nil))
	text := request.String()
	if strings.Contains(text, testTicket) || !strings.Contains(text, "RunID: 7") {
		t.Fatalf("String = %q", text)
	}
}

func TestUserMessageIsThePromptOfTheSpec(t *testing.T) {
	prompt := "Call `start_example` <and> follow & the instructions.\nè"
	line := userMessage(prompt)
	if !strings.HasSuffix(string(line), "}\n") || strings.Count(string(line), "\n") != 1 {
		t.Fatalf("line = %q", line)
	}
	var message map[string]any
	if err := json.Unmarshal(line, &message); err != nil {
		t.Fatal(err)
	}
	inner := message["message"].(map[string]any)
	if message["type"] != "user" || inner["role"] != "user" || inner["content"] != prompt || len(message) != 2 {
		t.Fatalf("message = %v", message)
	}
}
