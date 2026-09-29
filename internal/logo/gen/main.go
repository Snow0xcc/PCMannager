// Command gen 生成文档用的品牌 SVG（README 页眉 / Wiki / GitHub Pages）。
//
// 它是 internal/logo 的几何到静态资源的**单向出口**：程序自身不解析 SVG，托盘
// 图标走 logo.Render 直接光栅化成 HICON；这里生成的只是同一个几何在文档里的
// 镜像，让"文档上的蝴蝶"和"托盘里的蝴蝶"永远同源。
//
// 用法（由 scripts/gen-logo.sh 包装，仓库根目录下执行）：
//
//	go run ./internal/logo/gen              # 重新生成
//	go run ./internal/logo/gen -check        # 只校验已提交文件是否与几何一致
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/snow0xcc/pcmannager/internal/logo"
)

func main() {
	out := flag.String("out", "docs/site/assets", "输出目录（默认为仓库根下的 docs/site/assets）")
	check := flag.Bool("check", false, "只校验已有文件与几何是否一致，不写盘（用于 CI）")
	flag.Parse()

	// 顺序即输出顺序，保证错误信息稳定可复现。
	assets := []struct{ name, body string }{
		{"icon.svg", logo.MarkSVG()},
		{"logo.svg", logo.LogoSVG()},
	}

	if !*check {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fatal(err)
		}
	}

	stale := false
	for _, a := range assets {
		path := filepath.Join(*out, a.name)
		old, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			fatal(err)
		}
		if string(old) == a.body {
			continue
		}
		if *check {
			fmt.Printf("过期: %s —— 请运行 bash scripts/gen-logo.sh 重新生成\n", path)
			stale = true
			continue
		}
		if err := os.WriteFile(path, []byte(a.body), 0o644); err != nil {
			fatal(err)
		}
		fmt.Println("已生成", path)
	}

	if stale {
		os.Exit(1)
	}
	if *check {
		fmt.Println("品牌 SVG 与 internal/logo 几何一致")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen-logo:", err)
	os.Exit(1)
}
