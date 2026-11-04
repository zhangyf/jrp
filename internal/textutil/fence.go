// Package textutil 放 worddef / vision 两个用例包共用的纯文本规则（无 IO、无状态）。
package textutil

import "strings"

// StripCodeFence 去掉模型回复外面套的 ```json ... ``` 代码块外壳。
//
// P4 时 worddef / vision 各留了一份逐字一致的私有副本，为的是让两个用例包互不依赖；
// P5 薄壳合并（老师「收敛点选甲」）收到这里，两域共用一份，函数体逐字不变。
func StripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "```")
	if i < 0 {
		return s
	}
	s = s[i+3:]
	if j := strings.Index(s, "```"); j >= 0 {
		s = s[:j]
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "json") {
		s = strings.TrimSpace(s[4:])
	}
	return s
}
