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
// 任何一侧新增/改名事件而不同步另一侧，都会在这里红。契约是**集合**相等：
// 数组顺序不是契约的一部分（前端重排不应误红），但成员必须一一对应且不重复。
func TestEventTypesContract(t *testing.T) {
	wantList := []string{core.EventLog, core.EventState, core.EventProgress, core.EventNotice}
	want := make(map[string]bool, len(wantList))
	for _, ev := range wantList {
		want[ev] = true
	}

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
	gotSet := make(map[string]bool, len(wantList))
	for _, raw := range strings.Split(string(m[1]), ",") {
		raw = strings.TrimSpace(raw)
		raw = strings.Trim(raw, "\"' ")
		if raw == "" {
			continue
		}
		if gotSet[raw] {
			t.Errorf("index.html EVENT_TYPES 含重复项 %q（契约按集合比较，重复即漂移）", raw)
			continue
		}
		gotSet[raw] = true
		got = append(got, raw)
	}
	for _, ev := range wantList {
		if !gotSet[ev] {
			t.Errorf("index.html EVENT_TYPES = %v, 缺少服务端事件类型 %q（两侧事件类型漂移）", got, ev)
		}
	}
	for _, ev := range got {
		if !want[ev] {
			t.Errorf("index.html EVENT_TYPES 含未知事件类型 %q（服务端 core 无此 Event* 常量）", ev)
		}
	}
}

// TestSubscribeRegistersTypedListeners 确保前端是按类型注册监听（而不是只挂
// onmessage——后者收不到具名 SSE 事件，正是 A1 缺陷的根源）。
//
// 断言不与任何局部变量名耦合（历史教训：字面匹配 "addEventListener(t" 与
// 循环变量 t 绑死，前端改名即误红）。改为要求两个结构同时成立：
//
//	(a) subscribe 的 HTTP 分支内存在对 EVENT_TYPES 的 for...of 迭代；
//	(b) 该分支内存在 addEventListener 调用（迭代体以循环变量为事件名注册）。
//
// 前端若退化为只挂 onmessage（原始缺陷），两条断言同时红。
func TestSubscribeRegistersTypedListeners(t *testing.T) {
	data, err := FS().ReadFile(IndexPath)
	if err != nil {
		t.Fatalf("读取内嵌面板失败: %v", err)
	}
	src := string(data)

	// 只检查 subscribe() 的 HTTP 分支（Wails 分支走 runtime.EventsOn，
	// 不在本契约内）。窗口锚点全用 DOM/API 名：EventSource 构造为起点，
	// 该分支的 return 为终点；锚点找不到时窗口放宽到文件尾——宁可少查，
	// 也不因排版微调误红。
	loc := regexp.MustCompile(`new\s+EventSource\s*\(`).FindStringIndex(src)
	if loc == nil {
		t.Fatal("transport.subscribe 缺少 EventSource 构造——HTTP 面板将收不到任何 SSE 事件")
	}
	httpBranch := src[loc[1]:]
	if end := strings.Index(httpBranch, "return;"); end >= 0 {
		httpBranch = httpBranch[:end]
	}

	// (a) 必须按 EVENT_TYPES 迭代注册（循环变量名不限）。
	m := regexp.MustCompile(`for\s*\(\s*(?:const|let|var)\s+(\w+)\s+of\s+EVENT_TYPES\s*\)`).FindStringSubmatch(httpBranch)
	if m == nil {
		t.Error("transport.subscribe 未迭代 EVENT_TYPES——新增事件类型时前端不会注册监听，事件将静默丢失")
	} else if !strings.Contains(httpBranch, "addEventListener("+m[1]) {
		t.Errorf("EVENT_TYPES 迭代体未以循环变量 %q 注册 addEventListener——具名 SSE 事件将无法到达 onEvent", m[1])
	}
	// (b) 独立于 (a)：退化成只挂 onmessage 时两条同时红；硬编码单个事件名
	// 而不走迭代时 (a) 红。
	if !strings.Contains(httpBranch, "addEventListener(") {
		t.Error("transport.subscribe 的 HTTP 分支没有 addEventListener 调用——只挂 onmessage 收不到具名 SSE 事件（A1 缺陷复现）")
	}
}

// TestDynamicInterpolationEscaped 回归（A6）：el() 走 innerHTML，动态数据
// 插值必须走 esc()。全站唯一曾被漏掉的是 effective_data_dir。
func TestDynamicInterpolationEscaped(t *testing.T) {
	data, err := FS().ReadFile(IndexPath)
	if err != nil {
		t.Fatalf("读取内嵌面板失败: %v", err)
	}
	src := string(data)
	if strings.Contains(src, "+ a.effective_data_dir +") {
		t.Error("effective_data_dir 插值未走 esc()——innerHTML XSS 破口")
	}
	if !strings.Contains(src, "esc(a.effective_data_dir)") {
		t.Error("effective_data_dir 应经 esc() 转义后再插值")
	}
}
