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
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/listening"
	"github.com/zhangyf/objstore"
)

// listeningDir 是听力素材在 COS 里的目录（相对 sharedBase）。
const listeningDir = "/listening/"

// levelRe 认级别目录名：N5 / N4 / N3 / N2 / N1（也容许 n5 小写）。
var levelRe = regexp.MustCompile(`^N[1-5]$`)

// presignTTL 签名 URL 有效期。视频播放可能持续几十分钟，且用户可能暂停很久，
// 给足 3 小时；URL 只在响应里下发、不落盘，过期重拉一次接口即可。
const presignTTL = 3 * time.Hour

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
func (s *server) listeningIndex(w http.ResponseWriter, st *Storage) {
	ctx := s.ctx()
	prefix := st.sharedBase() + listeningDir
	objs, err := st.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: "/"})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "听力目录读不了：" + err.Error(),
		})
		return
	}

	// Delimiter="/" 时，子目录前缀落在 CommonPrefixes；objstore 把它折进
	// ObjectInfo（Key 以 / 结尾）。逐个级别再列一次拿视频清单。
	type levelOut struct {
		Level  string   `json:"level"`
		Videos []string `json:"videos"`
	}
	levels := make([]levelOut, 0, 8)
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, "/") {
			continue
		}
		lv := path.Base(strings.TrimSuffix(o.Key, "/"))
		if !levelRe.MatchString(lv) {
			continue
		}
		videos, err := s.listVideos(st, o.Key)
		if err != nil {
			continue
		}
		if len(videos) == 0 {
			continue
		}
		levels = append(levels, levelOut{Level: lv, Videos: videos})
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].Level < levels[j].Level })

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"levels":  levels,
	})
}

// listVideos 列某级别目录下的 mp4 文件名（不含路径）。
func (s *server) listVideos(st *Storage, prefix string) ([]string, error) {
	objs, err := st.store.ListObjects(s.ctx(), objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range objs {
		if !strings.HasSuffix(strings.ToLower(o.Key), ".mp4") {
			continue
		}
		out = append(out, path.Base(o.Key))
	}
	sort.Strings(out)
	return out, nil
}

// listeningVideo 返回单个视频的签名 URL + 解析后的 SRT 字幕。
//
// file 只接受裸文件名（不带路径、不含 ..），路径由服务端拼，避免目录穿越。
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

	// 签名 URL。
	videoURL, err := st.store.PresignGetObject(ctx, videoKey, presignTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "生成播放地址失败：" + err.Error(),
		})
		return
	}

	// SRT：同名换扩展名。缺失不算错（前端降级成纯播放、无字幕联动）。
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
		"expires":   int(presignTTL.Seconds()),
		"cues":      cues,
		"cue_count": len(cues),
		"note":      srtNote,
	})
}
