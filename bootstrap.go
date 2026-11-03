package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/objstore"
)

// 新账号引导。
//
// 2026-11-02 小程序审核被拒暴露的洞：整个 app 默认「老师那种已有档案的老账号」，
// 一个刚扫码的新用户（含审核员）目录里**什么都没有**，于是
// /api/plan、/api/conjugation、/api/lexicon 全部 500「no archives found」
// —— 首页白屏、活用/词汇表报错，审核直接拒。
//
// 原则：
//   - **写路径**（plan/hard/录词）：EnsureArchive —— 没有就造一份空档案落盘，之后一切照旧。
//   - **纯读路径**（活用/词汇表）：不写 COS，空档案就回空数据（共 0 题 / 0 行），
//     页面自己有「共 0 题」的空态，不报错就是能体验。

// hasArchive 报告这个账号名下有没有档案。
func (s *Storage) hasArchive(ctx context.Context) bool {
	if s.store == nil {
		return false
	}
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: s.personalBase() + "/archives/", Delimiter: ""})
	return err == nil && len(objs) > 0
}

// EnsureArchive 拿最新档案；目录里没有就造一份空的并落盘。
//
// ⚠️ 只给**写路径**用（plan/hard/录词）。纯读接口用 loadArchiveForRead，
// 别让一次 GET 凭空写出对象。
func (s *Storage) EnsureArchive(ctx context.Context, lang string) (*Archive, string, error) {
	data, filename, err := s.DownloadLatestArchive(ctx)
	if err == nil {
		arc, perr := archive.ParseArchive(string(data), lang)
		if perr != nil {
			return nil, "", fmt.Errorf("解析档案失败：%w", perr)
		}
		return arc, filename, nil
	}
	if s.hasArchive(ctx) {
		// 有档案却读不出来 = 真故障，不能拿空档案把老师的数据盖了。
		return nil, "", fmt.Errorf("读取档案失败：%w", err)
	}
	arc := &Archive{Language: lang}
	cfg := LangConfigs[lang]
	fname := archive.Filename(LangConfigs[lang].FilePrefix, time.Now(), 1, 0)
	if !s.dryRun {
		if uerr := s.UploadArchive(ctx, fname, []byte(archive.WriteArchive(arc, cfg.Name, cfg.WordColumn))); uerr != nil {
			return nil, "", fmt.Errorf("初始化档案失败：%w", uerr)
		}
	}
	return arc, fname, nil
}

// LoadArchiveForRead 纯读路径用：有档案给档案，没有给空档案（**不落盘**）。
// 真故障（有档案但读/解析失败）才返回错误。
func (s *Storage) LoadArchiveForRead(ctx context.Context, lang string) (*Archive, error) {
	data, _, err := s.DownloadLatestArchive(ctx)
	if err == nil {
		return archive.ParseArchive(string(data), lang)
	}
	if s.hasArchive(ctx) {
		return nil, fmt.Errorf("读取档案失败：%w", err)
	}
	return &Archive{Language: lang}, nil
}
