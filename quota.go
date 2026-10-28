package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// 按自然日的调用配额。
//
// 2026-10-28 安全评审 + 老师定的策略：
//
//	「pending 时大模型那里增加按量限制…… pending 时一天只能上传 2 张图片。」
//
// 背景：pending 用户有 7 个自然日的**完整**权限（老师定的产品策略），
// 而拍照取词每张图都真调视觉大模型 —— 那是要花钱的。不卡住等于给陌生人
// 7 天免费额度。体验版（trial）更简单：/api/vision 根本不在白名单里，走不到这儿。
//
// 只对 pending 生效，正式成员（老师、儿子、已批准的）不受影响。

// pendingVisionDaily pending 每天能识别的图片张数。
const pendingVisionDaily = 2

// quotaNameVision 配额名，会拼进对象 key。
const quotaNameVision = "vision"

type dayQuota struct {
	Date string `json:"date"`
	Used int    `json:"used"`
}

func (s *Storage) quotaKey(name, date string) string {
	return s.personalBase() + "/quota/" + name + "_" + date + ".json"
}

var (
	quotaMu sync.Mutex // 包住读-改-写，跟 usersMu 一个套路

	// quotaFallback 内存兜底：COS 写失败时至少在本进程内还数得住，
	// 别让一次抖动把「每天 2 张」变成「不限量」。st.store 为 nil（测试）时也走它。
	quotaFallback = struct {
		sync.Mutex
		m map[string]int
	}{m: map[string]int{}}
)

// takeQuota 扣 n 个额度，返回 (放行?, 已用数, 剩余数)。
//
// ⚠️ 判定在**扣之前**：used+n > limit 就不扣、不落盘 ——
// 否则被拒的请求也会把计数顶上去，老师批准之后那天的额度平白少了。
func takeQuota(ctx context.Context, st *Storage, name string, n, limit int) (bool, int, int) {
	if n <= 0 {
		return true, 0, limit
	}
	quotaMu.Lock()
	defer quotaMu.Unlock()

	date := time.Now().Format("2006-01-02")
	q := loadQuota(ctx, st, st.quotaKey(name, date), date)

	if q.Used+n > limit {
		left := limit - q.Used
		if left < 0 {
			left = 0
		}
		return false, q.Used, left
	}
	q.Used += n
	saveQuota(ctx, st, st.quotaKey(name, date), q)
	left := limit - q.Used
	if left < 0 {
		left = 0
	}
	return true, q.Used, left
}

func loadQuota(ctx context.Context, st *Storage, key, date string) dayQuota {
	if st.store != nil {
		if data, err := st.store.GetAll(ctx, key); err == nil && len(data) > 0 {
			var q dayQuota
			if json.Unmarshal(data, &q) == nil && q.Date == date {
				return q
			}
		}
	}
	quotaFallback.Lock()
	defer quotaFallback.Unlock()
	return dayQuota{Date: date, Used: quotaFallback.m[key]}
}

func saveQuota(ctx context.Context, st *Storage, key string, q dayQuota) {
	quotaFallback.Lock()
	quotaFallback.m[key] = q.Used
	quotaFallback.Unlock()
	if st.dryRun || st.store == nil {
		return
	}
	_ = st.store.PutObject(ctx, key, []byte(toJSON(q)))
}
