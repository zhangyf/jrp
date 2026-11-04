package judge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/zhangyf/jrp/internal/sentence"
)

// 造句的语义判定：字面比对不一致时，问大模型「这句意思成立吗」。
//
// 设计前提（2026-09-29 老师定）：
//  1. 字面一致永远直接判对，不调模型 —— 0 延迟、0 成本、不依赖外网。
//  2. 只有字面不一致才问模型；模型只判「意思成不成立」，不判语法优劣。
//  3. 模型不可用（没配密钥 / 超时 / 报错）就退回字面判定，练习绝不卡住。
//  4. 判定结果按 (原句, 老师写的) 缓存：刷新、换设备重开都得到同一个结论，
//     不能这次判对下次判错 —— 跟「不提交不换题」是同一个规矩。
//
// 密钥只存在于服务器环境变量，前端拿不到，也不会出现在任何响应里。
//
// 本包只放纯规则与用例编排：不认识 net/http / flag / *Storage / COS / LLM 网关。
// 读 COS 模板走 ConfigStore，问模型走 Caller，两者由根包适配注入。

// BuiltinPrompt 内置判定模板。COS 上没有 / 模板坏了 / 读不到时用这份。
//
// 2026-10-12 收紧过一次（老师要求「不能太松」）：
//   - 助词**缺失**从「算对」改成「判错」—— 练造句就是练助词，该有的 を/が/に/へ/で
//     没写，等于没练到。但助词**换成另一个同样成立**的（へ↔に）仍判对，别冤枉。
//   - 「拿不准就判正确」改成「拿不准就看要素齐不齐」：要素齐全只是写法不同 → 判对；
//     要素缺失（尤其助词缺失导致格关系不明）→ 判错。
//     没写成「拿不准就判错」，是因为那样会把 へ↔に 这类同样成立的替换一起拖下水。
//
// 第 4 条（感叹词/语气词不算错）和第 5 条（没学过的表达也算对）不能删 ——
// 删了模型就变成语法警察，会判一堆老师本来会的句子是错的。
// TestJudgePromptKeepsConservativeRules 盯着这几条不许删。
//
// 整段 prompt 可外置到 COS（configs/judge_prompt.txt），这份是兜底。
// 占位符：{answer} 参考答案、{chinese} 中文意思、{input} 学生写的。
const BuiltinPrompt = `你是日语老师。判断学生写的句子是否表达了参考答案的意思。

参考答案：{answer}
中文意思：{chinese}
学生写的：{input}

判定规则：
1. 只判断意思是否成立，不评价语法是否地道、措辞是否优雅。
2. 以下差异视为正确：汉字与假名的写法不同、全角半角、标点符号、空格、词序微调、同义词替换、敬体简体差异。
3. 助词缺失判错：该有的 を/が/に/へ/で 没写，导致「谁对谁做了什么」说不清的，判错。助词换成另一个同样成立的（へ↔に 等）仍判对；多加了但不改变意思的助词也算对。
4. 感叹词、语气词、开场填充词（ええ/えっ/えっと/あの/うーん/あれ 等）互相不同不算错 —— 它们不承载句子的核心意思。唯一例外：把肯定答成否定（はい↔いいえ、うん↔ううん）才算错。
5. 学生用了没学过的表达，只要意思成立，仍判正确。
6. 拿不准时按关键要素是否齐全来判断：主体、动作、对象、时态、肯定否定都齐全、只是写法不同 → 判对；要素缺失（尤其是助词缺失，导致格关系说不清）→ 判错。

例：参考答案「李さんは 中国人です。」学生写「李さん 中国人です。」→ 错误（省了 は）。
例：参考答案「ご飯を 食べました」学生写「ごはん 食べた」→ 错误（省了 を；写成假名和简体本身没问题）。
例：参考答案「駅へ 行きます」学生写「駅に 行きます」→ 正确（へ 和 に 都成立）。
例：参考答案「えっと、銀座ですか？」学生写「ええ、銀座ですか。」→ 正确（开场词不同，核心问句一样）。
例：参考答案「はい、そうです」学生写「いいえ、違います」→ 错误（肯定变否定）。

只输出一行 JSON，不要解释：{"correct": true 或 false, "reason": "一句话中文说明，20 字以内"}`

// JudgePrompt 用内置模板渲染判定词。
func JudgePrompt(answer, chinese, input string) string {
	return FillPrompt(BuiltinPrompt, answer, chinese, input)
}

