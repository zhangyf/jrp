package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReviewSnapshot 是「某天已经练完、已回写的那批词」的只读快照。
//
// 为什么需要它：档案里每个词只留累计值（ReviewCount / ErrorCount / LastReview），
// **不记录当次是答对还是答错**。所以回写一完成，老师当天写了什么、哪题错了
// 就再也取不回来了 —— 页面也因为到期日被推到未来而显示「今天没有到期的词」。
//
// 跟 Draft 的区别：
//   - Draft  = 没写完的，写完回写成功就删掉
//   - Review = 已写完的，留着给老师回看，当天一直在
type ReviewSnapshot struct {
	Date    string       `json:"date"`
	Mode    string       `json:"mode"` // words | hard
	SavedAt string       `json:"saved_at"`
	Items   []ReviewItem `json:"items"`
}

type ReviewItem struct {
	Number     int    `json:"number"`
	Word       string `json:"word"`
	Definition string `json:"definition"`
	Group      string `json:"group,omitempty"`
	Status     string `json:"status,omitempty"` // 当时的掌握状态，如 🔴待巩固
	Answer     string `json:"answer"`           // 老师写的答案，错题回看主要看这个
	Correct    bool   `json:"correct"`
	Unknown    bool   `json:"unknown"` // 主动点了「不会」
	Manual     bool   `json:"manual"`  // 老师勾了「算对」
	Blank      bool   `json:"blank"`   // 空着没写、也没标「不会」—— 没进档案，但要能看出来
	// 词性 / 备注不存快照：随时可能补录，存了就是过期数据。
	// /api/review 返回时按词形从 word_meta.json join（enrichReviewMeta）。
	Pos  string `json:"pos,omitempty"`
	Sub  string `json:"sub,omitempty"`
	Note string `json:"note,omitempty"`
}

// reviewKey 快照的 COS 键：reviews/review_<date>_<mode>.json
func (s *Storage) reviewKey(date, mode string) string {
	return fmt.Sprintf("%s/reviews/review_%s_%s.json", s.cosPrefix(), date, mode)
}

// UploadReview 合并写入快照。
//
// 合并而不是覆盖：老师可能先练今日练习、再练钉子户，也可能同一批词分两次回写
// （比如写完一半先回写，剩下的写完再回写一次）。按 Number 去重，后写的覆盖先写的。
func (s *Storage) UploadReview(ctx context.Context, snap *ReviewSnapshot) error {
	if s.dryRun || snap == nil || len(snap.Items) == 0 {
		return nil
	}
	if snap.SavedAt == "" {
		snap.SavedAt = time.Now().Format("2006-01-02 15:04:05")
	}

	merged := mergeReview(snap.Items, func() []ReviewItem {
		old, err := s.DownloadReview(ctx, snap.Date, snap.Mode)
		if err != nil || old == nil {
			return nil
		}
		return old.Items
	}())

	snap.Items = merged
	return s.store.PutObject(ctx, s.reviewKey(snap.Date, snap.Mode), []byte(toJSON(snap)))
}

// mergeReview 按 Word 合并，newer 覆盖 old，并保持 old 的相对顺序、
// 把 old 里没有的新词追加在后面。
//
// 为什么按 Word 而不是按 Number：同一天会出好几批 plan（回写成功 → plan 清 →
// 新一批），**每批的题号都从 1 重新编**。按号码合并的话，后一批的 #1 会把前一批
// 的 #1 盖掉 —— 前几批练过的词就这么没了。2026-10-05 当天档案里 83 个词的
// LastReview 是当天，快照里却只剩 44 个词：老师看到的「共 75 / 对 62」是虚的。
// 按词形合并，一天练过哪些词就都能留下来。
//
// 另一个例外：newer 是空条目（blank，没练也没标「不会」）而 old 已经有作答内容时，
// 保留 old。老师中途刷新页面后客户端状态清零，再提交会把整批都发上来，
// 里面早前批次已答的词全变成 blank —— 直接覆盖会把当天已回写的错词抹掉
// （同日事故：当天 10 个错词只剩 7 个）。空条目没有信息量，不该覆盖。
func mergeReview(newer, old []ReviewItem) []ReviewItem {
	if len(old) == 0 {
		return newer
	}
	byWord := make(map[string]ReviewItem, len(newer))
	order := make([]string, 0, len(newer))
	for _, it := range newer {
		if _, seen := byWord[it.Word]; !seen {
			order = append(order, it.Word)
		}
		byWord[it.Word] = it
	}

	out := make([]ReviewItem, 0, len(old)+len(newer))
	for _, it := range old {
		n, ok := byWord[it.Word]
		if !ok {
			out = append(out, it)
			continue
		}
		delete(byWord, it.Word)
		if n.Blank && !it.Blank {
			out = append(out, it) // 空条目不覆盖已答条目
			continue
		}
		out = append(out, n)
	}
	// 剩下的是这一批新出现的词，按它们在本批里的顺序追加
	for _, w := range order {
		if it, ok := byWord[w]; ok {
			out = append(out, it)
		}
	}

	// 按词去重：old 里可能带着历史遗留的重复条目（号码撞车时同一词被写了两遍），
	// 一条词只留一条，保留后来那条（信息更新），位置按首次出现算。
	uniq := make([]ReviewItem, 0, len(out))
	at := make(map[string]int, len(out))
	for _, it := range out {
		if j, ok := at[it.Word]; ok {
			// 后来那条优先，但空条目不能盖掉已答的（同一条规矩，换了个地方）
			if !it.Blank || uniq[j].Blank {
				uniq[j] = it
			}
			continue
		}
		at[it.Word] = len(uniq)
		uniq = append(uniq, it)
	}

	// 号码重排成 1..N 连续：跨批次的号码必然撞车（两批都有 #1），留着会让
	// 前端按号码索引错位（列表模式的「再练这 N 个错词」就是按号码匹配的）。
	for i := range uniq {
		uniq[i].Number = i + 1
	}
	return uniq
}

// DownloadReview 读快照。不存在 / 解析失败 / 日期对不上 → (nil, nil)，
// 跟草稿一样：回看是锦上添花，读不到就当没练过，不能拖垮整页。
func (s *Storage) DownloadReview(ctx context.Context, date, mode string) (*ReviewSnapshot, error) {
	data, err := s.store.GetAll(ctx, s.reviewKey(date, mode))
	if err != nil {
		return nil, nil
	}
	var snap ReviewSnapshot
	if err := jsonUnmarshal(data, &snap); err != nil {
		return nil, nil
	}
	if snap.Date != "" && snap.Date != date {
		return nil, nil
	}
	return &snap, nil
}

// DeleteReview 清快照。对象不存在（404）也当成功。
func (s *Storage) DeleteReview(ctx context.Context, date, mode string) error {
	if s.dryRun {
		return nil
	}
	_ = s.store.DeleteObject(ctx, s.reviewKey(date, mode))
	return nil
}

// validReviewMode 白名单。模式直接进 COS 键，不放宽会被任意字符串打散。
func validReviewMode(m string) bool {
	switch strings.ToLower(m) {
	case draftModeWords, draftModeHard:
		return true
	}
	return false
}
