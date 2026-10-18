//go:build ignore

// yamlcheck 校验 .github/workflows/*.yml 的结构。
//
// 用 //go:build ignore 排除在常规构建之外，因此它不影响 `go build ./...`、
// `go vet ./...` 与任何测试；只在 scripts/check-workflows.sh 里通过
// `go run scripts/yamlcheck.go` 显式执行。
//
// 依赖 gopkg.in/yaml.v3 —— 它已经是 go.mod 的直接依赖，无需额外安装。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	patterns := []string{".github/workflows/*.yml", ".github/workflows/*.yaml"}
	var files []string
	for _, p := range patterns {
		matched, _ := filepath.Glob(p)
		files = append(files, matched...)
	}
	if len(files) == 0 {
		fmt.Println("SKIP 未找到 workflow 文件")
		return
	}
	sort.Strings(files)

	failed := false
	for _, path := range files {
		if err := check(path); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", path, err)
			failed = true
			continue
		}
		fmt.Printf("OK   %s\n", path)
	}
	if failed {
		os.Exit(1)
	}
}

func check(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("YAML 解析失败: %w", err)
	}
	if _, ok := doc["jobs"]; !ok {
		return fmt.Errorf("缺少 jobs 段")
	}

	// 多行脚本被压成一行时，YAML 仍能解析，但 shell 语义已经坏了：后续赋值
	// 会被同一个 # 注释吃掉。典型特征是 run: | 块里出现超长行。
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if len(line) > 500 && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			return fmt.Errorf("第 %d 行过长（%d 字符），多行脚本疑似被压成一行", i+1, len(line))
		}
	}
	return nil
}
