package selfcontext

import (
	"context"
	"strings"
	"time"
)

// vlmTimeout 是单次视觉解析请求的上限。本地 LM Studio 首次加载模型可能慢，
// 给足 60s；超时的那次采样直接跳过（下个周期再来）。定义在无 build tag 的
// 本文件里，因为 summary.go（跨平台）也用它——若放在 screen_windows.go 的
// windows 分支里，非 Windows 交叉编译会 undefined。
const vlmTimeout = 60 * time.Second

// recordSummary performs the screen-capture → VLM → summary pipeline for one
// sample and attaches the result to the matching entry.
//
// 失败策略：任何一步失败都静默放弃（仅 Debug 日志）——VLM 不可用是常态
// （模型未加载/服务未启动），绝不能让采样循环被错误风暴淹没，更不能因为
// 语义解析失败丢掉已有的窗口标题记录。
func (f *Feature) recordSummary(title string) {
	if f.ctx == nil {
		return
	}
	cfg := f.vlmConfig()
	if cfg.BaseURL == "" || cfg.Model == "" {
		// 未配置 VLM：screen 模式退化为纯窗口记录（不报错刷屏，
		// 但启动时已通过 State 提示过配置缺失）。
		return
	}

	jpegBytes, err := CaptureScreen()
	if err != nil {
		f.ctx.Logger.Debug("屏幕捕获跳过", "module", moduleID, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(f.ctx.Ctx, vlmTimeout)
	defer cancel()
	summary, err := DescribeScreen(ctx, cfg, jpegBytes)
	if err != nil {
		f.ctx.Logger.Debug("VLM 解析跳过", "module", moduleID, "err", err)
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}

	f.mu.Lock()
	// 从尾部找匹配的条目（record 刚 append 过它）；找不到就放弃——
	// 说明条目已被环形缓冲挤掉。
	saved := false
	for i := len(f.entries) - 1; i >= 0 && time.Since(f.entries[i].Timestamp) < 2*time.Minute; i-- {
		if f.entries[i].Title == title && f.entries[i].Summary == "" {
			f.entries[i].Summary = summary
			saved = true
			break
		}
	}
	// 条目现在是持久化的（C3），描述也必须落盘，否则重启后 Summary 全丢。
	if saved {
		f.saveLocked()
	}
	f.mu.Unlock()

	f.ctx.Logger.Info("画面语义已解析", "module", moduleID, "title", title, "summary", summary)
	f.ctx.Bus.Log(moduleID, "info", "画面解析："+summary)
}

// vlmConfig reads the OpenAI-compatible VLM connection settings.
func (f *Feature) vlmConfig() VLMConfig {
	cfg := VLMConfig{}
	if f.ctx == nil {
		return cfg
	}
	if s, ok := f.ctx.Config.Get(optVLMBaseURL, "").(string); ok {
		cfg.BaseURL = strings.TrimSpace(s)
	}
	if s, ok := f.ctx.Config.Get(optVLMModel, "").(string); ok {
		cfg.Model = strings.TrimSpace(s)
	}
	if s, ok := f.ctx.Config.Get(optVLMKey, "").(string); ok {
		cfg.APIKey = strings.TrimSpace(s)
	}
	return cfg
}
