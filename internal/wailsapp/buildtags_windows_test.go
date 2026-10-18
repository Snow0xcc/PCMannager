//go:build windows

package wailsapp

import (
	"runtime/debug"
	"testing"
)

// TestBuiltWithProductionTag checks the detector against the current test
// binary's real build settings.
//
// `go test` is itself a build, and CI runs it without -tags production, so the
// test must accept both outcomes. What it must prove is that the detector
// actually consults build info rather than returning a constant — otherwise the
// guard would silently degrade (always-true passes and the native window code
// runs into Wails' stub; always-false disables the window with no signal).
func TestBuiltWithProductionTag(t *testing.T) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("此工具链不提供 build info")
	}
	want := false
	for _, s := range bi.Settings {
		if s.Key == "-tags" {
			want = hasProductionTag(s.Value)
		}
	}
	if got := builtWithProductionTag(); got != want {
		t.Fatalf("builtWithProductionTag() = %v, 但二进制实际 tags 判定为 %v", got, want)
	}
}

// TestBuiltWithProductionTagReadsBuildInfo stubs the build info reader to prove
// the three outcomes the real check must distinguish.
func TestBuiltWithProductionTagReadsBuildInfo(t *testing.T) {
	original := buildInfo
	t.Cleanup(func() { buildInfo = original })

	cases := []struct {
		name   string
		ok     bool
		tags   string
		want   bool
		reason string
	}{
		{name: "带 production 标签", ok: true, tags: "production", want: true},
		{name: "标签列表含 production", ok: true, tags: "desktop,production,devtools", want: true},
		{name: "仅 dev 标签", ok: true, tags: "dev", want: false},
		{name: "完全没有 -tags 设置", ok: true, tags: "", want: false},
		{name: "工具链不提供 build info", ok: false, want: true,
			reason: "未知时不能误伤正常构建，交由 wails.Run 自行报告"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buildInfo = func() (*debug.BuildInfo, bool) {
				if !tc.ok {
					return nil, false
				}
				bi := &debug.BuildInfo{}
				if tc.tags != "" {
					bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "-tags", Value: tc.tags})
				}
				return bi, true
			}
			if got := builtWithProductionTag(); got != tc.want {
				t.Fatalf("builtWithProductionTag() = %v, want %v（%s）", got, tc.want, tc.reason)
			}
		})
	}
}

// TestHasProductionTag pins the parsing rule: the value is a comma-separated
// list and only an exact "production" element counts, because that is literally
// what Wails' build constraint tests for.
func TestHasProductionTag(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"production", true},
		{"desktop,production", true},
		{"production,devtools", true},
		{" production ", true},
		{"dev", false},
		{"productionish", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := hasProductionTag(tc.value); got != tc.want {
			t.Errorf("hasProductionTag(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
