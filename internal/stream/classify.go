package stream

import "strings"

// Error texts of Claude Code 2.1.x (in result.result when is_error):
//
//   - API error, verified (2.1.291, recorded streams): with the network
//     unreachable «API Error: Connection refused — a firewall or proxy may be
//     blocking it (ECONNREFUSED)», with a model that does not exist «There's
//     an issue with the selected model (…)». In both terminal_reason is
//     api_error, and api_error_status carries the HTTP status when there is
//     one (404);
//   - usage limit, assumed: it cannot be reproduced on demand. The format is
//     the one read in the code of Claude Code 2.1.291: «You've hit your
//     session limit · resets 3pm (…)» (also weekly limit, Opus limit…), and
//     the CLI itself recognizes these messages by the prefixes of
//     UsageLimitMarkers. api_error_status is expected to be 429.

// UsageLimitMarkers are pieces (lowercase) of Claude Code's limit messages:
// the prefixes the 2.1.291 CLI recognizes its own by, plus older texts.
var UsageLimitMarkers = []string{
	"you've hit your",
	"you've reached your",
	"you're out of usage credits",
	"you're out of extra usage",
	"your org is out of usage",
	"requires usage credits",
	"usage limit",
	"limit reached",
	"rate limit",
}

// UsageLimitStatus is the HTTP status of the limit: Too Many Requests.
const UsageLimitStatus = 429

// ClassifyError is the neutral code of an error result: usage_limit,
// api_error or unknown. Claude's text is only used to decide: it is never
// returned or logged.
func ClassifyError(result map[string]any) string {
	text, _ := result["result"].(string)
	text = strings.ReplaceAll(strings.ToLower(text), "’", "'")
	status, statusIsInt := intValue(result["api_error_status"])
	for _, marker := range UsageLimitMarkers {
		if strings.Contains(text, marker) {
			return "usage_limit"
		}
	}
	if statusIsInt && status == UsageLimitStatus {
		return "usage_limit"
	}
	if strings.HasPrefix(text, "api error") || strings.Contains(text, "overloaded") ||
		result["terminal_reason"] == "api_error" || statusIsInt {
		return "api_error"
	}
	return "unknown"
}
