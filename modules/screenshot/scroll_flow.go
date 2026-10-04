package screenshot

import (
	"context"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Scrolling-capture parameters.
const (
	// scrollSettle is how long to wait after injecting a wheel notch before
	// sampling. Sampling too early catches a half-painted frame, whose rows
	// would not match and would abort the stitch.
	scrollSettle = 160 * time.Millisecond

	// scrollNotches is the wheel step injected per round in auto mode. Three
	// detents is a typical step for a text view: a smaller step makes the
	// capture slower and more prone to missing overlap, a larger one risks
	// jumping past the search window entirely.
	scrollNotches = -3

	// scrollMaxHeight bounds the stitched image. An unbounded long shot is a
	// memory leak with a user attached; past this height the capture stops and
	// keeps what it has.
	scrollMaxHeight = 20000

	// scrollIdleTicks is how many consecutive frames with no new rows are taken
	// as "we have reached the bottom or the user stopped scrolling".
	scrollIdleTicks = 4
)

// scrollCapture accumulates a long screenshot from a region the user scrolls.
//
// It owns a sampling loop rather than hooking scroll events: matching captured
// pixels is the only approach that works on arbitrary applications without any
// per-application integration, and it also makes manual scrolling (where the
// user drives) and automatic scrolling (where we inject the wheel) the same code
// path.
type scrollCapture struct {
	feat *Feature
	ctx  *core.Context

	// region is the captured area in SCREEN coordinates.
	region image.Rectangle
	// auto selects injected scrolling instead of waiting for the user.
	auto bool
	// interval is the sampling period, read from the configuration.
	interval time.Duration

	mu     sync.Mutex
	stitch *scrollStitcher
	// stopFn cancels the sampling loop; stopReq makes that a one-shot action.
	stopFn  context.CancelFunc
	stopReq bool
	// reason explains why the loop ended; "" means it was cancelled from outside
	// (so the caller still wants to collect the result).
	reason string
	// done is closed by loop when it returns, which is what makes collecting the
	// result safe without a shared WaitGroup.
	done chan struct{}
}

// beginScrollCapture starts sampling region for a long screenshot.
//
// The caller must have parked the editor window outside region; the sampling
// loop captures the real screen, so an overlay inside the region would be
// stitched into the result.
func (f *Feature) beginScrollCapture(region image.Rectangle, auto bool) (*scrollCapture, error) {
	if f.ctx == nil {
		return nil, errNotReady
	}
	if region.Dx() <= 0 || region.Dy() <= 0 {
		return nil, fmt.Errorf("screenshot: 滚动截图区域无效")
	}
	// A region as tall as the screen is almost always the wrong selection: the
	// stitcher needs the remembered strip to still overlap in the next frame,
	// which a full-height region cannot guarantee once it scrolls. Reject it
	// rather than produce a garbage long shot.
	if h := f.displayHeight(); h > 0 && region.Dy() >= h {
		return nil, fmt.Errorf("screenshot: 滚动截图区域高度需小于屏幕高度")
	}

	loopCtx, cancel := context.WithCancel(f.ctx.Ctx)
	sc := &scrollCapture{
		feat:     f,
		ctx:      f.ctx,
		region:   region,
		auto:     auto,
		interval: f.scrollInterval(),
		stopFn:   cancel,
		done:     make(chan struct{}),
	}

	f.mu.Lock()
	if f.scroll != nil {
		f.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("screenshot: 已有滚动截图在进行中")
	}
	f.scroll = sc
	f.mu.Unlock()

	f.wg.Add(1)
	go sc.loop(loopCtx)

	mode := "手动滚动"
	if auto {
		mode = "自动滚动"
	}
	f.ctx.Logger.Info("滚动截图已开始", "module", moduleID,
		"w", region.Dx(), "h", region.Dy(), "auto", auto)
	f.ctx.Bus.Log(moduleID, "info", fmt.Sprintf("滚动截图（%s）%d×%d", mode, region.Dx(), region.Dy()))
	return sc, nil
}

// loop samples and stitches frames until stopped.
func (s *scrollCapture) loop(loopCtx context.Context) {
	defer close(s.done)
	defer s.feat.wg.Done()
	defer s.feat.clearScroller(s)

	first, err := grabRegion(s.region)
	if err != nil {
		s.finishWith("首帧捕获失败：" + err.Error())
		s.ctx.Logger.Error("滚动截图首帧失败", "module", moduleID, "err", err)
		return
	}
	st := newScrollStitcher(first)
	if st == nil {
		s.finishWith("首帧无效")
		return
	}
	s.mu.Lock()
	s.stitch = st
	s.mu.Unlock()

	// Centre the cursor on the region before any injection: SendInput delivers
	// the wheel to whatever is under the pointer, and the pointer may well be
	// somewhere else entirely, since the capture was started from the panel.
	if s.auto {
		s.moveCursorToRegion()
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	idle := 0

	for {
		select {
		case <-loopCtx.Done():
			return
		case <-ticker.C:
			if s.auto {
				s.moveCursorToRegion()
				if !winui.ScrollWheel(scrollNotches) {
					s.ctx.Logger.Warn("注入滚动失败，停止自动滚动", "module", moduleID)
					s.finishWith("系统拒绝了滚动事件，已停止")
					return
				}
				// Let the target repaint before sampling it.
				time.Sleep(scrollSettle)
			}

			img, err := grabRegion(s.region)
			if err != nil {
				// A dropped frame loses nothing: the stitcher picks up from the
				// last good position on the next tick.
				s.ctx.Logger.Warn("滚动截图取帧失败", "module", moduleID, "err", err)
				continue
			}

			res := st.append(img)
			if !res.Matched {
				// No overlap: the next frame cannot be placed, so continuing
				// would silently duplicate or drop rows. Stop and keep what we
				// have rather than corrupt the result.
				s.finishWith("滚动过快或内容已变化，已停止拼接")
				return
			}
			if res.Advanced == 0 {
				idle++
				if idle >= scrollIdleTicks {
					s.finishWith("已到达底部，拼接完成")
					return
				}
			} else {
				idle = 0
			}
			if st.height() >= scrollMaxHeight {
				s.finishWith("已达到最大长度，已停止")
				return
			}
			s.publishProgress(st)
		}
	}
}

// moveCursorToRegion puts the pointer at the region's centre.
func (s *scrollCapture) moveCursorToRegion() {
	cx := (s.region.Min.X + s.region.Max.X) / 2
	cy := (s.region.Min.Y + s.region.Max.Y) / 2
	winui.SetCursorPos(int32(cx), int32(cy))
}

// publishProgress reports the accumulated height to the panel.
func (s *scrollCapture) publishProgress(st *scrollStitcher) {
	s.ctx.Bus.Progress(moduleID, actionScroller, 0,
		fmt.Sprintf("滚动截图中，已拼接 %d 像素", st.height()))
}

// requestStop asks the sampling loop to finish, keeping the stitched content.
//
// It is idempotent and safe to call after the loop already exited on its own
// (the bottom was reached), which is what lets the editor treat "finished" and
// "user pressed stop" as the same path.
func (s *scrollCapture) requestStop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	already := s.stopReq
	s.stopReq = true
	stop := s.stopFn
	s.mu.Unlock()

	if !already && stop != nil {
		stop()
	}
	// Wait for the loop to observe the cancellation so no frame is appended
	// while the stitcher is being read.
	<-s.done
}

// stopRequested reports whether requestStop was called.
func (s *scrollCapture) stopRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopReq
}

