// api_listening.go —— 听力域接口（视频字幕 / 播放时长 / 字幕精校回调）
//
// 腿一 R2 补做（2026-11-20）：听力模块是 R2 之后新增的，当时按子功能平铺成 3 个文件，
// 没跟上「一个域一个文件」的规矩。本次收拢成 1 个域文件。函数体逐字保留，仅对 import
// 取并集去重；serve.go 三条路由、HTTP 响应、COS 存档字节均不变。
// 每个分区以「原文件名」标注；查找 handler 先看下面的来源清单。
//
// 来源与顶层声明：
// api_listening.go: listeningDir, levelRe, listeningCDNBase, handleListening, listeningIndex, listeningVideo
// api_listening_progress.go: listeningProgressFile, maxReportSeconds, levelKeyRe, listeningProgress, listeningProgressKey, loadListeningProgress, handleListeningProgress, listeningTotals, sortedListeningLevels
// api_listening_refine.go: ciCallback, ciJobsDetail, ciInput, ciObject, ciOperaton, refineToken, refinePathToken, refineModel, handleListeningRefine, runRefine, splitMode, refineInFlight, refineTryLock, refineUnlock, refineCues, checkRefineTarget, extOf, refinedMarkerKey, refinedMarkerExists, markRefined
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/listening"
	"github.com/zhangyf/jrp/internal/llm"
	"github.com/zhangyf/jrp/internal/stats"
	"github.com/zhangyf/objstore"
)

// =========================================================================
// 原 api_listening.go —— 听力视频 + 同名 SRT 字幕
// =========================================================================

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

// =========================================================================
// 原 api_listening_progress.go —— 听力实际播放时长上报与统计
// =========================================================================

// api_listening_progress.go —— 听力实际播放时长的上报与统计。
//
// 老师要的：统计「实际听的时长」（小程序 + web 两端），在统计页的 JLPT
// N4/N5 等级那里体现练习进度。口径（2026-11-05 老师拍板）：
//
//	① 只算**正在播放**的秒数 —— 暂停、拖动进度条跳过的时间不计。
//	   前端在 timeupdate / onTimeUpdate 里累加相邻两次的差值，
//	   seek 造成的大跳变自己排除，攒够一小段或退出页面时上报一次。
//	② 不设目标，只显示累计时长。
//
// 存储：个人档案下 listening_progress.json（跟 quota 一样落在 users/<uid>/
// 下，owner 落裸前缀）。按等级累计，同时留一份按自然日的明细，将来想画
// 「每天听了多久」的趋势不用回头补数据。
//
//	{ "total": {"N5": 1234, "N4": 567}, "by_day": {"2026-11-05": {"N5": 300}} }
//
// 单位一律是**秒**（float），前端换算成分钟显示。上报单次封顶 120 秒，
// 防止前端 bug（比如差值没排除 seek）一次灌进几千秒。

// listeningProgressFile 个人听力时长档案的文件名（相对 personalBase）。
const listeningProgressFile = "/listening_progress.json"

// maxReportSeconds 单次上报封顶（秒）。正常前端每 15 秒左右上报一次，
// 120 秒已经很宽松；超过说明差值没排除 seek，直接截断。
const maxReportSeconds = 120

// levelKeyRe 统计档里等级名的校验，跟 api_listening.go 的 levelRe 一致。
var levelKeyRe = regexp.MustCompile(`^N[1-5]$`)

// listeningProgress 听力时长档案。
type listeningProgress struct {
	Total map[string]float64            `json:"total"`  // 等级 -> 累计秒
	ByDay map[string]map[string]float64 `json:"by_day"` // 日期 -> 等级 -> 当日秒
}

// listeningProgressKey 个人档案 key。
func (s *Storage) listeningProgressKey() string {
	return s.personalBase() + listeningProgressFile
}

