package main

import (
	"testing"
)

// ---- 方案 A：密钥放 URL path ----

func TestRefinePathToken(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/listening/refine/abc123", "abc123"},
		{"/api/listening/refine/abc123/", "abc123"},          // 尾斜杠忽略
		{"/api/listening/refine/abc123/extra", "abc123"},     // 多段只取第一段
		{"/api/listening/refine/tok-with-dash_x", "tok-with-dash_x"},
		{"/api/listening/refine/", ""},                       // 空 token
		{"/api/listening/refine", ""},                        // 没到 marker
		{"/api/stats", ""},                                   // 完全不相关的路径
	}
	for _, c := range cases {
		if got := refinePathToken(c.path); got != c.want {
			t.Errorf("refinePathToken(%q) = %q，要 %q", c.path, got, c.want)
		}
	}
}

// ---- 方案 C：桶名 + key 前缀校验 ----

func TestCheckRefineTarget桶名不对(t *testing.T) {
	st := &Storage{lang: "ja"} // sharedBase = language-review/ja
	t.Setenv("TENCENT_COS_BUCKET", "mybucket-1251036673")

	d := ciJobsDetail{
		Input:     ciInput{Object: "language-review/ja/listening/N5/x.srt", BucketId: "other-999"},
		Operation: ciOperaton{Output: ciObject{Object: "language-review/ja/listening/N5/x.srt", Bucket: "other-999"}},
	}
	if err := checkRefineTarget(d, st); err == nil {
		t.Fatal("指向别的桶应被拒")
	}
}

func TestCheckRefineTarget桶名对但key越界(t *testing.T) {
	st := &Storage{lang: "ja"}
	t.Setenv("TENCENT_COS_BUCKET", "mybucket-1251036673")

	// 桶对，但 key 在 listening 目录外（比如拿去精校别人的档案）
	d := ciJobsDetail{
		Input:     ciInput{Object: "language-review/ja/archives/xxx.json", BucketId: "mybucket-1251036673"},
		Operation: ciOperaton{Output: ciObject{Object: "language-review/ja/archives/xxx.srt", Bucket: "mybucket-1251036673"}},
	}
	if err := checkRefineTarget(d, st); err == nil {
		t.Fatal("key 不在听力目录下应被拒")
	}
}

func TestCheckRefineTarget完全在听力目录下通过(t *testing.T) {
	st := &Storage{lang: "ja"}
	t.Setenv("TENCENT_COS_BUCKET", "mybucket-1251036673")

	d := ciJobsDetail{
		Input:     ciInput{Object: "language-review/ja/listening/N5/v.mp4", BucketId: "mybucket-1251036673"},
		Operation: ciOperaton{Output: ciObject{Object: "language-review/ja/listening/N5/v.srt", Bucket: "mybucket-1251036673"}},
	}
	if err := checkRefineTarget(d, st); err != nil {
		t.Fatalf("本桶+听力目录下应通过：%v", err)
	}
}

func TestCheckRefineTarget只给Output也认(t *testing.T) {
	st := &Storage{lang: "ja"}
	t.Setenv("TENCENT_COS_BUCKET", "mybucket-1251036673")

	// 有些回调可能只填 Output（srt key），Input 空——只要 Output 在听力目录下就放行。
	d := ciJobsDetail{
		Input:     ciInput{},
		Operation: ciOperaton{Output: ciObject{Object: "language-review/ja/listening/N4/v.srt", Bucket: "mybucket-1251036673"}},
	}
	if err := checkRefineTarget(d, st); err != nil {
		t.Fatalf("只给 Output 且在听力目录下应通过：%v", err)
	}
}

func TestCheckRefineTarget桶名前缀相似不误判(t *testing.T) {
	st := &Storage{lang: "ja"}
	t.Setenv("TENCENT_COS_BUCKET", "mybucket-1251036673")

	// 攻击者用一个前缀相同的桶名，应被拒（不是精确相等）。
	d := ciJobsDetail{
		Input:     ciInput{Object: "language-review/ja/listening/N5/x.srt", BucketId: "mybucket-1251036673-evil"},
		Operation: ciOperaton{Output: ciObject{Object: "language-review/ja/listening/N5/x.srt", Bucket: "mybucket-1251036673-evil"}},
	}
	if err := checkRefineTarget(d, st); err == nil {
		t.Fatal("前缀相似的桶名应被拒（不能 HasPrefix，要精确相等）")
	}
}
