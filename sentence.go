package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zhangyf/jrp/internal/sentence"
)

// ---------- COS key ----------

// sentenceBankKey 主句库 = 教材，全站共享。
func (s *Storage) sentenceBankKey() string { return s.sharedBase() + "/plans/sentence_bank.json" }

// sentenceHistoryKey / sentenceWrongKey 是练习痕迹（谁做过哪句、哪句错了），
// 按用户隔离 —— 共享的话儿子做过的句子老师就练不到了。
func (s *Storage) sentenceHistoryKey() string {
	return s.personalBase() + "/plans/sentence_history.json"
}
func (s *Storage) sentenceWrongKey() string { return s.personalBase() + "/plans/sentence_wrong.json" }

// ---------- Storage 层读写 ----------

func (s *Storage) DownloadSentenceBank(ctx context.Context) (*sentence.SentenceBank, error) {
	data, err := s.store.GetAll(ctx, s.sentenceBankKey())
	if err != nil {
		return nil, err
	}
	var b sentence.SentenceBank
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("解析句库失败：%w", err)
	}
	return &b, nil
}

func (s *Storage) UploadSentenceBank(ctx context.Context, b *sentence.SentenceBank) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sentenceBankKey(), []byte(toJSON(b)))
}

func (s *Storage) DownloadSentenceHistory(ctx context.Context) (sentence.SentenceHistory, error) {
	data, err := s.store.GetAll(ctx, s.sentenceHistoryKey())
	if err != nil {
		return sentence.SentenceHistory{}, nil // 还没建过，视为空
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return sentence.SentenceHistory{}, nil
	}
	h, err := sentence.LoadSentenceHistory(data)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (s *Storage) DownloadSentenceWrong(ctx context.Context) (*sentence.SentenceWrong, error) {
	data, err := s.store.GetAll(ctx, s.sentenceWrongKey())
	if err != nil {
		return &sentence.SentenceWrong{}, nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &sentence.SentenceWrong{}, nil
	}
	return sentence.LoadSentenceWrong(data)
}

func (s *Storage) UploadSentenceHistory(ctx context.Context, h sentence.SentenceHistory) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sentenceHistoryKey(), []byte(toJSON(h)))
}

func (s *Storage) UploadSentenceWrong(ctx context.Context, w *sentence.SentenceWrong) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sentenceWrongKey(), []byte(toJSON(w)))
}
