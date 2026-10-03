package panel

import (
	"regexp"
	"strings"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// contractTest: 服务端（internal/server）经 SSE 写出的每个事件都带
// `event: <type>` 名（server.go：`"event: "+ev.Type`）。按 SSE 规范，
// 浏览器的 onmessage 只对无名事件触发——因此前端必须按类型注册
// addEventListener（ROADMAP A1）。本测试把两侧钉死：
//
//	index.html 的 EVENT_TYPES 数组  ==  core 的 Event* 常量集合。
//
// 任何一侧新增/改名事件而不同步另一侧，都会在这里红。
func TestEventTypesContract(t *testing.T) {
	want := []string{core.EventLog, core.EventState, core.EventProgress, core.EventNotice}

	data, err := FS().ReadFile(IndexPath)
	if err != nil {
		t.Fatalf("读取内嵌面板失败: %v", err)
	}
	re := regexp.MustCompile(`const EVENT_TYPES\s*=\s*\[([^\]]*)\]`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatal("index.html 缺少 EVENT_TYPES 常量表——SSE 契约测试无法执行")
	}
	var got []string
	for _, raw := range strings.Split(string(m[1]), ",") {
		raw = strings.TrimSpace(raw)
		raw = strings.Trim(raw, "\"' ")
		if raw != "" {
			got = append(got, raw)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("index.html EVENT_TYPES = %v, 与服务端事件类型 %v 数量不一致", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("EVENT_TYPES[%d] = %q, 服务端为 %q（两侧事件类型漂移）", i, got[i], want[i])
		}
	}
}

// TestSubscribeRegistersTypedListeners 确保前端是按类型注册监听（而不是只挂
// onmessage——后者收不到具名 SSE 事件，正是 A1 缺陷的根源）。
func TestSubscribeRegistersTypedListeners(t *testing.T) {
	data, err := FS().ReadFile(IndexPath)
	if err != nil {
		t.Fatalf("读取内嵌面板失败: %v", err)
	}
	src := string(data)
	// subscribe() 的 HTTP 分支必须出现对 EVENT_TYPES 的 addEventListener 循环。
	if !strings.Contains(src, "addEventListener(t") {
		t.Error("transport.subscribe 未按事件类型注册 addEventListener——具名 SSE 事件将无法到达 onEvent")
	}
}
