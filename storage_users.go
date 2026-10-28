package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// 用户表。放在语言前缀之外（language-review/_auth/）：一个账号应该能学日语、
// 英语、法语，不该被语言切开。
//
// 存成单个 JSON 而不是每人一个对象：照抄 word_meta.json / sentence_bank.json
// 的范式，用户数就几个人、读写频率极低（只在登录时读、只在新建用户时写）。
// 真到几十人再切成 _auth/users/<uid>.json，改一个文件的事。

const usersCosKey = "_auth/users.json"

// User 一个登录身份。
// Role：
//   - owner   站点所有者（老师）。COS 上个人数据直接落在语言根下，老数据的位置。
//   - member  普通成员（比如儿子），个人数据在 users/<uid>/ 下。
//   - pending 已扫码但未批准：**有 7 个自然日的完整体验期**（跟 member 一样的权限，
//     只是数据在 users/<uid>/ 下）。到期还没被批准就一律 403，下次登录照样挡
//     —— 见 pendingExpired。
//   - disabled 被踢掉的。
type User struct {
	UID       string `json:"uid"`
	OpenID    string `json:"openid"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	Created   string `json:"created"`
	LastLogin string `json:"last_login"`
}

const (
	roleOwner    = "owner"
	roleMember   = "member"
	rolePending  = "pending"
	roleDisabled = "disabled"
)

type userTable struct {
	Version int    `json:"version"`
	Users   []User `json:"users"`
}

// authBase 用户表所在的根目录（语言无关）。
func (s *Storage) authBase() string {
	p := s.cosPrefix()
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i] + "/_auth"
	}
	return "_auth"
}

func (s *Storage) usersKey() string { return s.authBase() + "/users.json" }

// ListUsers 读用户表。COS 读不到（还没人登过）返回空表，不算错误。
func (s *Storage) ListUsers(ctx context.Context) ([]User, error) {
	// store 为 nil 只在测试里出现（dryRun 也要能读真实用户表，只是不写）。
	if s.store == nil {
		return nil, nil
	}
	data, err := s.store.GetAll(ctx, s.usersKey())
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	var t userTable
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return t.Users, nil
}

// SaveUsers 整份写回。写之前重新读一遍 COS（不用缓存）以降低并发丢写：
// 调用方负责持锁，这里的重读是为了避免把别人刚加的用户盖掉。
func (s *Storage) SaveUsers(ctx context.Context, users []User) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.usersKey(), []byte(toJSON(userTable{Version: 1, Users: users})))
}

// ---------------------------------------------------------------------------
// 60 秒缓存：照抄 plan_config.go / api_judge.go 的写法。
// 登录链路每次请求都要判角色，不能每次都打 COS。
// ---------------------------------------------------------------------------

const usersTTL = 60 * time.Second

var userCache = struct {
	sync.Mutex
	at     time.Time
	loaded bool
	users  []User
	// lastErr 记着最近一次读有没有出错。
	//
	// ⚠️ 2026-10-28 安全评审：以前读失败就「当没人登过」，于是 authRequired()
	// 返回 false，匿名请求被当成 owner —— COS 抖一下陌生人就能往老师档案里写。
	// 现在读失败要能被外面看见，好在鉴权那道 fail-closed（见 authRequired）。
	lastErr bool
}{}

func cachedUsers(ctx context.Context, st *Storage) []User {
	userCache.Lock()
	defer userCache.Unlock()
	if userCache.loaded && time.Since(userCache.at) < usersTTL {
		return userCache.users
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		// 读失败就用上一份（没有就当没人登过），绝不因为 COS 抖动把所有人踢下线。
		userCache.lastErr = true
		return userCache.users
	}
	userCache.users = users
	userCache.loaded = true
	userCache.lastErr = false
	userCache.at = time.Now()
	return users
}

// usersUnknown 报告「用户表一次都没读成功过」—— 这时不能假定没人，
// 鉴权必须 fail-closed（匿名一律 401），否则匿名会被当成 owner。
func usersUnknown() bool {
	userCache.Lock()
	defer userCache.Unlock()
	return !userCache.loaded
}

func invalidateUserCache() {
	userCache.Lock()
	userCache.loaded = false
	userCache.at = time.Time{}
	userCache.Unlock()
}

func findUserByUID(users []User, uid string) (User, bool) {
	for _, u := range users {
		if u.UID == uid {
			return u, true
		}
	}
	return User{}, false
}

func findUserByOpenID(users []User, openid string) (User, bool) {
	for _, u := range users {
		if u.OpenID == openid {
			return u, true
		}
	}
	return User{}, false
}

// upsertUser 按 openid 找人，找到就更新登录时间，没找到就新建。
// 第一个登录的人自动成为 owner（阶段 2 时 nginx Basic Auth 还开着，
// 外网根本到不了登录页，所以这个「第一个」必然是老师本人）。
func upsertUser(ctx context.Context, st *Storage, openid string) (User, error) {
	usersMu.Lock()
	defer usersMu.Unlock()

	users, _ := st.ListUsers(ctx) // 写前重读，不用缓存
	if u, ok := findUserByOpenID(users, openid); ok {
		u.LastLogin = time.Now().Format("2006-01-02 15:04:05")
		for i := range users {
			if users[i].UID == u.UID {
				users[i] = u
			}
		}
		if err := st.SaveUsers(ctx, users); err != nil {
			return u, err
		}
		invalidateUserCache()
		return u, nil
	}

	role := roleForNewUser(users)
	u := User{
		UID:       newUID(),
		OpenID:    openid,
		Role:      role,
		Name:      "",
		Created:   time.Now().Format("2006-01-02 15:04:05"),
		LastLogin: time.Now().Format("2006-01-02 15:04:05"),
	}
	// ⚠️ owner 的 uid 恒为空。personalBase() 靠 uid=="" 走老路径（零迁移），
	// 给 owner 也发 u_xxx 的话，老师一登录就读 users/u_xxx/archives/（空目录），
	// 表现就是「档案全没了」—— 数据其实还在老位置，只是读错了地方。
	if role == roleOwner {
		u.UID = ""
	}
	users = append(users, u)
	if err := st.SaveUsers(ctx, users); err != nil {
		return User{}, err
	}
	invalidateUserCache()
	return u, nil
}

// ---------- pending 的 7 天体验期 ----------
//
// 老师 2026-10-28 定的：新用户扫码进来是 pending，但**不能一上来就挡**——
// 给 7 个自然日的完整体验期（该练练、该录录），到期还没被批准再拦。
// 这样管理员（老师）有缓冲时间，陌生人也不能无限期占用。

const pendingGraceDays = 7

// parseUserTime 用户表里的时间串。老数据格式不统一，两种都认。
func parseUserTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// pendingDaysLeft 还剩几个自然日（按日期算，不看时分秒）。<=0 就是过期。
// ⚠️ 老数据没有 created → 按满格给，宁可多给几天也别误伤已登录的人。
func pendingDaysLeft(u User) int {
	t, ok := parseUserTime(u.Created)
	if !ok {
		return pendingGraceDays
	}
	d0 := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	now := time.Now()
	d1 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return pendingGraceDays - int(d1.Sub(d0).Hours()/24)
}

func pendingExpired(u User) bool { return pendingDaysLeft(u) <= 0 }

// roleForNewUser 决定新用户进来时给什么角色：一个都没有 → owner，其余 → pending
// （pending 要 owner 用 `jrp user approve` 放行，防止谁扫一下就能读数据）。
func roleForNewUser(users []User) string {
	if len(users) == 0 {
		return roleOwner
	}
	return rolePending
}

// usersMu 包住用户表的读-改-写。COS 的 PutObject 没有 If-Match，做不了乐观锁，
// 只能靠进程内这把锁 + 写前重读（用户数个位数，新用户注册极低频，够用）。
var usersMu sync.Mutex

// newUID 8 字节 hex。短到能塞进 cookie 和日志，又足够不可能撞。
func newUID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return "u_" + hex.EncodeToString(b)
}