// loadListeningProgress 读档案；没有或坏档返回空结构（不报错，第一听就是空）。
func loadListeningProgress(ctx context.Context, st *Storage) *listeningProgress {
	p := &listeningProgress{Total: map[string]float64{}, ByDay: map[string]map[string]float64{}}
	if st.store == nil {
		return p
	}
	data, err := st.store.GetAll(ctx, st.listeningProgressKey())
	if err != nil || len(data) == 0 {
		return p
	}
	var got listeningProgress
	if json.Unmarshal(data, &got) != nil {
		return p
	}
	if got.Total != nil {
		p.Total = got.Total
	}
	if got.ByDay != nil {
		p.ByDay = got.ByDay
	}
	return p
}

// handleListeningProgress 上报听力时长。POST {level, seconds}。
//
// 挂 withUser（跟 /api/record 一样，改个人档案的接口不匿名）。体验版不在
// 白名单里，走不到这儿 —— trial 用户点了视频就先弹登录。
func (s *server) handleListeningProgress(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}
	if st.uid == trialUID {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}

	var body struct {
		Level   string  `json:"level"`
		Seconds float64 `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "bad body: " + err.Error(),
		})
		return
	}
	level := strings.ToUpper(strings.TrimSpace(body.Level))
	if !levelKeyRe.MatchString(level) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "level 必须是 N1..N5",
		})
		return
	}
	// 负数（时钟回拨 / 前端 bug）当 0；超上限截断。
	secs := body.Seconds
	if secs < 0 {
		secs = 0
	}
	if secs > maxReportSeconds {
		secs = maxReportSeconds
	}
	if secs == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "added": 0})
		return
	}

	if st.dryRun {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "added": secs, "dry_run": true,
		})
		return
	}

	ctx := s.ctx()
	p := loadListeningProgress(ctx, st)
	today := time.Now().Format("2006-01-02")
	p.Total[level] += secs
	if p.ByDay[today] == nil {
		p.ByDay[today] = map[string]float64{}
	}
	p.ByDay[today][level] += secs

	if err := st.store.PutObject(ctx, st.listeningProgressKey(), []byte(toJSON(p))); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "写听力档案失败：" + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "added": secs, "total": p.Total[level],
	})
}

// listeningTotals 给统计页用的：各等级累计秒数。
// 读不到（没听过）返回空 map，前端按「没有听力数据」处理。
func listeningTotals(ctx context.Context, st *Storage) map[string]float64 {
	if st.uid == trialUID {
		return map[string]float64{}
	}
	return loadListeningProgress(ctx, st).Total
}

// sortedListeningLevels 产出统计页要画的听力进度行。
//
// ⚠️ 固定产出 N5、N4 两行（有数据用数据，没数据用 0），不再按「有没有听过」过滤
// —— 词汇/语法的进度条哪怕 0% 也常驻显示，听力藏起来会很突兀（老师一看就问
// 「听力时长呢」）。等级顺序固定 N5 在前 N4 在后，跟 JLPT 卡里其它条一致。
//
// goal 是该等级的满格目标（分钟），后端从 COS 配置注入；配置异常回退内置默认，
// 避免除零。前端拿 Seconds/60 ÷ GoalMinutes 直接画进度条。
func sortedListeningLevels(total map[string]float64, goal listening.Goal) []stats.ListeningLevel {
	if !listening.ValidGoal(goal) {
		goal = listening.BuiltinGoal()
	}
	// 固定顺序 N5、N4；只展示这两个等级（N1-N3 暂无目标口径，不画）。
	order := []string{"N5", "N4"}
	goalOf := map[string]int{"N5": goal.N5, "N4": goal.N4}
	out := make([]stats.ListeningLevel, 0, len(order))
	for _, lv := range order {
		out = append(out, stats.ListeningLevel{
			Level:       lv,
			Seconds:     total[lv], // 没听过就是 0
			GoalMinutes: goalOf[lv],
		})
	}
	return out
}

// =========================================================================
// 原 api_listening_refine.go —— COS 工作流回调触发字幕精校
// =========================================================================

// api_listening_refine.go —— 字幕精校回调（COS 数据万象工作流完成后打过来）。
//
// 老师在 COS 桶里搭了工作流：上传视频 → 自动转封装 + ASR 生成 SRT → 回调本接口。
// 服务端从 COS 拉同名 .srt，调大模型**保守精校**（只改确定的错，时间轴不动），
// 直接覆盖回 COS。方案 A：纯文本、不给视频（2026-11-06 与老师对齐）。
//
// ⛔ 这个接口**不走 withUser**：COS 工作流没有用户会话，走 withUser 会被 401 挡死。
//    它能写 COS，等于后门，所以鉴权用**独立密钥** JRP_REFINE_TOKEN：
//    回调请求必须带 Authorization: Bearer <JRP_REFINE_TOKEN>，否则 401。
//    密钥只存在 /etc/jrp/jrp.env（600），跟大模型密钥同一份文件。
//
// 回调 body 是 COS 数据万象的 XML（跟提交任务接口的 Response 同结构）：
//
//	<Response>
//	  <JobsDetail>
//	    <State>Success</State>
//	    <Input><Object>language-review/ja/listening/N5/xxx.mp4</Object></Input>
//	    <Operation><Output><Object>.../xxx.srt</Object></Output></Operation>
//	  </JobsDetail>
//	</Response>
//
// 老师确认（2026-11-06）：Output.Object 就是 **.srt 的完整 key**。所以这里优先用
// Output.Object 当 srt key；拿不到（比如只回传了 Input）就按 Input.Object 的视频 key
// 换扩展名推。

// ciCallback 是 COS 数据万象回调 body 里我们关心的那几个节点。
// 只解析需要的字段，其余忽略（encoding/xml 按字段名匹配，没列的自动跳过）。
type ciCallback struct {
	XMLName xml.Name       `xml:"Response"`
	Detail  []ciJobsDetail `xml:"JobsDetail"`
}

type ciJobsDetail struct {
	State     string     `xml:"State"`
	Code      string     `xml:"Code"`
	Message   string     `xml:"Message"`
	Input     ciInput    `xml:"Input"`
	Operation ciOperaton `xml:"Operation"`
}

// ciInput 回调里的输入对象：Object 是源文件 key，BucketId 是源桶（可能带 APPID）。
type ciInput struct {
	Object   string `xml:"Object"`
	BucketId string `xml:"BucketId"`
	Region   string `xml:"Region"`
}

// ciObject 通用输出对象：Object 是结果文件 key，Bucket 是结果桶。
type ciObject struct {
	Object string `xml:"Object"`
	Bucket string `xml:"Bucket"`
	Region string `xml:"Region"`
}

type ciOperaton struct {
	Output ciObject `xml:"Output"`
}

// refineToken 从环境读回调密钥。没配就是「精校功能未启用」。
func refineToken() string {
	return strings.TrimSpace(os.Getenv("JRP_REFINE_TOKEN"))
}

// refinePathToken 从回调 URL path 里剥出密钥段。
// 路由注册的是 /api/listening/refine/，path 形如 /api/listening/refine/<token>。
// 取「refine/ 之后」的那一段；多段时只取第一段（token 本身不含斜杠）。
func refinePathToken(path string) string {
	const marker = "/api/listening/refine/"
	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}
	rest := path[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// refineModel 返回精校用的模型链。默认走现网文本链（跟判句同一个）。
//
// 覆盖优先级：环境变量 > COS 配置（configs/refine_config.json）> 现网默认。
// 环境变量留给运维应急（改完 restart 即生效），日常调参走 COS 配置不用重启。
func refineModel(rc listening.RefineConfig) llm.Config {
	cfg, ok := llm.LoadConfig()
	if !ok {
		return llm.Config{}
	}
	// 先套 COS 配置
	if m := strings.TrimSpace(rc.Model); m != "" {
		cfg.Model = m
	}
	if fb := strings.TrimSpace(rc.Fallback); fb != "" {
		cfg.Fallback = llm.ParseFallbackModels(fb, cfg.Model)
	}
	// 再让环境变量兜底覆盖
	if m := strings.TrimSpace(os.Getenv("JRP_REFINE_MODEL")); m != "" {
		cfg.Model = m
	}
	if fb := strings.TrimSpace(os.Getenv("JRP_REFINE_FALLBACK")); fb != "" {
		cfg.Fallback = llm.ParseFallbackModels(fb, cfg.Model)
	}
	// 超时：默认取配置（内置 150s），环境变量可再覆盖。
	timeout := rc.TimeoutSeconds
	if timeout <= 0 {
		timeout = listening.DefaultTimeoutSeconds
	}
	if v := strings.TrimSpace(os.Getenv("JRP_REFINE_TIMEOUT")); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= 600 {
			timeout = n
		}
	}
	cfg.Timeout = time.Duration(timeout) * time.Second
	return cfg
}

// handleListeningRefine —— COS 工作流回调入口。
//
//	POST /api/listening/refine   （XML body，Bearer JRP_REFINE_TOKEN）
//	→ 200 { success:true, changed:N, cues:N, model:"..." }
//	→ 200 { success:true, skipped:"already refined" }   幂等：已精校过直接跳过
//	→ 401 密钥不对 / 400 body 或 key 不对 / 503 没配模型
func (s *server) handleListeningRefine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	// —— 鉴权：密钥放 URL path（数据万象回调加不了自定义 header，见文件头注释）——
	//     回调地址配成 /api/listening/refine/<JRP_REFINE_TOKEN>，这里把 token 剥出来比对。
	//     常量时间比较防时序；没配密钥 = 功能未启用，直接 403。
	want := refineToken()
	got := refinePathToken(r.URL.Path)
	if want == "" {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false,
			"error":   "服务端没配 JRP_REFINE_TOKEN，字幕精校未启用",
		})
		return
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false, "error": "unauthorized",
		})
		return
	}

	// —— 解析 XML 回调 body ——
	var cb ciCallback
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "读 body 失败：" + err.Error(),
		})
		return
	}
	if err := xml.Unmarshal(body, &cb); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "回调不是合法 XML：" + err.Error(),
		})
		return
	}
	if len(cb.Detail) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "回调里没有 JobsDetail",
		})
		return
	}
	d := cb.Detail[0]

	// 只处理成功的任务；失败/进行中的回调直接确认收到、不做事。
	if !strings.EqualFold(d.State, "Success") {
		log.Printf("refine: 任务未成功 State=%s Code=%s Msg=%s，跳过", d.State, d.Code, d.Message)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "skipped": "job not successful: " + d.State,
		})
		return
	}

	// —— 方案 C：校验桶名 + key 前缀。URL 密钥挡陌生人，这道挡「拿到的 URL
	//     去精校别的桶 / 别的目录」——回调 body 是明文 XML 谁都能构造，所以
	//     只认本桶、且 key 必须落在听力目录下，越界的直接拒。
	if err := checkRefineTarget(d, s.storage); err != nil {
		log.Printf("refine: 拒绝越界回调：%v（Input=%q Output=%q）",
			err, d.Input.Object, d.Operation.Output.Object)
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	// —— 定位 srt key：优先 Output.Object（老师确认是完整 srt key），
	//     兜底按 Input.Object 的视频 key 换扩展名 ——
	srtKey := strings.TrimSpace(d.Operation.Output.Object)
	if srtKey == "" {
		videoKey := strings.TrimSpace(d.Input.Object)
		if videoKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false, "error": "回调里既没有 Output.Object 也没有 Input.Object",
			})
			return
		}
		srtKey = strings.TrimSuffix(videoKey, extOf(videoKey)) + ".srt"
	}
	if !strings.HasSuffix(strings.ToLower(srtKey), ".srt") {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "srt key 必须以 .srt 结尾：" + srtKey,
		})
		return
	}

	st := s.storage
	ctx := s.ctx()

	// —— 幂等：已精校过的直接跳过（工作流可能重试回调，别重复烧模型钱）——
	if refinedMarkerExists(ctx, st, srtKey) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "skipped": "already refined",
		})
		return
	}

	// —— 同一份字幕同时只跑一份精校 ——
	//     数据万象回调可能并发重试，两个进程同时读写同一个 srt 会互相覆盖。
	if !refineTryLock(srtKey) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "skipped": "already in progress",
		})
		return
	}

	// —— 立刻 200，精校放后台 ——
	//     ⛔ 不能同步等：整段字幕要调十几次模型，动辄十几分钟，nginx proxy_read_timeout
	//     只有 300s，必然 504；而且客户端一断开 request ctx 就被取消，模型调用跟着死。
	//     回调方（数据万象）只关心「收到没」，返回 accepted 即可，真正的结果落 COS +
	//     .refined 标记里。后台用独立 context，不继承请求生命周期。
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "accepted": true, "srt": srtKey,
	})
	go func() {
		defer refineUnlock(srtKey)
		s.runRefine(srtKey)
	}()
}

// runRefine 后台精校主流程。独立 context（不跟请求生命周期），整体上限 40 分钟。
func (s *server) runRefine(srtKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	st := s.storage

	// 再查一次幂等标记：拿到锁之前可能已被别的回调跑完。
	if refinedMarkerExists(ctx, st, srtKey) {
		return
	}

	srtData, err := st.store.GetAll(ctx, srtKey)
	if err != nil {
		log.Printf("refine: 拉字幕失败 %s：%v", srtKey, err)
		return
	}
	cues := listening.ParseSRT(string(srtData))
	if len(cues) == 0 {
		log.Printf("refine: 字幕解析为空，跳过 %s", srtKey)
		return
	}

	// 旋钮：COS configs/refine_config.json 优先，坏了回退内置（60 秒缓存）。
	rcfg, rsrc := listening.ResolveRefineConfig(ctx, st)

	cfg := refineModel(rcfg)
	if cfg.Endpoint == "" || cfg.APIKey == "" {
		log.Printf("refine: 服务端没配大模型，精校跳过 %s", srtKey)
		return
	}

	changed, model, err := refineCues(ctx, cfg, st, rcfg, cues)
	if err != nil {
		log.Printf("refine: %s 精校失败：%v", srtKey, err)
		return
	}

	// —— 回写 COS 覆盖原 srt（老师要求直接覆盖，不保留人工兜底）——
	if err := st.store.PutObject(ctx, srtKey, []byte(listening.RenderSRT(cues))); err != nil {
		log.Printf("refine: 回写字幕失败 %s：%v", srtKey, err)
		return
	}

	markRefined(ctx, st, srtKey, model, changed)

	parts := listening.SplitParts(cues, rcfg)
	log.Printf("refine: %s 精校完成，%d 句 / %d 段（%s 切分，配置来源 %s），改了 %d 处，模型 %s",
		srtKey, len(cues), len(parts), splitMode(rcfg), rsrc, changed, model)
}

// splitMode 日志用：当前是「按题目」还是「按句数」切。
func splitMode(c listening.RefineConfig) string {
	if c.ByQuestion != nil && *c.ByQuestion {
		return "按题目"
	}
	return "按句数"
}

// refineInFlight 同一份 srt 同时只允许一个精校在跑。
// 数据万象回调重试很频繁，没有这道闸并发写同一份字幕会互相覆盖。
var refineInFlight = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

func refineTryLock(key string) bool {
	refineInFlight.Lock()
	defer refineInFlight.Unlock()
	if refineInFlight.m[key] {
		return false
	}
	refineInFlight.m[key] = true
	return true
}

func refineUnlock(key string) {
	refineInFlight.Lock()
	defer refineInFlight.Unlock()
	delete(refineInFlight.m, key)
}

// refineCues 按题目分段喂给模型，把回来的修改应用到 cues（原地改）。
// 返回总共改了几处、生效的模型名。
//
// 切分由配置决定（默认按题号切，一段 = 一道题；识别不出题号才退回按句数硬切）。
// 一段失败就整体失败 —— 不写半成品，免得只精校了一半就覆盖回 COS。
//
// 走 llmChat（跟查释义同一个）：它内部按 cfg.Timeout 给每个模型各自计时，
// 并复用 SkipModel/MarkResult 的主备切换 + 冷却状态。
func refineCues(ctx context.Context, cfg llm.Config, st *Storage, rcfg listening.RefineConfig, cues []listening.Cue) (int, string, error) {
	// 模板走 60 秒缓存装载（COS 优先，坏了回退内置），改模板不用重新部署。
	tpl, _, _ := listening.ResolveRefinePrompt(ctx, st)

	parts := listening.SplitParts(cues, rcfg)
	total := 0
	model := ""
	for i, p := range parts {
		input := listening.BuildRefineInput(cues, p.Start, p.End)
		reply, m, err := llmChat(cfg, tpl+"\n\n字幕：\n"+input)
		if err != nil {
			return total, model, fmt.Errorf("第 %d/%d 段（第 %d-%d 句，%q）精校失败：%w",
				i+1, len(parts), p.Start+1, p.End, p.Label, err)
		}
		if model == "" {
			model = m
		}
		changes := listening.ParseRefineReply(reply)
		total += listening.ApplyRefinements(cues, changes)
	}
	return total, model, nil
}

// checkRefineTarget 校验回调指向的对象确实在本桶的听力目录下（方案 C）。
//
// 数据万象回调的 body 是明文 XML，拿到 URL 的人能任意构造 Input/Output，所以
// 除了 URL 密钥，还要认对象本身：桶名必须等于本服务配的 TENCENT_COS_BUCKET，
// key 必须落在 sharedBase()/listening/ 之下。越界一律拒，不给精校别的桶/目录的机会。
//
// 桶名比较用常量时间（虽然桶名不是秘密，但顺手一致）；key 前缀用 strings.HasPrefix。
func checkRefineTarget(d ciJobsDetail, st *Storage) error {
	wantBucket := strings.TrimSpace(os.Getenv("TENCENT_COS_BUCKET"))
	// 回调里桶名可能带 APPID 后缀（如 xxx-1251036673），TENCENT_COS_BUCKET 配的也是
	// 这个完整形式，直接比。Input 和 Output 都要落在同一个桶。
	if wantBucket != "" {
		for _, b := range []string{d.Input.BucketId, d.Operation.Output.Bucket} {
			if b == "" {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(b), []byte(wantBucket)) != 1 {
				return fmt.Errorf("回调指向了别的桶：%s", b)
			}
		}
	}
	// key 前缀：Output.Object（srt key）或 Input.Object（视频 key）至少一个必须
	// 落在 listening 目录下。两个都给且都不在 → 拒。
	prefix := st.sharedBase() + "/listening/"
	okIn := strings.HasPrefix(d.Input.Object, prefix)
	okOut := d.Operation.Output.Object != "" && strings.HasPrefix(d.Operation.Output.Object, prefix)
	if !okIn && !okOut {
		return fmt.Errorf("对象不在听力目录下（应在 %s）", prefix)
	}
	return nil
}

// extOf 取文件名扩展名（含点），没有就返回空串。
func extOf(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		return key[i:]
	}
	return ""
}

// refinedMarkerKey 精校标记的 COS key：在原 srt 后加 .refined。
// 例：.../xxx.srt → .../xxx.srt.refined
func refinedMarkerKey(srtKey string) string {
	return srtKey + ".refined"
}

func refinedMarkerExists(ctx context.Context, st *Storage, srtKey string) bool {
	_, err := st.store.HeadObject(ctx, refinedMarkerKey(srtKey))
	return err == nil
}

// markRefined 落一个精校标记对象（内容是 JSON：时间/模型/改了几处），便于排查。
func markRefined(ctx context.Context, st *Storage, srtKey, model string, changed int) {
	meta := map[string]interface{}{
		"refined_at": time.Now().Format(time.RFC3339),
		"model":      model,
		"changed":    changed,
	}
	_ = st.store.PutObject(ctx, refinedMarkerKey(srtKey), []byte(toJSON(meta)))
}
