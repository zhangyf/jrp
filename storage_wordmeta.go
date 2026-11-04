package main

import (
	"context"
)

// WordMeta / WordPOS 类型已迁 internal/wordmeta（含完整字段注释），
// 根包靠 types.go 的别名保持这两个名字不变；本文件只留 word_meta.json
// 的 COS 读写。读不到就返回空表，绝不因此把词汇总表打不开。
func (s *Storage) wordMetaKey() string {
	return s.sharedBase() + "/word_meta.json"
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
