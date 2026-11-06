package main

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

import (
	"context"
	"crypto/subtle"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/listening"
	"github.com/zhangyf/jrp/internal/llm"
)

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
