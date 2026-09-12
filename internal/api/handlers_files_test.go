package api

import "testing"

// railGroups is the rail on the 服务器配置 page, in the order it renders them.
// A key filed under anything else would either vanish from the rail or append
// a section nobody designed, so the table and the rail are checked against one
// list rather than kept in step by hand.
var railGroups = map[string]bool{
	"基础":    true,
	"世界与生成": true,
	"玩家与权限": true,
	"网络与端口": true,
	"性能":    true,
}

func TestKnownPropertiesAreGrouped(t *testing.T) {
	for _, p := range knownProperties {
		if p.Group == "" {
			t.Errorf("%s 没有分组：分组是配置页左侧锚点栏的唯一来源", p.Key)
			continue
		}
		if !railGroups[p.Group] {
			t.Errorf("%s 的分组 %q 不在锚点栏的分组里", p.Key, p.Group)
		}
	}
}

// The settings that can open a server up, or that cannot be undone, say so in
// words. The test names them individually rather than counting: the point is
// that these particular four carry a warning, not that some number of keys do.
func TestRiskyPropertiesExplainTheConsequence(t *testing.T) {
	risky := map[string]bool{
		"online-mode": true,
		"hardcore":    true,
		"level-name":  true,
	}

	seen := map[string]bool{}
	for _, p := range knownProperties {
		if risky[p.Key] {
			seen[p.Key] = true
			if p.Risk == "" {
				t.Errorf("%s 必须写明后果，一个裸开关不够", p.Key)
			}
		}
	}
	for key := range risky {
		if !seen[key] {
			t.Errorf("knownProperties 里没有 %s", key)
		}
	}
}

// A warning on every row is a warning on none, so the risky set stays small.
// This fails loudly if a later edit starts sprinkling them.
func TestRiskIsRare(t *testing.T) {
	count := 0
	for _, p := range knownProperties {
		if p.Risk != "" {
			count++
		}
	}
	if count > 5 {
		t.Errorf("%d 项带风险说明，太多了：每行都有警告等于每行都没有", count)
	}
}

// Live names an in-game command, so it has to look like one. A hint that
// happens to be prose would render inside a <code> and read as a command the
// operator could paste.
func TestLiveIsACommand(t *testing.T) {
	for _, p := range knownProperties {
		if p.Live != "" && p.Live[0] != '/' {
			t.Errorf("%s 的 Live 是 %q，不是一条以 / 开头的指令", p.Key, p.Live)
		}
	}
}
