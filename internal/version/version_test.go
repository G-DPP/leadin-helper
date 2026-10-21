package version

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestStringHasVersionProtocolAndTarget(t *testing.T) {
	got := String()
	for _, part := range []string{
		Version,
		fmt.Sprintf("protocol %d", ProtocolVersion),
		runtime.GOOS + "/" + runtime.GOARCH,
	} {
		if !strings.Contains(got, part) {
			t.Errorf("String() = %q, missing %q", got, part)
		}
	}
	if !strings.HasPrefix(got, "leadin-helper ") {
		t.Errorf("String() = %q, want the leadin-helper prefix", got)
	}
}
