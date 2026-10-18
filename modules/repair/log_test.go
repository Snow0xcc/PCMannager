package repair

import (
	"strings"
	"testing"
)

// TestTailBoundsErrorOutput guards the context attached to a failed command.
//
// The field log only ever said `err="exit status 1"`, which is useless in a
// windowsgui build with no console to inspect. The tail is what makes the
// failure actionable, so it must stay bounded and must keep the END of the
// output (that is where cmd.exe prints the reason).
func TestTailBoundsErrorOutput(t *testing.T) {
	short := "cmd 不认识该参数"
	if got := tail(short, 100); got != short {
		t.Fatalf("tail(short) = %q, want the input unchanged", got)
	}

	long := strings.Repeat("a", 1000) + "真正的错误原因"
	got := tail(long, 400)
	if len(got) > 404 { // 400 bytes + "..." prefix
		t.Fatalf("tail returned %d bytes, want <= 404", len(got))
	}
	if !strings.HasSuffix(got, "真正的错误原因") {
		t.Fatalf("tail lost the reason at the end: %q", got[len(got)-20:])
	}
	if !strings.HasPrefix(got, "...") {
		t.Fatalf("tail should mark truncation with an ellipsis: %q", got[:10])
	}
}
