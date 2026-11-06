package main

// api_listening.go —— 听力模块：视频 + 同名 SRT 字幕，边播边看逐句高亮。
//
// 数据在 COS 的 language-review/<lang>/listening/<LEVEL>/ 下，每个视频配一个
// 同名 .srt（扩展名不同）。桶是私有的，视频不能给裸 URL —— 走 PresignGetObject
// 现签一个带时效的下载地址给 <video>；SRT 在服务端解析成结构化字幕数组下发。
//
// 两个动作：
//
//	GET /api/listening                     → 各级别 + 每级视频清单（不含 URL）
//	GET /api/listening?level=N5&file=x.mp4 → 单个视频的签名 URL + 解析后的字幕
//
// 挂 withUser（教材内容但按级别组织，先只给登录用户；体验版不在白名单，跟 /api/search 一致）。

import (
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/zhangyf/jrp/internal/listening"
	"github.com/zhangyf/objstore"
)

// listeningDir 是听力素材在 COS 里的目录（相对 sharedBase）。
const listeningDir = "/listening/"

// levelRe 认级别目录名：N5 / N4 / N3 / N2 / N1（也容许 n5 小写）。
var levelRe = regexp.MustCompile(`^N[1-5]$`)

// listeningCDNBase 听力文件的对外域名。EO 侧做了回源 URL 重写：前端请求
// https://listening.110105.xyz/{Nx}/{file}，EO 自动补上 COS 路径前缀
// （language-review/<lang>/listening/）回源到私有桶。这样对外不暴露桶域名
// 和真实 key 前缀，也不带 COS 签名 —— 从安全角度把存储细节藏在 EO 后面。
//
// 列表仍走默认桶（内部 SDK ListObjects），只有下发给前端的播放地址换这个域名。
// ⚠️ {Nx} 是等级（N5 这种，注意大小写），{file} 是最终文件名（不含路径前缀）。
const listeningCDNBase = "https://listening.110105.xyz/"

// handleListening 听力模块唯一入口。
func (s *server) handleListening(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET only",
		})
		return
	}
	level := strings.TrimSpace(r.URL.Query().Get("level"))
	file := strings.TrimSpace(r.URL.Query().Get("file"))
	if level == "" {
		s.listeningIndex(w, st)
		return
	}
	s.listeningVideo(w, r, st, level, file)
}

// listeningIndex 列出 listening/ 下所有级别和每级的视频清单。
//
// 目录结构 listening/<LEVEL>/<name>.mp4 + <name>.srt。这里只列 mp4，
// SRT 是否配对在详情接口里才校验（列表阶段不必为每个视频多打一次 HEAD）。
//
// ⚠️ 不用 Delimiter="/"：objstore 的 listWithDelimiter 解析了 CommonPrefixes
// 却不返回它们（只回 Contents），靠它列不出子目录。改成 Delimiter="" 一次
// 列全所有 mp4，按路径里 listening/ 之后的那一段（级别）自己分组。
func (s *server) listeningIndex(w http.ResponseWriter, st *Storage) {
	ctx := s.ctx()
	prefix := st.sharedBase() + listeningDir
	objs, err := st.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "听力目录读不了：" + err.Error(),
		})
		return
	}

	// prefix 之后的第一段 = 级别，第二段 = 文件名。
	// 例：language-review/ja/listening/N5/xxx.mp4，prefix 剥掉后是 "N5/xxx.mp4"。
	type levelOut struct {
		Level  string   `json:"level"`
		Videos []string `json:"videos"`
	}
	byLevel := map[string][]string{}
	order := []string{}
	for _, o := range objs {
		if !strings.HasSuffix(strings.ToLower(o.Key), ".mp4") {
			continue
		}
		rel := strings.TrimPrefix(o.Key, prefix)
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) != 2 {
			continue
		}
		lv, name := parts[0], parts[1]
		if !levelRe.MatchString(lv) || strings.Contains(name, "/") {
			continue
		}
		if _, ok := byLevel[lv]; !ok {
			order = append(order, lv)
		}
		byLevel[lv] = append(byLevel[lv], name)
	}
	sort.Strings(order)
	levels := make([]levelOut, 0, len(order))
	for _, lv := range order {
		videos := byLevel[lv]
		sort.Strings(videos)
		levels = append(levels, levelOut{Level: lv, Videos: videos})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"levels":  levels,
	})
}

// listeningVideo 返回单个视频的播放地址 + 解析后的 SRT 字幕。
//
// file 只接受裸文件名（不带路径、不含 ..），路径由服务端拼，避免目录穿越。
//
// ⚠️ 播放地址不再走 COS 签名 URL，改成 EO 域名裸路径：
// https://listening.110105.xyz/{Nx}/{file}。EO 侧回源重写会自动补 COS 路径前缀，
// 把桶域名和真实 key 藏在 EO 后面（安全考量）。列表仍走默认桶 SDK（内部）。
func (s *server) listeningVideo(w http.ResponseWriter, r *http.Request, st *Storage, level, file string) {
	if !levelRe.MatchString(level) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "level 必须是 N1..N5",
		})
		return
	}
	if file == "" || file != path.Base(file) || strings.Contains(file, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "file 必须是文件名（不含路径）",
		})
		return
	}
	if !strings.HasSuffix(strings.ToLower(file), ".mp4") {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "file 必须是 .mp4",
		})
		return
	}

	ctx := s.ctx()
	base := st.sharedBase() + listeningDir + level + "/"
	videoKey := base + file

	// 先确认视频在（HEAD 一次），顺便拿到大小给前端显示。
	info, err := st.store.HeadObject(ctx, videoKey)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false, "error": "找不到视频：" + file,
		})
		return
	}

	// 播放地址：EO 域名 + 等级 + 文件名。文件名含空格/日文，逐段 URL 编码
	// （等级和文件名分开编，斜杠是路径分隔符不能编）。
	videoURL := listeningCDNBase + url.PathEscape(level) + "/" + url.PathEscape(file)

	// SRT：同名换扩展名。缺失不算错（前端降级成纯播放、无字幕联动）。
	// 文本同理走 EO 域名（EO 回源重写会补前缀），前端按需拉取。
	srtKey := base + strings.TrimSuffix(file, path.Ext(file)) + ".srt"
	var cues []listening.Cue
	var srtNote string
	srtData, err := st.store.GetAll(ctx, srtKey)
	if err != nil {
		srtNote = "没有找到字幕文件，仅播放视频"
		cues = []listening.Cue{}
	} else {
		cues = listening.ParseSRT(string(srtData))
		if len(cues) == 0 {
			srtNote = "字幕文件解析为空"
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"level":     level,
		"file":      file,
		"title":     strings.TrimSuffix(file, path.Ext(file)),
		"size":      info.Size,
		"video_url": videoURL,
		"cues":      cues,
		"cue_count": len(cues),
		"note":      srtNote,
	})
}
