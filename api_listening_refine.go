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
	Input     ciObject   `xml:"Input"`
	Operation ciOperaton `xml:"Operation"`
}

type ciObject struct {
	Object string `xml:"Object"`
}

type ciOperaton struct {
	Output ciObject `xml:"Output"`
}

// refineToken 从环境读回调密钥。没配就是「精校功能未启用」。
func refineToken() string {
	return strings.TrimSpace(os.Getenv("JRP_REFINE_TOKEN"))
}

// refineModel 返回精校用的模型链。默认走现网文本链（跟判句同一个），
// 老师想给字幕单独配更强模型时设 JRP_REFINE_MODEL / JRP_REFINE_FALLBACK 覆盖。
func refineModel() llm.Config {
	cfg, ok := llm.LoadConfig()
	if !ok {
		return llm.Config{}
	}
	if m := strings.TrimSpace(os.Getenv("JRP_REFINE_MODEL")); m != "" {
		cfg.Model = m
	}
	if fb := strings.TrimSpace(os.Getenv("JRP_REFINE_FALLBACK")); fb != "" {
		cfg.Fallback = llm.ParseFallbackModels(fb, cfg.Model)
	}
	// 精校一批发 40 句、模型要读完再写，比判句那句慢得多，超时给足。
	if v := strings.TrimSpace(os.Getenv("JRP_REFINE_TIMEOUT")); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= 300 {
			cfg.Timeout = time.Duration(n) * time.Second
		}
	} else {
		cfg.Timeout = 60 * time.Second
	}
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

	// —— 鉴权：独立密钥，常量时间比较防时序 ——
	want := refineToken()
	got := bearerToken(r)
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

	// —— 拉 SRT ——
	srtData, err := st.store.GetAll(ctx, srtKey)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false, "error": "找不到字幕文件：" + srtKey,
		})
		return
	}
	cues := listening.ParseSRT(string(srtData))
	if len(cues) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "字幕解析为空，不精校",
		})
		return
	}

	// —— 精校 ——
	cfg := refineModel()
	if cfg.Endpoint == "" || cfg.APIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "服务端没配大模型（JRP_LLM_ENDPOINT/API_KEY），精校用不了",
		})
		return
	}

	changed, model, err := refineCues(ctx, cfg, cues)
	if err != nil {
		log.Printf("refine: %s 精校失败：%v", srtKey, err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false, "error": "精校失败：" + err.Error(),
		})
		return
	}

	// —— 回写 COS 覆盖原 srt（老师要求直接覆盖，不保留人工兜底）——
	if err := st.store.PutObject(ctx, srtKey, []byte(listening.RenderSRT(cues))); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "回写字幕失败：" + err.Error(),
		})
		return
	}

	// —— 落精校标记，下次回调直接跳过 ——
	markRefined(ctx, st, srtKey, model, changed)

	log.Printf("refine: %s 精校完成，%d 句改了 %d 处，模型 %s", srtKey, len(cues), changed, model)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"srt":     srtKey,
		"cues":    len(cues),
		"changed": changed,
		"model":   model,
	})
}

// refineCues 分批把字幕喂给模型，把回来的修改应用到 cues（原地改）。
// 返回总共改了几处、生效的模型名。任一批模型全挂就返回错误（不写半成品，
// 免得只精校了一半就覆盖回去）。
//
// 走 llmChat（跟查释义同一个）：它内部按 cfg.Timeout 给每个模型各自计时，
// 并复用 SkipModel/MarkResult 的主备切换 + 冷却状态。ctx 目前只用于将来扩展，
// llm.Call 自己建超时 ctx。
func refineCues(ctx context.Context, cfg llm.Config, cues []listening.Cue) (int, string, error) {
	total := 0
	model := ""
	for start := 0; start < len(cues); start += listening.RefineBatchSize() {
		end := start + listening.RefineBatchSize()
		if end > len(cues) {
			end = len(cues)
		}
		input := listening.BuildRefineInput(cues, start, end)
		reply, m, err := llmChat(cfg, listening.RefinePrompt+"\n\n字幕：\n"+input)
		if err != nil {
			return total, model, fmt.Errorf("第 %d-%d 句精校失败：%w", start+1, end, err)
		}
		if model == "" {
			model = m
		}
		changes := listening.ParseRefineReply(reply)
		total += listening.ApplyRefinements(cues, changes)
	}
	return total, model, nil
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
