package launcher

import "testing"

// TestPinyinAbbr 守护拼音首字母缩写：汉字转首字母、英文数字原样小写保留、
// 符号丢弃、空串安全。
func TestPinyinAbbr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"记事本", "jsb"},
		{"打开剪贴板历史", "dkjtbls"},
		{"中国", "zg"},
		{"iPhone 15测评", "iphone15cp"}, // 混合：英文数字小写保留，空格丢弃
		{"计算器", "jsq"},
		{"ABC", "abc"},
		{"", ""},
		{"   ", ""},
		{"！！！", ""}, // 纯符号：无缩写
	}
	for _, c := range cases {
		if got := pinyinAbbr(c.in); got != c.want {
			t.Errorf("pinyinAbbr(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestPinyinAbbrMultiPronunciation 抽查多音字取首选读音的行为：go-pinyin 的
// 首选对常见应用名词汇是稳定合理的（重→z、长→c）。
func TestPinyinAbbrMultiPronunciation(t *testing.T) {
	// 这些断言守护的是「确定性与合理首选」而非语言学的正确性：
	// 缩写搜索要的是稳定一致，而不是每个词都完美。
	if got := pinyinAbbr("重庆"); got != "cq" && got != "zq" {
		t.Errorf("多音字「重」应取确定读音, got %q", got)
	}
}
