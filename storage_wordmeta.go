package main

import (
	"context"
)

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
type WordPOS struct {
	Pos string `json:"pos"`
	Sub string `json:"sub"`
}

func (s *Storage) wordMetaKey() string {
	return s.cosPrefix() + "/word_meta.json"
}

// DownloadWordMeta 读词性表。
//
// 不存在或解析失败 → 返回空表而不是报错：词性只是展示增强，
// 读不到就让页面上这列空着，绝不能因此把整个词汇总表打不开。
func (s *Storage) DownloadWordMeta(ctx context.Context) (*WordMeta, error) {
	data, err := s.store.GetAll(ctx, s.wordMetaKey())
	if err != nil {
		return &WordMeta{Items: map[string]WordPOS{}}, nil
	}
	var m WordMeta
	if err := jsonUnmarshal(data, &m); err != nil {
		return &WordMeta{Items: map[string]WordPOS{}}, nil
	}
	if m.Items == nil {
		m.Items = map[string]WordPOS{}
	}
	return &m, nil
}

// UploadWordMeta 写词性表。演练模式下不落盘，与其他 Upload* 保持一致。
func (s *Storage) UploadWordMeta(ctx context.Context, m *WordMeta) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.wordMetaKey(), []byte(toJSON(m)))
}
