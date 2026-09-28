package launcher

import (
	"strings"

	"github.com/mozillazg/go-pinyin"
)

// 拼音首字母缩写：给中文候选自动生成搜索别名（如「记事本」→ jsb），
// 让用户不必手动录入拼音缩写也能用缩写命中。
//
// 选型：mozillazg/go-pinyin 与 pinyin-data/pypinyin 同作者，字典编译内嵌
// 进二进制（无外置数据文件），纯 Go 无 cgo，符合项目 CGO_ENABLED=0 硬约束。

// a 是共享的转换参数：无声调首字母风格。go-pinyin 的 Args 含内部缓存语义，
// 文档建议复用同一实例。
var pinyinArgs = pinyin.NewArgs()

func init() {
	pinyinArgs.Style = pinyin.FirstLetter
	pinyinArgs.Heteronym = false // 多音字取首选（普通应用名场景足够）
}

// pinyinAbbr 返回 s 的拼音首字母缩写（小写）：
//   - 汉字逐字转首字母（多音字取最常用读音，如「重」→ z）；
//   - 非汉字字符（英文/数字）原样小写保留，便于混合搜索（iPhone 15测评 → iphone15cp）；
//   - 其它符号（空格/标点）丢弃。
//
// 结果为空串时返回 nil（无别名可加）。
func pinyinAbbr(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case isHanRune(r):
			ret := pinyin.Pinyin(string(r), pinyinArgs)
			if len(ret) > 0 && len(ret[0]) > 0 && ret[0][0] != "" {
				b.WriteString(ret[0][0])
			}
		case isAsciiAlnum(r):
			b.WriteRune(lowerAscii(r))
		default:
			// 空格/标点/符号丢弃。
		}
	}
	return b.String()
}

// isHanRune 判断是否 CJK 统一表意文字（含扩展A区，覆盖 go-pinyin 字典范围）。
func isHanRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // Extension A
		(r >= 0x20000 && r <= 0x2A6DF) // Extension B
}

// isAsciiAlnum 判断 ASCII 字母或数字。
func isAsciiAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// lowerAscii 小写化 ASCII 字母（非字母原样返回）。
func lowerAscii(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
