package screenshot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kbinani/screenshot"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// recorder captures a screen region into an animated GIF.
//
// It is intentionally a plain polling loop: GIF cannot express variable frame
// rates usefully, the captured content is static UI rather than motion, and a
// fixed cadence keeps the encoder's delay field meaningful. It also avoids
// depending on anything that would need cgo.
type recorder struct {
	feat *Feature
	ctx  *core.Context

	region image.Rectangle // 屏幕坐标
	fps    int

	enc *frameEncoder

	mu       sync.Mutex
	stopFn   context.CancelFunc
	stopped  bool
	finished bool
	// done is closed by loop when it returns. Waiting on it (rather than on a
	// shared WaitGroup) is what makes finish/discard safe: the recorder owns its
	// own lifetime, so an unrelated goroutine cannot make them block.
	done chan struct{}
	// err holds the terminal error, if any, for the UI to report.
	err error
}

// beginRecording starts capturing region at the configured fps.
//
// region must be in SCREEN coordinates: the editor hands over a screen-space
// rectangle because that is what a screen capture takes, and the editor window
// has already been parked outside the region by the time this runs.
//
// The capture runs on its own goroutine and stops when finish is called, when
// the application context is cancelled, or when the frame cap is reached.
func (f *Feature) beginRecording(region image.Rectangle, fps int) (*recorder, error) {
	if f.ctx == nil {
		return nil, errNotReady
	}
	if region.Dx() <= 0 || region.Dy() <= 0 {
		return nil, fmt.Errorf("screenshot: 录屏区域无效")
	}

	enc := newFrameEncoder(region.Dx(), region.Dy(), fps)
	if enc == nil {
		return nil, fmt.Errorf("screenshot: 录屏区域无效")
	}

	loopCtx, cancel := context.WithCancel(f.ctx.Ctx)
	rec := &recorder{
		feat:   f,
		ctx:    f.ctx,
		region: region,
		fps:    fps,
		enc:    enc,
		stopFn: cancel,
		done:   make(chan struct{}),
	}

	f.mu.Lock()
	if f.rec != nil {
		f.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("screenshot: 已有录屏在进行中")
	}
	f.rec = rec
	f.mu.Unlock()

	f.wg.Add(1)
	go rec.loop(loopCtx)

	f.ctx.Logger.Info("录屏已开始", "module", moduleID,
		"w", region.Dx(), "h", region.Dy(), "fps", fps)
	f.ctx.Bus.Log(moduleID, "info", fmt.Sprintf("录屏中 %d×%d @ %dfps", region.Dx(), region.Dy(), fps))
	return rec, nil
}

// loop samples the region until stopped.
func (r *recorder) loop(loopCtx context.Context) {
	defer r.feat.wg.Done()
	defer close(r.done)
	defer r.feat.clearRecorder(r)

	interval := time.Second / time.Duration(r.fps)
	if interval <= 0 {
		interval = time.Second / recDefaultFPS
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-loopCtx.Done():
			return
		case <-ticker.C:
			// The editor parks itself outside the region while recording, so the
			// frame here is the real screen content and not our own overlay.
			img, err := screenshot.CaptureRect(r.region)
			if err != nil {
				// A failed frame is not worth aborting the recording; note it and
				// keep going so a transient error does not lose the take.
				r.ctx.Logger.Warn("录屏取帧失败", "module", moduleID, "err", err)
				continue
			}
			if !r.enc.add(img) {
				// Frame cap reached: stop rather than recording into the void.
				r.ctx.Logger.Info("录屏已达帧数上限，自动停止", "module", moduleID,
					"frames", r.enc.count())
				r.ctx.Bus.Notice(moduleID, "录屏已达时长上限，已自动停止")
				return
			}
			r.publishProgress()
		}
	}
}

// publishProgress reports the elapsed time to the panel.
func (r *recorder) publishProgress() {
	n := r.enc.count()
	secs := n / r.fps
	r.ctx.Bus.Progress(moduleID, actionRecord, 0,
		fmt.Sprintf("录屏中 %d:%02d（%d 帧）", secs/60, secs%60, n))
}

