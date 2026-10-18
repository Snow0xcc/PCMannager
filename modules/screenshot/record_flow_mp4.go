package screenshot

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// mp4Recorder 与 recorder（GIF）并行的 MP4 录制会话。
//
// 与 GIF 版的本质差异：帧不进内存。每帧 PNG 直写 ffmpeg 管道 → 磁盘，
// 因此没有帧数上限，只有时长（8h）与文件体积（5GB）两个边界，
// 两者都在采样循环里巡检，触达即收尾落盘。
type mp4Recorder struct {
	feat *Feature
	ctx  *core.Context

	region image.Rectangle // 屏幕坐标
	fps    int
	w      *mp4Writer
	out    string

	mu       sync.Mutex
	stopFn   context.CancelFunc
	finished bool
	done     chan struct{}
	frames   int64
	stopped  string // 自动停止原因（"" = 正常收尾）
}

// beginRecordingMP4 starts a streaming MP4 recording of region.
func (f *Feature) beginRecordingMP4(region image.Rectangle, fps int) (*mp4Recorder, error) {
	if f.ctx == nil {
		return nil, errNotReady
	}
	if region.Dx() <= 0 || region.Dy() <= 0 {
		return nil, fmt.Errorf("screenshot: 录屏区域无效")
	}
	if !ffmpegAvailable() {
		return nil, fmt.Errorf("screenshot: 未安装 ffmpeg，无法录制 MP4")
	}

	out := filepath.Join(f.saveDir(), fmt.Sprintf("recording_%d.mp4", time.Now().UnixMilli()))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, fmt.Errorf("screenshot: 无法创建输出目录: %w", err)
	}
	w, err := newMP4Writer(out, fps)
	if err != nil {
		return nil, err
	}

	loopCtx, cancel := context.WithCancel(f.ctx.Ctx)
	rec := &mp4Recorder{
		feat:   f,
		ctx:    f.ctx,
		region: region,
		fps:    fps,
		w:      w,
		out:    out,
		stopFn: cancel,
		done:   make(chan struct{}),
	}

	f.mu.Lock()
	if f.mp4 != nil || f.rec != nil {
		f.mu.Unlock()
		cancel()
		w.abort()
		return nil, fmt.Errorf("screenshot: 已有录屏在进行中")
	}
	f.mp4 = rec
	f.mu.Unlock()

	f.wg.Add(1)
	go rec.loop(loopCtx)

	f.ctx.Logger.Info("MP4 录屏已开始", "module", moduleID,
		"w", region.Dx(), "h", region.Dy(), "fps", fps, "out", out)
	f.ctx.Bus.Log(moduleID, "info", fmt.Sprintf("MP4 录屏中 %d×%d @ %dfps", region.Dx(), region.Dy(), fps))
	return rec, nil
}

// loop 采样并把帧推进管道。时长与体积两个边界都在这里巡检。
func (r *mp4Recorder) loop(loopCtx context.Context) {
	defer close(r.done)
	defer r.feat.wg.Done()
	defer r.feat.clearMP4(r)

	started := time.Now()
	interval := time.Second / time.Duration(r.fps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-loopCtx.Done():
			return
		case <-ticker.C:
			// 边界 1：时长。8 小时到达后收尾（ffmpeg 正常 finalise）。
			if time.Since(started) >= recMP4MaxDuration {
				r.mu.Lock()
				r.stopped = "已达最长录制时长 8 小时"
				r.mu.Unlock()
				r.ctx.Bus.Notice(moduleID, "录屏已达最长时长，自动停止")
				return
			}
			// 边界 2：体积。流式写盘后产物大小直接 stat 输出文件；
			// 每 32 帧查一次（4GB 处第一次触发前的开销可忽略）。
			if r.frames%32 == 0 {
				if fi, err := os.Stat(r.out); err == nil && fi.Size() >= recMP4MaxBytes {
					// 体积超限：abort（不 finalise）已够——数据都已在盘上。
					r.mu.Lock()
					r.stopped = "文件已达 5GB 上限"
					r.mu.Unlock()
					r.ctx.Bus.Notice(moduleID, "录屏文件已达 5GB，自动停止")
					return
				}
			}

			img, err := grabRegion(r.region)
			if err != nil {
				r.ctx.Logger.Warn("MP4 录屏取帧失败", "module", moduleID, "err", err)
				continue
			}
			if !r.w.addFrame(img) {
				// 管道断开：ffmpeg 已退出（磁盘满/被杀）。
				r.mu.Lock()
				r.stopped = "编码器已退出（磁盘空间不足或 ffmpeg 异常）"
				r.mu.Unlock()
				r.ctx.Bus.Notice(moduleID, "录屏异常终止，已保存已录部分")
				return
			}
			r.mu.Lock()
			r.frames++
			n := r.frames
			r.mu.Unlock()

			secs := int(time.Since(started).Seconds())
			r.ctx.Bus.Progress(moduleID, actionRecord, 0,
				fmt.Sprintf("MP4 录屏中 %d:%02d:%02d（%d 帧）", secs/3600, secs%3600/60, secs%60, n))
		}
	}
}

// stop stops the loop and finalises the MP4, returning the output path.
func (r *mp4Recorder) stop() (string, error) {
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return r.out, nil
	}
	r.finished = true
	stop := r.stopFn
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
	<-r.done

	// 关闭 stdin 让 ffmpeg 写完 moov（容器收尾），这一步不可省。
	if err := r.w.close(); err != nil {
		r.ctx.Logger.Warn("MP4 收尾异常", "module", moduleID, "err", err)
	}
	if r.frames == 0 {
		_ = os.Remove(r.out)
		return "", fmt.Errorf("screenshot: 没有录到任何帧")
	}
	r.feat.remember(r.out)
	r.ctx.Logger.Info("MP4 录屏已保存", "module", moduleID, "file", r.out, "frames", r.frames)
	return r.out, nil
}

// abort discards the recording: kill ffmpeg, delete the partial file.
func (r *mp4Recorder) abort() {
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	r.finished = true
	stop := r.stopFn
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
	<-r.done
	r.w.abort()
	_ = os.Remove(r.out)
	r.ctx.Bus.Notice(moduleID, "已丢弃本次 MP4 录屏")
}

// frameCount reports frames captured so far.
func (r *mp4Recorder) frameCount() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frames
}

// stopReason reports why the loop self-stopped ("" = still running or user stop).
func (r *mp4Recorder) stopReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

// clearMP4 forgets the finished session.
func (f *Feature) clearMP4(r *mp4Recorder) {
	f.mu.Lock()
	if f.mp4 == r {
		f.mp4 = nil
	}
	f.mu.Unlock()
	f.ctx.Bus.State(moduleID, f.State())
}