// FillPrompt 把占位符换成实际句子。
// 中文意思为空时，模板里含 {chinese} 的整行直接删掉 —— 免得留一行「中文意思：」空壳。
func FillPrompt(tpl, answer, chinese, input string) string {
	hasChinese := strings.TrimSpace(chinese) != ""
	var kept []string
	for _, ln := range strings.Split(tpl, "\n") {
		if !hasChinese && strings.Contains(ln, "{chinese}") {
			continue
		}
		kept = append(kept, ln)
	}
	s := strings.Join(kept, "\n")
	s = strings.ReplaceAll(s, "{answer}", answer)
	s = strings.ReplaceAll(s, "{chinese}", chinese)
	s = strings.ReplaceAll(s, "{input}", input)
	// 删掉空行后可能留下的连续换行
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// ValidatePrompt 校验外置模板。护栏之一：缺 {answer} 或 {input} 一律判坏，
// 否则填完的 prompt 里根本没有要判的句子，模型只能瞎猜。
func ValidatePrompt(tpl string) error {
	if strings.TrimSpace(tpl) == "" {
		return fmt.Errorf("模板为空")
	}
	var miss []string
	if !strings.Contains(tpl, "{answer}") {
		miss = append(miss, "{answer}")
	}
	if !strings.Contains(tpl, "{input}") {
		miss = append(miss, "{input}")
	}
	if len(miss) > 0 {
		return fmt.Errorf("模板缺少占位符 %s", strings.Join(miss, "、"))
	}
	return nil
}

// EnsureJSONRule 护栏之二：模板里没提 JSON 输出就补上。
// 少了这行模型就开始自由发挥，ParseReply 抠不出结论，判分全线失灵。
func EnsureJSONRule(p string) string {
	if strings.Contains(strings.ToLower(p), "correct") {
		return p
	}
	return p + "\n\n只输出一行 JSON，不要解释：{\"correct\": true 或 false, \"reason\": \"一句话中文说明，20 字以内\"}"
}

// PromptVersion 模板版本：内容 sha256 前 12 位。
// 改一个字版本就变，草稿里的旧结论据此作废重判 —— 不能拿旧规矩的结论当新规矩的。
func PromptVersion(tpl string) string {
	sum := sha256.Sum256([]byte(tpl))
	return hex.EncodeToString(sum[:])[:12]
}

// ChoosePrompt 在「COS 上读到的」和「内置的」之间做选择。抽成纯函数是为了能单测 ——
// 护栏全在这里：读不到、空的、缺占位符，一律回退内置，练习绝不因为一份坏文件卡住。
func ChoosePrompt(cosText string, err error) (tpl, source string) {
	if err != nil || strings.TrimSpace(cosText) == "" {
		return BuiltinPrompt, "builtin"
	}
	if err := ValidatePrompt(cosText); err != nil {
		log.Printf("judge-prompt: COS 上的模板不可用（%v），回退内置模板", err)
		return BuiltinPrompt, "builtin"
	}
	return cosText, "cos"
}

// RenderPrompt 渲染出最终发给模型的文本。
func RenderPrompt(tpl, answer, chinese, input string) string {
	return EnsureJSONRule(FillPrompt(tpl, answer, chinese, input))
}

// Result 是判定结果（原根包 judgeResult）。字段与 json 行为保持不变。
type Result struct {
	Correct bool
	Reason  string
}

// ParseReply 从模型回复里抠出 JSON。模型常常在 JSON 前后加废话
// （```json 围栏、说明文字），所以找第一个 { 到最后一个 } 之间那段来解析。
func ParseReply(reply string) (Result, error) {
	s := strings.TrimSpace(reply)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	var out struct {
		Correct *bool  `json:"correct"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return Result{}, fmt.Errorf("解析模型回复失败：%w（原文：%.80s）", err, reply)
	}
	if out.Correct == nil {
		return Result{}, fmt.Errorf("模型没给 correct 字段：%.80s", reply)
	}
	return Result{Correct: *out.Correct, Reason: strings.TrimSpace(out.Reason)}, nil
}

// CacheKey 缓存键带模板版本：prompt 一改，旧的判定结论自然失效，
// 不会出现「规矩换了但还拿旧结论糊弄」的情况。
func CacheKey(version, answer, input string) string {
	return version + "\x00" + sentence.Norm(answer) + "\x00" + sentence.Norm(input)
}