// finish stops the recording and returns the encoded GIF bytes.
func (r *recorder) finish() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("screenshot: 没有进行中的录屏")
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return nil, fmt.Errorf("screenshot: 录屏已结束")
	}
	r.finished = true
	stop := r.stopFn
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
	// Wait for the loop to observe the cancellation so no frame is added while
	// the encoder is being read.
	<-r.done

	if r.enc.count() == 0 {
		return nil, fmt.Errorf("screenshot: 没有录到任何帧")
	}

	var buf bytes.Buffer
	if err := r.enc.encode(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// discard stops the recording and throws the frames away.
//
// It still waits for the loop to leave, so the caller can be sure no further
// capture happens once it returns; that is what makes "cancel" mean cancelled.
func (r *recorder) discard() {
	if r == nil {
		return
	}
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
}

// frameCount reports how many frames have been captured so far.
func (r *recorder) frameCount() int {
	if r == nil {
		return 0
	}
	return r.enc.count()
}

// clearRecorder forgets the finished recorder so a new one can start.
func (f *Feature) clearRecorder(r *recorder) {
	f.mu.Lock()
	if f.rec == r {
		f.rec = nil
	}
	f.mu.Unlock()
	// No encoding happens here: the editor polls the module for completion and
	// then asks it to stop, which is the single path that finalises a take.
	// Encoding in this defer would block the capture loop's own exit.
	f.ctx.Bus.State(moduleID, f.State())
}

// isFinished reports whether finish/discard already claimed the recorder.
func (r *recorder) isFinished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finished
}

// activeRecorder returns the in-flight recorder, if any.
func (f *Feature) activeRecorder() *recorder {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rec
}

// recordingFPS reads the configured frame rate.
func (f *Feature) recordingFPS() int {
	if f.ctx == nil {
		return defaultRecFPS
	}
	n, ok := toInt(f.ctx.Config.Get(optRecFPS, defaultRecFPS))
	if !ok || n < recMinFPS || n > recMaxFPS {
		return defaultRecFPS
	}
	return n
}

// recordingFormat reads the configured recording format, degrading to GIF
// whenever MP4 is requested but ffmpeg is missing（探测失败时静默回退，
// 面板的选项列表里 MP4 本就不可选，这里是双保险）。
func (f *Feature) recordingFormat() string {
	s := defaultRecFormat
	if f.ctx != nil {
		if v, ok := f.ctx.Config.Get(optRecFormat, defaultRecFormat).(string); ok {
			s = v
		}
	}
	if s == "mp4" && !ffmpegAvailable() {
		return defaultRecFormat
	}
	return s
}

// saveRecording writes GIF bytes to the screenshot directory.
func (f *Feature) saveRecording(data []byte) (string, error) {
	dir := f.saveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("screenshot: 无法创建目录 %s: %w", dir, err)
	}
	name := filepath.Join(dir, fmt.Sprintf("recording_%d.gif", time.Now().UnixMilli()))
	if err := os.WriteFile(name, data, 0o644); err != nil {
		return "", err
	}
	f.remember(name)
	f.ctx.Logger.Info("录屏已保存", "module", moduleID, "file", name, "bytes", len(data))
	f.ctx.Bus.Notice(moduleID, "录屏已保存 "+filepath.Base(name))
	return name, nil
}

// copyRecording puts the GIF on the clipboard.
//
// GIF is not an image format the Windows clipboard accepts as CF_BITMAP, so the
// bytes are offered as a file drop instead (what a chat client expects), falling
// back to reporting that only saving is available.
func (f *Feature) copyRecording(data []byte) error {
	path, err := f.saveRecording(data)
	if err != nil {
		return err
	}
	if err := copyFileToClipboard(path); err != nil {
		f.ctx.Logger.Warn("复制录屏到剪贴板失败", "module", moduleID, "err", err)
		f.ctx.Bus.Notice(moduleID, "已保存录屏，但复制到剪贴板失败："+path)
		return err
	}
	f.ctx.Bus.Notice(moduleID, "录屏已复制到剪贴板")
	return nil
}
