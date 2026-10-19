package main

import (
	"context"
	"testing"
)

// 加账号体系时最怕的一件事：owner（老师）的 COS key 被改动，等于老数据一夜之间
// 「消失」。这组测试把「owner 的 key 与 2026-10 之前逐字节相同」钉死，
// 顺便把「普通成员走 users/<uid>/ 前缀、教材仍然共享」也钉死。
// 真要改 key 规则，这里必须先红 —— 那就意味着要写迁移脚本，不是随手改改。

func TestOwnerKeysAreUnchanged(t *testing.T) {
	s := &Storage{lang: "ja"} // uid 为空 = owner
	const p = "language-review/ja"

	cases := map[string]string{
		"archive prefix":  s.personalBase() + "/archives/",
		"history prefix":  s.personalBase() + "/history/",
		"daily plan":      s.planJSONKey("", "2026-10-20"),
		"hard plan":       s.planJSONKey("hard", "2026-10-20"),
		"sentence plan":   s.planJSONKey("sentences", "2026-10-20"),
		"draft":           draftKeyOf(s),
		"review snapshot": reviewKeyOf(s),
		"sentence hist":   s.sentenceHistoryKey(),
		"sentence wrong":  s.sentenceWrongKey(),
	}
	want := map[string]string{
		"archive prefix":  p + "/archives/",
		"history prefix":  p + "/history/",
		"daily plan":      p + "/plans/plan_2026-10-20.json",
		"hard plan":       p + "/plans/hard_2026-10-20.json",
		"sentence plan":   p + "/plans/sentences_2026-10-20.json",
		"draft":           p + "/drafts/draft_2026-10-20_words.json",
		"review snapshot": p + "/reviews/review_2026-10-20_words.json",
		"sentence hist":   p + "/plans/sentence_history.json",
		"sentence wrong":  p + "/plans/sentence_wrong.json",
	}
	for name, got := range cases {
		if got != want[name] {
			t.Errorf("%s: got %q, want %q（owner 的 key 变了 = 老数据读不到）", name, got, want[name])
		}
	}
}

func TestOwnerSharesTextbookKeys(t *testing.T) {
	s := &Storage{lang: "ja"}
	const p = "language-review/ja"
	cases := map[string]string{
		"sentence bank": s.sentenceBankKey(),
		"review bank":   s.reviewBankKey(),
		"word meta":     s.wordMetaKey(),
		"plan config":   s.planConfigKey(),
		"judge prompt":  s.judgePromptKey(),
		"knowledge":     s.sharedBase() + "/knowledge/x.md",
		"shared base":   s.sharedBase(),
		"personal base": s.personalBase(),
	}
	want := map[string]string{
		"sentence bank": p + "/plans/sentence_bank.json",
		"review bank":   p + "/plans/sentence_review.json",
		"word meta":     p + "/word_meta.json",
		"plan config":   p + "/configs/plan_config.json",
		"judge prompt":  p + "/configs/judge_prompt.txt",
		"knowledge":     p + "/knowledge/x.md",
		"shared base":   p,
		"personal base": p,
	}
	for name, got := range cases {
		if got != want[name] {
			t.Errorf("%s: got %q, want %q", name, got, want[name])
		}
	}
}

func TestForUserIsolatesPersonalKeepsTextbookShared(t *testing.T) {
	owner := &Storage{lang: "ja"}
	if !owner.IsOwner() {
		t.Fatalf("uid 为空应该是 owner")
	}
	member := owner.ForUser("u_7f3a2b9c")
	if member.IsOwner() || member.UserID() != "u_7f3a2b9c" {
		t.Fatalf("ForUser 没绑上 uid：%q owner=%v", member.UserID(), member.IsOwner())
	}
	if owner.UserID() != "" {
		t.Errorf("ForUser 把原对象也改了（不该有副作用）：%q", owner.UserID())
	}

	const mp = "language-review/ja/users/u_7f3a2b9c"
	personal := map[string]string{
		"daily plan":     member.planJSONKey("", "2026-10-20"),
		"draft":          draftKeyOf(member),
		"review":         reviewKeyOf(member),
		"sentence hist":  member.sentenceHistoryKey(),
		"sentence wrong": member.sentenceWrongKey(),
	}
	wantPersonal := map[string]string{
		"daily plan":     mp + "/plans/plan_2026-10-20.json",
		"draft":          mp + "/drafts/draft_2026-10-20_words.json",
		"review":         mp + "/reviews/review_2026-10-20_words.json",
		"sentence hist":  mp + "/plans/sentence_history.json",
		"sentence wrong": mp + "/plans/sentence_wrong.json",
	}
	for name, got := range personal {
		if got != wantPersonal[name] {
			t.Errorf("个人数据 %s: got %q, want %q", name, got, wantPersonal[name])
		}
	}

	// 教材和配置对所有人都是同一份 —— 按人复制等于改一句要改两遍。
	shared := map[string]string{
		"sentence bank": member.sentenceBankKey(),
		"review bank":   member.reviewBankKey(),
		"word meta":     member.wordMetaKey(),
		"plan config":   member.planConfigKey(),
		"judge prompt":  member.judgePromptKey(),
	}
	for name, got := range shared {
		if want := owner.sharedBase(); got == "" || got[:len(want)] != want {
			t.Errorf("教材 %s 不该带用户前缀：%q", name, got)
		}
		if member.personalBase() != mp {
			t.Errorf("personalBase got %q, want %q", member.personalBase(), mp)
		}
	}
}

// 这两个 key 函数未导出且带参数，测试里包一层避免重复拼日期。
func draftKeyOf(s *Storage) string  { return s.draftKey("2026-10-20", "words") }
func reviewKeyOf(s *Storage) string { return s.reviewKey("2026-10-20", "words") }

// TestFirstUserIsOwnerWithEmptyUID：第一个登录的人必须是 owner，且 uid 为空。
// ⚠️ 这是零迁移的命门：owner 一旦拿到 u_xxx，personalBase() 就会去读
// users/u_xxx/archives/（空目录），老师登录完会发现「档案全没了」（2026-10-19 线上踩到）。
func TestFirstUserIsOwnerWithEmptyUID(t *testing.T) {
	st := &Storage{dryRun: true}
	ctx := context.Background()

	first, err := upsertUser(ctx, st, "openid-first")
	if err != nil {
		t.Fatalf("第一个用户创建失败：%v", err)
	}
	if first.Role != roleOwner {
		t.Errorf("第一个用户应是 owner，实际 %q", first.Role)
	}
	if first.UID != "" {
		t.Fatalf("owner 的 uid 必须为空（否则读不到老档案），实际 %q", first.UID)
	}

	// owner 的 Storage 走的必须还是老路径
	if !st.ForUser(first.UID).IsOwner() {
		t.Errorf("owner 的 Storage IsOwner() 应为 true")
	}

}

// TestRoleForNewUser：第一个 owner，其余 pending（要 owner approve 才能用）。
// dryRun 的 Storage 写不进用户表，所以这条逻辑单独用纯函数测。
func TestRoleForNewUser(t *testing.T) {
	if got := roleForNewUser(nil); got != roleOwner {
		t.Errorf("空用户表应给 owner，实际 %q", got)
	}
	if got := roleForNewUser([]User{{UID: "u_1", Role: roleOwner}}); got != rolePending {
		t.Errorf("已有用户应给 pending，实际 %q", got)
	}
}
