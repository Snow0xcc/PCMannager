// Package updater implements the auto-update module: it periodically checks
// GitHub Releases for a newer version, surfaces a notice in the panel, and can
// download the matching asset into the module's data directory.
//
// Safety posture:
//   - Only api.github.com / github.com / objects.githubusercontent.com are
//     dialable; the allowlist is enforced per-dial via a Control hook, so
//     redirects are re-validated too.
//   - The asset must match pcmannager-<goos>-<goarch>[.exe] for the RUNNING
//     platform; no architecture confusion.
//   - A size cap (128 MiB) protects against runaway responses.
//   - The downloaded file lands in the module data dir, never in PATH, and it
//     is NEVER executed automatically. Applying the update is a separate,
//     explicit user action (the install action in feature.go).
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// apiBase is the GitHub REST endpoint for the latest release of this project.
const apiBase = "https://api.github.com/repos/Snow0xcc/PCMannager/releases/latest"

// mirrors is the 国内加速镜像池，按优先级从高到低排列；末尾的空串表示
// GitHub 直连兜底。客户端按顺序尝试，某个镜像超时/不可达就切下一个，
// 最后总能落到直连——国内镜像站偶发限流是常态，轮询保底才能保证升级稳定。
//
// 重要认知：镜像只是可用性加速而非安全边界。它们只是额外几个可 dial 的
// 主机，安全性仍完全由 allowedHosts 的 per-dial 校验保证——经镜像的每一跳
// （含重定向）都必须命中白名单，镜像若把请求重定向到任意主机同样会被
// Control 钩子拒绝。
var mirrors = []string{
	"https://ghfast.top/",
	"https://ghproxy.net/",
	"https://github.moeyy.xyz/",
	"", // 直连 GitHub
}

// maxAssetSize caps a downloaded release asset (128 MiB).
const maxAssetSize = 128 << 20

// allowedHosts is the dial allowlist. Release downloads redirect from
// github.com to objects.githubusercontent.com, hence the third entry; the
// mirror host names are added dynamically (see init) so the list stays in
// lockstep with mirrors.
var allowedHosts = map[string]bool{
	"api.github.com":                true,
	"github.com":                    true,
	"objects.githubusercontent.com": true,
}

func init() {
	// 把 mirrors 里的 host 逐个加入白名单，避免改镜像池时漏改两处。
	for _, m := range mirrors {
		if m == "" {
			continue
		}
		if u, err := url.Parse(m); err == nil && u.Host != "" {
			allowedHosts[u.Host] = true
		}
	}
}

// newHTTPClient returns an http.Client whose Transport enforces the host
// allowlist on every dial. Control runs at dial time, before TLS is layered
// on top; http.Client follows redirects using the same transport, so every
// hop of a download redirect is re-validated.
func newHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("updater: 非法地址 %q: %w", addr, err)
			}
			if !allowedHosts[host] {
				return nil, fmt.Errorf("updater: 已阻止连接未允许的主机 %q", host)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Timeout: 5 * time.Minute, Transport: transport}
}

// Release is the subset of the GitHub Releases payload the module needs.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// BrowserDownloadURL is the direct download link (github.com/...).
	BrowserDownloadURL string `json:"browser_download_url"`
}

// AssetName is the release asset this build expects, e.g.
// "pcmannager-windows-amd64.exe" — naming per .github/workflows/release.yml.
func AssetName() string {
	name := "pcmannager-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// mirrorURL wraps a GitHub resource URL with the given accelerator prefix.
//
// 规则：prefix 为空串时直接返回 raw（直连兜底）；只有 http(s) 的 GitHub 域名
// （api.github.com、github.com）会被包装，且若 URL 已带任一镜像前缀则原样
// 返回，保证幂等——否则 ghproxy 会叠在 ghfast 上变成无效 URL。非 GitHub 域名
// 或解析失败一律原样返回，不吞掉本不该碰的地址。
func mirrorURL(raw, prefix string) string {
	if prefix == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	// 只代理 http(s) 的 GitHub 域名：其它 scheme（ftp 等）即使 host 相同也
	// 不是 HTTP 下载路径，包装后会变成无效请求。
	if u.Scheme != "http" && u.Scheme != "https" {
		return raw
	}
	// 已带任一镜像前缀则不再二次包装。
	for _, m := range mirrors {
		if m == "" {
			continue
		}
		if mu, err := url.Parse(m); err == nil && strings.EqualFold(u.Host, mu.Host) {
			return raw
		}
	}
	if u.Host != "api.github.com" && u.Host != "github.com" {
		return raw
	}
	return prefix + raw
}

// candidateURLs 按 mirrors 的优先级顺序生成候选 URL 列表，末尾的空串前缀
// 对应 GitHub 直连兜底。调用方按顺序尝试，首个成功即止。
func candidateURLs(raw string) []string {
	out := make([]string, 0, len(mirrors))
	for _, m := range mirrors {
		out = append(out, mirrorURL(raw, m))
	}
	return out
}

// fetchLatest queries the GitHub API for the latest release, trying every
// mirror in order and finally falling back to a direct connection.
func fetchLatest(ctx context.Context, hc *http.Client) (*Release, error) {
	var errs []string
	for _, u := range candidateURLs(apiBase) {
		rel, err := fetchLatestVia(ctx, hc, u)
		if err == nil {
			return rel, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", u, err))
	}
	return nil, fmt.Errorf("updater: 所有镜像与直连均失败: %s", strings.Join(errs, "; "))
}

// fetchLatestVia fetches the release from an explicit URL.
func fetchLatestVia(ctx context.Context, hc *http.Client, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: GitHub API 返回 %s", resp.Status)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("updater: 解析 releases 响应失败: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("updater: 响应缺少 tag_name")
	}
	return &rel, nil
}

// findAsset locates the release asset matching the running platform. It
// returns nil when the release has no asset for this platform, which is not
// an error — the caller reports "no package for this platform".
func (r *Release) findAsset() *Asset {
	want := AssetName()
	for i := range r.Assets {
		if strings.EqualFold(r.Assets[i].Name, want) {
			return &r.Assets[i]
		}
	}
	return nil
}

// download streams the asset to dst (atomic capped copy), returning the byte
// count and the sha256 of the payload. It tries every mirror in order and
// finally falls back to the direct URL.
func download(ctx context.Context, hc *http.Client, url, dst string) (int64, string, error) {
	var errs []string
	for _, u := range candidateURLs(url) {
		n, sum, err := downloadVia(ctx, hc, u, dst)
		if err == nil {
			return n, sum, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", u, err))
	}
	return 0, "", fmt.Errorf("updater: 所有镜像与直连均失败: %s", strings.Join(errs, "; "))
}

// downloadVia streams the asset from an explicit URL to dst. The host
// allowlist is enforced per-dial by the shared client, so an off-allowlist
// redirect fails closed — including redirects issued by the accelerator.
func downloadVia(ctx context.Context, hc *http.Client, url, dst string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("updater: 下载返回 %s", resp.Status)
	}
	return copyCapped(dst, resp.Body, maxAssetSize)
}
