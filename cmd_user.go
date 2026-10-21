package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// runUser 管理登录账号。owner 绑定的兜底手段 —— 万一第一个扫码的不是本人，
// 或者要把儿子从 pending 放进来，不用改代码、不用动 COS 控制台。
//
//	jrp --lang ja user list
//	jrp --lang ja user approve u_7f3a2b9c      批准（pending → member）
//	jrp --lang ja user set-role u_7f3a2b9c owner
//	jrp --lang ja user disable u_7f3a2b9c      踢掉（存量会话立刻失效）
//	jrp --lang ja user rename u_7f3a2b9c 老师
func runUser(fs *flag.FlagSet, lang string) {
	_ = fs.Parse(cmdArgs)
	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: 需要子命令：list | approve <uid> | set-role <uid> <role> | disable <uid> | rename <uid> <名字> | delete <uid>")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	users, err := storage.ListUsers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读用户表失败：%v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		rows := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			rows = append(rows, map[string]interface{}{
				"uid": u.UID, "role": u.Role, "name": u.Name,
				"last_login": u.LastLogin, "is_owner": u.UID == "",
			})
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "user", "language": lang,
			"count": len(users), "users": rows,
			"note": "还没人扫码登录过就是空的；第一个登录的人自动是 owner",
		})
		return
	case "approve", "set-role", "disable", "rename", "delete":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Error: %s 需要 <uid>\n", args[0])
			os.Exit(1)
		}
		uid := args[1]
		u, ok := findUserByUID(users, uid)
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: 没有这个用户：%s（先 user list 看看）\n", uid)
			os.Exit(1)
		}
		switch args[0] {
		case "approve":
			u.Role = roleMember
		case "disable":
			u.Role = roleDisabled
		case "set-role":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Error: set-role 需要 <role>：owner | member | pending | disabled")
				os.Exit(1)
			}
			r := strings.ToLower(args[2])
			if r != roleOwner && r != roleMember && r != rolePending && r != roleDisabled {
				fmt.Fprintf(os.Stderr, "Error: 不认识的角色 %q\n", args[2])
				os.Exit(1)
			}
			u.Role = r
		case "rename":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Error: rename 需要 <名字>")
				os.Exit(1)
			}
			u.Name = strings.Join(args[2:], " ")
		case "delete":
			// 只删账号，users/<uid>/ 下的学习数据保留 —— 误删还能把人加回来接着用。
			// ⚠️ owner（uid 为空）删不得：删了就没人能再批准任何人，只能上服务器改 COS。
			if u.UID == "" {
				fmt.Fprintln(os.Stderr, "Error: 不能删除站点所有者")
				os.Exit(1)
			}
			users = removeUser(users, uid)
			if err := storage.SaveUsers(ctx, users); err != nil {
				fmt.Fprintf(os.Stderr, "Error: 写回失败：%v\n", err)
				os.Exit(1)
			}
			outputResult(map[string]interface{}{
				"success": true, "command": "user", "language": lang,
				"uid": uid, "action": "delete",
				"next_step": "账号已删除，学习数据保留；他再用微信扫会重新变成待批准",
			})
			return
		}
		u.LastLogin = time.Now().Format("2006-01-02 15:04:05")
		for i := range users {
			if users[i].UID == u.UID {
				users[i] = u
			}
		}
		if err := storage.SaveUsers(ctx, users); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 写回失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "user", "language": lang,
			"uid": u.UID, "role": u.Role, "name": u.Name,
			"next_step": "角色变更对存量会话立即生效（每个请求都会重新查用户表）",
		})
		return
	}
	fmt.Fprintf(os.Stderr, "Error: 不认识的子命令 %q\n", args[0])
	os.Exit(1)
}