// finishWith records the terminal reason; the loop returns right after.
func (s *scrollCapture) finishWith(reason string) {
	s.mu.Lock()
	s.reason = reason
	s.mu.Unlock()
	s.ctx.Bus.Progress(moduleID, actionScroller, 100, reason)
}

// image returns the stitched result so far, or nil.
func (s *scrollCapture) image() image.Image {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	st := s.stitch
	s.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.image()
}

// height reports the accumulated height in pixels.
func (s *scrollCapture) height() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stitch.height()
}

// stopReason reports why the capture ended, or "" while it is still running.
func (s *scrollCapture) stopReason() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// clearScroller forgets the finished capture once nobody can collect it.
//
// A capture that ended on its own (bottom reached, scrolling too fast) is KEPT:
// the editor's poll is what collects and saves it, so dropping the pointer here
// would throw the long shot away. Only an externally requested stop is cleared,
// because that stop's caller already holds the pointer.
func (f *Feature) clearScroller(s *scrollCapture) {
	if s == nil {
		return
	}
	f.mu.Lock()
	if f.scroll == s && s.stopRequested() {
		f.scroll = nil
	}
	f.mu.Unlock()
	f.ctx.Bus.State(moduleID, f.State())
}

// forgetScroller drops the reference unconditionally, after a result was taken.
func (f *Feature) forgetScroller(s *scrollCapture) {
	f.mu.Lock()
	if f.scroll == s {
		f.scroll = nil
	}
	f.mu.Unlock()
	f.ctx.Bus.State(moduleID, f.State())
}

// activeScroller returns the in-flight scrolling capture, if any.
func (f *Feature) activeScroller() *scrollCapture {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scroll
}

// activeMP4 returns the in-flight MP4 recording, if any.
func (f *Feature) activeMP4() *mp4Recorder {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mp4
}

// scrollInterval reads the configured sampling period.
func (f *Feature) scrollInterval() time.Duration {
	ms := defaultScrollWait
	if f.ctx != nil {
		if n, ok := toInt(f.ctx.Config.Get(optScrollWait, defaultScrollWait)); ok && n >= 120 && n <= 2000 {
			ms = n
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// displayHeight returns the primary display height, or 0 when unknown.
func (f *Feature) displayHeight() int {
	return primaryDisplayHeight()
}
