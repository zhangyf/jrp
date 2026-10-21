package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// 管理后台接口（/api/admin/*）。
//
// 权限只认两样：owner 的登录会话，或 --auth-token。
//
// ⚠️ 不认「过了 nginx Basic Auth 就算管理员」：Basic Auth 是 nginx 那道门，
// Go 自己看不见，等阶段 3 把 auth_basic 摘掉就没人拦了。owner 会话是 Go
// 能自己校验的东西，摘不摘口令都一样安全，也不用再管一套管理员密码。
//
// 管理操作一律用 owner 的 Storage：用户表在语言根之外的 _auth/ 下，
// 跟 uid 无关，不存在「谁的档案」这回事。

func (s *server) withAdmin(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, logged := s.sessionUser(r)
		tokenOK := s.token != "" &&
			subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1

		if !tokenOK {
			if !logged {
				writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
					"success": false, "error": "unauthorized", "login": "/login.html?next=/admin.html",
				})
				return
			}
			// 角色以用户表为准：会话里的 Role 是登录那一刻的快照，
			// 管理员自己被改了角色也要立刻生效。
			u, ok := findUserByUID(cachedUsers(r.Context(), s.storage), sess.UID)
			if !ok || u.Role != roleOwner {
				writeJSON(w, http.StatusForbidden, map[string]interface{}{
					"success": false, "error": "只有站点所有者能进管理后台",
				})
				return
			}
		}
		next(w, r, s.storage.ForUser(""))
	}
}

// adminUserRow 给前端的一行。openid 只给尾号 —— 那是微信身份，没必要整段摆到页面上。
type adminUserRow struct {
	UID        string `json:"uid"`
	Role       string `json:"role"`
	Name       string `json:"name"`
	OpenIDTail string `json:"openid_tail"`
	Created    string `json:"created"`
	LastLogin  string `json:"last_login"`
	IsOwner    bool   `json:"is_owner"`
}

// handleAdminUsers 用户列表。待审核的排最前，被冻结的沉底。
func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 GET"})
		return
	}
	users, err := st.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	rows := make([]adminUserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, adminUserRow{
			UID:        u.UID,
			Role:       u.Role,
			Name:       u.Name,
			OpenIDTail: openIDTail(u.OpenID),
			Created:    u.Created,
			LastLogin:  u.LastLogin,
			IsOwner:    u.UID == "",
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return adminRank(rows[i].Role) < adminRank(rows[j].Role)
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"count":   len(rows),
		"users":   rows,
		"roles": map[string]string{
			roleOwner:    "站点所有者",
			roleMember:   "成员",
			rolePending:  "待批准",
			roleDisabled: "已冻结",
		},
	})
}

// adminRank 排序权重：待批准 → 所有者 → 成员 → 已冻结。
func adminRank(role string) int {
	switch role {
	case rolePending:
		return 0
	case roleOwner:
		return 1
	case roleMember:
		return 2
	default:
		return 3
	}
}

func openIDTail(openid string) string {
	if len(openid) <= 4 {
		return openid
	}
	return "…" + openid[len(openid)-4:]
}

// handleAdminUser 改一个账号。
//
//	POST /api/admin/user  {uid, action, name?}
//	action: approve（批准/解冻）| disable（冻结）| rename（改名）| delete（删号）
//
// ⚠️ 冻结和删号都会立刻踢掉他的存量会话；删号只删账号，users/<uid>/ 下的学习数据保留。
func (s *server) handleAdminUser(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 POST"})
		return
	}
	var in struct {
		UID    string `json:"uid"`
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "请求体不是 JSON"})
		return
	}
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))

	usersMu.Lock()
	defer usersMu.Unlock()

	users, err := st.ListUsers(r.Context()) // 写前重读，不用缓存
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "读用户表失败：" + err.Error()})
		return
	}
	u, ok := findUserByUID(users, in.UID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "没有这个用户（刷新列表看看）"})
		return
	}

	if err := adminActionAllowed(u, in.Action); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	note := ""
	switch in.Action {
	case "approve":
		u.Role = roleMember
		note = "已批准，现在可以扫码登录使用"
	case "disable":
		u.Role = roleDisabled
		note = "已冻结，他的会话立刻失效，再扫码也进不来"
	case "rename":
		n := strings.TrimSpace(in.Name)
		if n == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "名字不能为空"})
			return
		}
		u.Name = n
		note = "已改名为「" + n + "」"
	case "delete":
		users = removeUser(users, in.UID)
		if err := st.SaveUsers(r.Context(), users); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "写回失败：" + err.Error()})
			return
		}
		invalidateUserCache()
		s.destroySessionsForUID(in.UID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "uid": in.UID, "action": "delete",
			"note": "账号已删除（学习数据保留）；他再用微信扫会重新变成待批准",
		})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "不认识的动作：" + in.Action})
		return
	}

	u.LastLogin = time.Now().Format("2006-01-02 15:04:05")
	for i := range users {
		if users[i].UID == u.UID {
			users[i] = u
		}
	}
	if err := st.SaveUsers(r.Context(), users); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "写回失败：" + err.Error()})
		return
	}
	invalidateUserCache()
	// 角色一变就让存量会话失效：不然冻结了还能用旧会话继续练。
	if in.Action == "disable" || in.Action == "approve" {
		s.destroySessionsForUID(u.UID)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "uid": u.UID, "action": in.Action, "role": u.Role, "name": u.Name, "note": note,
	})
}

// adminActionAllowed 判断这个动作能不能落在这个账号上。
//
// ⚠️ owner 不可冻结、不可删除：uid=="" 是零迁移的根（他的数据就在语言根下），
// 把他冻了老师自己进不去，删了则没人能再批准任何人 —— 只能上服务器改 COS 救。
func adminActionAllowed(u User, action string) error {
	if u.UID == "" && (action == "disable" || action == "delete") {
		return fmt.Errorf("不能冻结或删除站点所有者")
	}
	switch action {
	case "approve", "disable", "rename", "delete":
		return nil
	default:
		return fmt.Errorf("不认识的动作：%s", action)
	}
}

func removeUser(users []User, uid string) []User {
	out := make([]User, 0, len(users))
	for _, u := range users {
		if u.UID != uid {
			out = append(out, u)
		}
	}
	return out
}
