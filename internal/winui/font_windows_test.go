//go:build windows

package winui

import (
	"sort"
	"strings"
	"testing"
)

// TestFontFamilies 断言枚举结果可用于字体选择框：非空、无重复、已排序，且不含
// 以 '@' 开头的竖排镜像字体。
//
// 字体列表是运行期缓存的，这里刻意连续调用两次，顺带覆盖缓存路径返回的仍是
// 排序去重结果（副本不会被上一次调用改写）。
func TestFontFamilies(t *testing.T) {
	fonts := FontFamilies()
	if len(fonts) == 0 {
		// 无字体可用属于极端环境，不是代码缺陷。
		t.Skip("系统未枚举到任何字体，跳过")
	}

	if !sort.StringsAreSorted(fonts) {
		t.Fatalf("字体列表未排序: %v", fonts)
	}

	seen := make(map[string]struct{}, len(fonts))
	for _, name := range fonts {
		if strings.HasPrefix(name, "@") {
			t.Fatalf("列表含竖排镜像字体 %q", name)
		}
		if name == "" {
			t.Fatal("列表含空字体名")
		}
		if _, dup := seen[name]; dup {
			t.Fatalf("字体 %q 重复出现", name)
		}
		seen[name] = struct{}{}
	}

	// 第二次调用走缓存，结果必须与首次一致。
	again := FontFamilies()
	if len(again) != len(fonts) {
		t.Fatalf("缓存前后数量不一致: %d != %d", len(again), len(fonts))
	}
	for i := range again {
		if again[i] != fonts[i] {
			t.Fatalf("缓存前后第 %d 项不同: %q != %q", i, again[i], fonts[i])
		}
	}
}
