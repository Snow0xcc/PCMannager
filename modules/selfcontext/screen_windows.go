//go:build windows

package selfcontext

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"strings"

	"github.com/kbinani/screenshot"
)

// 屏幕捕获与 VLM 解析的参数。
const (
	// screenDownscale 把全屏截图降采样到的目标宽度。VLM 对长边的利用率有限，
	// 1280px 已足以辨认"在用什么应用、看什么内容"，而 JPEG 体积从 ~1.5MB
	// 降到 ~120KB——这是低 CPU/低流量后台运行的关键一步。
	screenDownscale = 1280

	// screenJPEGQuality 与画面可辨认度的平衡点；再低会出现可见块效应。
	screenJPEGQuality = 62

	// vlmPrompt 要求模型输出一句话场景描述。提示词刻意要求"不引用屏幕上的
	// 个人敏感字样"，这是隐私边界的一部分（与 optPauseOnLock 同一立场）。
	vlmPrompt = "用一句不超过40字的中文描述这个屏幕画面里用户正在做什么。" +
		"不要引用可能包含密码、密钥、个人身份的具体文字。"
)

// CaptureScreen grabs the primary display and returns a downscaled JPEG.
//
// 低功耗的三道闸门都在调用方（间隔、锁屏暂停、失败静默跳过），这里只负责
// 把一次捕获做得又小又快：kbinani/screenshot 的 CaptureRect 走 GDI 全屏
// blit（~10ms/1080p），随后纯内存缩放 + JPEG 编码（~20ms），整个过程
// 不产生任何磁盘写——阶段二若入库，写盘由存储层决定。
func CaptureScreen() ([]byte, error) {
	if screenshot.NumActiveDisplays() <= 0 {
		return nil, fmt.Errorf("selfcontext: 无可用显示器")
	}
	bounds := screenshot.GetDisplayBounds(0)
	full, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, fmt.Errorf("selfcontext: 截屏失败: %w", err)
	}

	small := downscale(full, screenDownscale)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: screenJPEGQuality}); err != nil {
		return nil, fmt.Errorf("selfcontext: JPEG 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// downscale 等比缩放到目标宽度（仅在需要缩小时才动像素）。
// 纯 Go 的近邻采样足够：VLM 关心的是布局与内容类别，不是像素锐度。
func downscale(img *image.RGBA, targetW int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= targetW {
		return img
	}
	nh := h * targetW / w
	if nh < 1 {
		nh = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, targetW, nh))
	// 步进采样（每 sxSrc 像素取一点），避免逐目标像素做双线性插值的开销。
	dx, dy := float64(w)/float64(targetW), float64(h)/float64(nh)
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + int(float64(y)*dy)
		for x := 0; x < targetW; x++ {
			sx := b.Min.X + int(float64(x)*dx)
			out.Set(x, y, img.RGBAAt(sx, sy))
		}
	}
	return out
}

// VLMConfig 是 OpenAI 兼容多模态接口的连接参数（面板可配置）。
type VLMConfig struct {
	// BaseURL 例如 "http://127.0.0.1:1234/v1"（LM Studio 默认）或豆包的
	// OpenAI 兼容端点。允许 http：本地推理不走 TLS 是常态。
	BaseURL string
	// APIKey 可为空（本地服务通常不校验）。
	APIKey string
	// Model 是 vision 模型名（如 "gpt-4o-mini"、"qwen2-vl-7b-instruct"）。
	Model string
}

// DescribeScreen 把一帧 JPEG 交给 OpenAI 兼容的多模态接口，返回一句话描述。
//
// 请求构造（OpenAI Chat Completions 规范）：
//
//	{
//	  "model": <Model>,
//	  "messages": [{
//	    "role": "user",
//	    "content": [
//	      {"type": "text", "text": <prompt>},
//	      {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,..."}}
//	    ]
//	  }],
//	  "max_tokens": 80
//	}
//
// 用 data URL 内嵌图像而不是先上传文件：兼容层（LM Studio/豆包/Ollama 的
// OpenAI 端点）对 data URL 的支持最一致，且省一次上传往返。
func DescribeScreen(ctx context.Context, cfg VLMConfig, jpegBytes []byte) (string, error) {
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("selfcontext: 未配置 VLM 服务地址")
	}
	if cfg.Model == "" {
		return "", fmt.Errorf("selfcontext: 未配置 VLM 模型名")
	}
	if len(jpegBytes) == 0 {
		return "", fmt.Errorf("selfcontext: 空图像")
	}

	payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":[`+
		`{"type":"text","text":%q},`+
		`{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,%s"}}`+
		`]}],"max_tokens":80}`,
		cfg.Model, vlmPrompt, base64Std(jpegBytes))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions",
		strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	httpClient := &http.Client{Timeout: vlmTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("selfcontext: VLM 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("selfcontext: VLM 返回 %d", resp.StatusCode)
	}

	// 只取 choices[0].message.content：手写解析避免引入整棵 JSON 依赖树
	// 的反射编解码（响应结构固定且字段名稳定）。
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return extractContent(string(body)), nil
}

// extractContent 从 chat/completions 响应里抠出 message.content。
// 手写的窄解析：找到 "content":" 后读字符串（处理 \" 与 \\ 两种转义）。
// 响应不是预期形状时返回空串，由调用方当作"本次解析失败"跳过。
func extractContent(body string) string {
	const key = `"content":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	var sb strings.Builder
	for j := 0; j < len(rest); j++ {
		switch rest[j] {
		case '\\':
			if j+1 < len(rest) {
				j++
				switch rest[j] {
				case 'n':
					sb.WriteByte('\n')
				case '"':
					sb.WriteByte('"')
				case '\\':
					sb.WriteByte('\\')
				default:
					sb.WriteByte(rest[j])
				}
			}
		case '"':
			return sb.String()
		default:
			sb.WriteByte(rest[j])
		}
	}
	return sb.String()
}
