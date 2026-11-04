// Package wordmeta 存放词性表与三圆分级相关的纯类型和纯规则。
//
// COS 读写（word_meta.json）、档案写回（applyCircle / unholdLesson）、
// CLI 编排都留在根包；本包只放「给定数据 → 得到结果」的确定性逻辑，
// 不碰 Storage / HTTP / 全局配置，方便独立单测。
package words

// WordMeta 是整份词库的词性表。
//
// 为什么不直接给档案加一列「词性」：档案表格是 7 列，改列数会牵动
// ParseArchive / WriteArchive / Excel 导出 / 导入解析全链，风险太大。
// 词性只是「看着复习」用的附加信息，独立成一份 JSON 最省事 ——
// 丢了也不影响任何练习功能，最多页面上词性那列空着。
//
// 键用档案里的完整词形「假名(汉字)」，因为那是唯一标识
// （同假名不同汉字的词要分开：かきます(書きます) 写 / かきます 画）。
type WordMeta struct {
	Version int                `json:"version"`
	Updated string             `json:"updated"`
	Note    string             `json:"note"`
	Items   map[string]WordPOS `json:"items"`
}

// WordPOS 一个词的词性。Sub 是细分：
// 动词 → 一类(五段) / 二类(一段) / 三类(サ変・カ変)
// 形容词 → い形 / な形
//
// Note 是备注：主要写「跟哪个近义词怎么区分」（老师 2026-09-28 要的），
// 比如 通ります 的备注写「空间上从某处穿过；過ぎます 是时间流逝/超过某点」。
// 词义明确、不会跟别的词混的就空着 —— 空着是正常的，不是缺数据。
type WordPOS struct {
	Pos  string `json:"pos"`
	Sub  string `json:"sub"`
	Note string `json:"note,omitempty"`

	// Circle 是《标日初上 三圆词汇速记手册》给的分级：三圆 / 二圆 / 一圆。
	// 空串 = 手册里没有这个词（老师课外补录的，或者手册没收）。
	Circle string `json:"circle,omitempty"`

	// Hold = true 的词不进每日练习计划。
	//
	// 2026-11-02：按手册批量补录了 21–24 课的词，这些课根本还没学到，
	// 让它们涌进待复习列表毫无意义。挂起的词在词汇总表里照常显示，
	// 学到那一课时用 `jrp circle unhold --lesson N` 放开。
	Hold bool `json:"hold,omitempty"`
}

// CircleItem 一条待处理的词。
type CircleItem struct {
	Word   string `json:"word"`             // 档案词形：假名(汉字)
	Def    string `json:"def,omitempty"`    // 中文释义（新增时才用；含括号区分点）
	Pos    string `json:"pos,omitempty"`    // 词性大类
	Sub    string `json:"sub,omitempty"`    // 细分：一类/二类/三类、い形/な形
	Circle string `json:"circle"`           // 三圆 / 二圆 / 一圆
	Lesson int    `json:"lesson,omitempty"` // 手册课号，决定新词进哪个组
	Group  string `json:"group,omitempty"`  // 显式指定分组名，优先于 Lesson
}

// CircleInput 一次批量操作的输入。
type CircleInput struct {
	Language string       `json:"language,omitempty"`
	Words    []CircleItem `json:"words"`
	// DefFix 是「只改释义」的老词 —— 给原来没写区分括号的词补上，
	// 免得新词跟老词看中文分不出该写哪个（2026-11-02 补了 10 个）。
	DefFix []CircleItem `json:"def_fix,omitempty"`
}

// CircleOutcome 是执行结果。
type CircleOutcome struct {
	Added       int      `json:"added"`      // 新增进档案的词数
	Duplicates  int      `json:"duplicates"` // 词形已存在，跳过
	Tagged      int      `json:"tagged"`     // 打上/更新圆级标的词数
	Held        int      `json:"held"`       // 被挂起（不进练习）的词数
	DefFixed    int      `json:"def_fixed"`  // 改过释义的老词数
	OldFilename string   `json:"old_filename"`
	NewFilename string   `json:"new_filename"`
	Version     string   `json:"version"`
	TotalWords  int      `json:"total_words"`
	Groups      []string `json:"groups,omitempty"` // 这次动过的分组
	DupDefs     []string `json:"dup_definitions,omitempty"`
	DupCores    []string `json:"dup_core_definitions,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}
