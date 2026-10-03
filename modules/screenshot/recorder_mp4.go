package screenshot

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/sysutil"
)

// MP4 录制的边界与回退参数。
//
// 纯 Go 没有可用的 H.264 编码器，项目又约束零 cgo + 单二进制，因此 MP4 的
// 唯一可行路径是可选外部依赖 ffmpeg：本包把每帧以 PNG 字节流写到 ffmpeg 的
// stdin（image2pipe 解码 → libx264 编码 → MP4 容器）。
//
// 关键设计——流式落盘：帧数据从进程内存直接进入 ffmpeg 管道并写入目标文件，
// 编码产物不驻留 Go 堆。这使 8 小时录制与 5GB 体积上限天然安全（内存占用
// 恒定于一帧），上限检查退化为对输出文件的体积巡检而非内存守卫。
const (
	// recMP4MaxDuration 是需求规定的最长录制时长（8 小时）。
	recMP4MaxDuration = 8 * time.Hour
	// recMP4MaxBytes 是需求规定的单文件体积上限（5GB）。达到后自动停止
	// 并落盘已录内容，防止撑爆磁盘。
	recMP4MaxBytes = 5 << 30
)

// ffmpegAvailable reports whether an external ffmpeg binary can be found.
// 结果按进程缓存：PATH 在运行期不会变。
var ffmpegProbe struct {
	once sync.Once
	path string
	ok   bool
}

func ffmpegAvailable() bool {
	ffmpegProbe.once.Do(func() {
		ffmpegProbe.path, ffmpegProbe.ok = sysutil.Which("ffmpeg")
	})
	return ffmpegProbe.ok
}

// mp4Writer drives one ffmpeg process, feeding it PNG frames over stdin.
type mp4Writer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    string
	closed bool
	errCh  chan error
}

// newMP4Writer starts ffmpeg reading PNG frames from stdin and writing MP4 to
// out. fps 与 region 尺寸写进参数，避免 ffmpeg 二次探测。
//
// 参数选择：
//   - -framerate 在 image2pipe demuxer 上声明输入帧率；
//   - libx264 的 -preset veryfast 用 CPU 换实时性（录制不能落后于采集）；
//   - -crf 23 是视觉无损档；-pix_fmt yuv420p 保证播放器兼容性
//     （PNG 进来是 rgb24/rgba，直接 yuv444 出去很多播放器放不了）；
//   - -movflags +faststart 把 moov atom 挪到文件头，复制/拖动进度条不掉帧。
func newMP4Writer(out string, fps int) (*mp4Writer, error) {
	if fps < recMinFPS {
		fps = recMinFPS
	}
	if fps > recMaxFPS {
		fps = recMaxFPS
	}
	if ffmpegAvailable() == false {
		return nil, fmt.Errorf("screenshot: 未找到 ffmpeg，无法录制 MP4（已回退 GIF）")
	}

	cmd := exec.Command("ffmpeg",
		"-y",
		"-loglevel", "error",
		"-f", "image2pipe",
		"-vcodec", "png",
		"-framerate", fmt.Sprint(fps),
		"-i", "-",
		"-an",
		"-vcodec", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		out,
	)
	hideCmdWindow(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	w := &mp4Writer{cmd: cmd, stdin: stdin, out: out, errCh: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("screenshot: 启动 ffmpeg 失败: %w", err)
	}
	// ffmpeg 的诊断走 stderr；收尾时等待进程退出并收集错误。
	go func() {
		err := cmd.Wait()
		w.errCh <- err
	}()
	return w, nil
}

// addFrame encodes one frame as PNG and streams it into the pipe.
//
// 返回 false 表示管道已断（ffmpeg 退出/磁盘满），调用方应停止录制。
func (w *mp4Writer) addFrame(img *image.RGBA) bool {
	if w.closed {
		return false
	}
	if err := png.Encode(w.stdin, img); err != nil {
		return false
	}
	return true
}

// close finishes the file: closing stdin makes ffmpeg finalise the container
// (moov atom), then we wait briefly for a clean exit.
func (w *mp4Writer) close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.stdin.Close(); err != nil {
		return err
	}
	select {
	case err := <-w.errCh:
		return err
	case <-time.After(30 * time.Second):
		// 等待超时：杀掉进程避免泄漏，文件大概率仍可用（moov 未写完则播放器报错）。
		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		return fmt.Errorf("screenshot: ffmpeg 收尾超时，文件可能不完整")
	}
}

// abort tears the recording down without finalising (used on size overflow
// where the file is still renamed into place — frames already on disk are fine).
func (w *mp4Writer) abort() {
	if w.closed {
		return
	}
	w.closed = true
	_ = w.stdin.Close()
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	<-w.errCh
}

// hideCmdWindow keeps an ffmpeg console from flashing over the game/screen
// being recorded (Windows GUI subsystem detail, same as everywhere else).
func hideCmdWindow(cmd *exec.Cmd) {
	hideCmdWindowOS(cmd)
}
