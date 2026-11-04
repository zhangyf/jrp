# jrp 分层设计

> 日期：2026-11-04　状态：**三块已齐：用例清单（第 3 节）、接口定义（第 5 节）、改造顺序（第 6 节）**。改造已按第 6 节 P0→P5 开工，进度见提交历史。
> 本文件只做设计记录，不承载生产代码。

## 0. 目标与范围

把散落在根目录入口里的**业务逻辑**剥成独立用例层，使 web / 小程序 / CLI 三端引用同一份实现、逻辑统一；入口层瘦身后根目录文件数自然下降，可读性与可维护性提高。

这不是"把文件搬进子目录"（那只是物理重组，handler 里照样各写各的），而是**逻辑分层**：

- 现状：`internal/` 12 个包都是无状态纯规则；带业务流程的"用例"仍散在 `package main`，且吃死 `*Storage`。
- 目标：用例进 `internal/<域>`，依赖改成接口，不认识 `net/http` / `flag` / `*Storage`；入口只做参数解析、调用、输出。

交付分三块：用例清单（第 3 节）→ 接口定义（第 5 节）→ 改造顺序（第 6 节）。均为纸面设计，不含生产代码改动。

## 1. 分层模型

```
┌─ 入口适配层（留根，薄）───────────────────────────────┐
│  HTTP handler (api_*.go，服务 web + 小程序)             │
│  CLI 子命令  (cmd_*.go)                                 │
│  鉴权/会话/装配 (auth, serve, main, wx, quota, types)   │
│  只做：解析参数 → 调用用例 → 序列化结果                  │
└───────────────────┬───────────────────────────────────┘
                    │ 调用（依赖方向单向向下）
┌───────────────────▼───────────────────────────────────┐
│  用例层（新，internal/<域>）  ← 本轮清单要点的东西      │
│  纯进纯出：输入 → 规则 → 结果                          │
│  ⛔ 不 import net/http / flag / *Storage / COS / LLM    │
└───────────────────┬───────────────────────────────────┘
                    │
┌───────────────────▼───────────────────────────────────┐
│  领域规则层（已就位，12 个 internal 包）                │
│  archive / review / grading / sentence / sentencereview │
│  / lessonimport / wordmeta / offline / planconfig       │
│  / llm / knowledge / conjugation                        │
└───────────────────┬───────────────────────────────────┘
                    │ 通过接口注入（用例不认识具体实现）
┌───────────────────▼───────────────────────────────────┐
│  存储 / 外部 IO 层（留根）                             │
│  *Storage（COS 档案、快照、配置）、LLM 网关、WX 接口     │
└────────────────────────────────────────────────────────┘
```

**铁律**：用例层可 import 领域规则层，不可 import 入口层或 IO 层；IO 通过最小接口注入。这样用例能被任何入口引用，也能脱离 COS/HTTP 单测。

## 2. 三端如何收敛（真例子，不用抽象词）

web 与小程序是 JS 前端，都通过 HTTP 打进同一个 Go 服务；CLI 是第二类入口。所以"三端统一"在服务端 = **HTTP handler 与 CLI 两类入口调用同一份用例**。

以「录生词」为例，现状三处已经压到同一个函数：

| 端 | 入口 | 落到 |
|---|---|---|
| 小程序 | `addword.js:411` → `POST /api/words` | `api_words.go:57` |
| web / 任意 HTTP | `POST /api/words` | `api_words.go:57` |
| CLI | `cmd_add_words.go:136` | 直接调 |

三处都调 `words_add.go:42 addWordsToArchive`。方向对了，但这个函数吃 `*Storage`、把「去重/撞释义/分组/版本」规则和「EnsureArchive/写档」IO 揉在一起，当不了独立包。目标形态：

```
internal/words.AddWords(arc, group, items) Result   ← 纯规则，可单测
   ↑ HTTP handler：读档案 → AddWords → 写回 → JSON
   ↑ CLI         ：读档案 → AddWords → 写回 → 打印
```

## 3. 用例清单（本轮交付）

按业务域分组。**现状形态**是分层成本的关键，四类是：

- **纯规则**：不碰 I/O，已有或可补单测 → 直接搬，零成本
- **规则+IO**：吃 `*Storage`，业务规则与读写黏在一个函数 → 要拆 + 注入接口（改造主体）
- **纯 IO**：只做 COS/LLM 读写 → 留作接口实现，不算用例
- **编排壳**：handler / run 的参数解析与输出 → 留入口层，不算用例

**复用入口**里 `HTTP` = web + 小程序（同一 handler），`CLI` = 命令行；标 `跨端` 表示两类入口都在调，是"逻辑统一"最该先保住的地方。

### 3.1 出题 / 计划 plan

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `BuildDuePlan` 到期词计划 | cmd_gen_plan.go:253 | 跨端（api_plan:104 / api_offline:104 / api_record:173 / cmd_gen_plan:93，4 处） | **纯规则**（吃 arc+hold） | 直接搬 |
| `BuildHardPlan` 钉子户计划 | cmd_export_hard.go:55 | 跨端（api_plan:156 / api_offline:77 / cmd_export_hard:163，3 处） | **纯规则** | 直接搬 |
| `buildSentencePlan` / `BuildSentencePlanFrom` 造句计划 | api_plan.go:204 / api_offline.go:221 | HTTP ×2 | 规则+IO（当天快照锁定 + 上传） | 拆 |
| `sentencePlanLocked` 造句快照锁定判定 | api_plan.go:270 | HTTP | 纯规则 | 直接搬 |
| `pruneSentencePlan` 造句结果裁减 | api_record.go:208 | HTTP | 纯规则（已有单测） | 直接搬 |
| `countByStatus` 状态计数 | api_plan.go:310 | HTTP | 纯规则 | 直接搬 |
| `dueHold` 读 hold 词表 | cmd_gen_plan.go:239 | 跨端 | 纯 IO（读 word_meta） | 接口注入 |
| `enrichWordMeta` / `enrichReviewMeta` 展示补全 | api_plan.go:279/296 | HTTP | 展示 join（读 meta） | 接口注入 |

### 3.2 词库录入与维护 words

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `addWordsToArchive` 录生词 | words_add.go:42 | **跨端**（api_words:57 / cmd_add_words:136） | **规则+IO 黏合** | 拆（建议做第一个样板） |
| `sameDefinitionWords` / `definitionCore` / `definitionTerms` / `termsOverlap` / `sameCoreWords` 释义撞车 | cmd_add_words.go | 被 addWords / applyCircle 共用 | 纯规则（已有单测） | 直接搬 |
| `NormalizeWordForm` / `isKana` / `isKanji` / `classifySegment` 词形归一 | cmd_normalize_words.go | CLI | 纯规则 | 直接搬 |
| `applyCircle` 批量导入打标 / `unholdLesson` 放开某课 | circle.go:42/177 | 仅 CLI | **规则+IO 黏合** | 拆 |
| `runMergeWords` 合词 / `runDedupe` 去重 / `runNormalizeWords` / `runUpdateDef` / `runUpdateWord` / `runWordNote` / `runWordMeta` / `runSaveLesson` | 一批 cmd_*.go | 仅 CLI（词库维护） | 规则+IO | 二期 |
| `laterMMDD` / `splitMMDD` / `sumRemovals` 等小工具 | cmd_merge_words.go / cmd_dedupe.go | CLI | 纯规则 | 随域搬 |

### 3.3 练习提交 record

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `ApplyRecord` 写回复习结果 | cmd_record.go:79 | **跨端**（api_record:58 / cmd_record:48） | **规则+IO 黏合**（已具用例雏形：`RecordInput`/`RecordResultSummary`） | 拆 |
| `tomorrowPreview` 明日预告 | api_record.go:161 | HTTP | 规则+IO | 拆 |
| `parseFloatParam` / `intParam` 参数解析 | api_record.go:181/193 | HTTP | 编排壳 | 留入口层 |

### 3.4 统计 stats

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `ComputeStats` / `computeStats` 统计汇总 | cmd_stats.go:73/108 | **跨端**（api_stats:25 / cmd_stats:89） | **规则+IO 黏合** | 拆 |
| `buildJlptProgress` / `buildStatsDetail` / `maxLesson` / `lastIndexOf` | cmd_stats.go | 被统计用例调用 | 纯规则（部分已有单测） | 直接搬 |

### 3.5 词汇总表 lexicon

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `buildLexicon` 全量词表构造 | cmd_lexicon.go:74 | **跨端**（api_lexicon:89 / cmd_lexicon:41） | **纯规则**（吃 arc+meta） | 直接搬 |
| `splitLexiconWord` 假名/汉字/备注拆分 | api_lexicon.go:41 | HTTP | 纯规则 | 直接搬 |

### 3.6 判句 judge

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `judgeWithCache` 判句（含缓存） | api_judge.go:235 | HTTP | 规则+IO（缓存读写） | 拆 |
| `judgePrompt` / `fillJudgePrompt` / `validateJudgePrompt` / `ensureJSONRule` / `promptVersion` / `chooseJudgePrompt` / `renderJudgePrompt` / `parseJudgeReply` / `judgeCacheKey` | api_judge.go | HTTP | 纯规则（已有单测） | 直接搬 |
| `resolveJudgePrompt` 读 COS 模板 | api_judge.go:158 | HTTP | 纯 IO + 缓存 | 接口注入 |

### 3.7 拍照取词 / 查释义 vision · worddef

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `lookupWordDefs` 先查档案再问模型 | api_worddef.go:100 | HTTP ×2（api_worddef:219 / api_vision:402） | 规则+IO | 拆 |
| `fillWordDefPrompt` / `hasParen` / `findWordByKana` / `parseWordDefReplyRaw` | api_worddef.go | HTTP | 纯规则 | 直接搬 |
| `parseVisionReply` / `cleanVisionCandidates` / `stripCodeFence` / `hasJapanese` | api_vision.go | HTTP | 纯解析规则 | 直接搬 |
| `visionChat` / `askVision` / `visionRetryable` / `visionHumanError` | api_vision.go | HTTP | 纯 IO（LLM 网关 + 重试） | 留作接口实现 |

### 3.8 离线练习册 offline（上一批已拆到 internal/offline）

领域规则、解析、渲染、类型已在 `internal/offline`。剩下的是入口编排：

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `handleExport` 导出练习册 | api_offline.go:30 | HTTP | 编排壳（调 offline.Generate*） | 留入口层 |
| `handleImport` 回填解析 | api_offline.go:143 | HTTP | 编排壳（调 offline.Parse*） | 留入口层 |

### 3.9 句库 / 回炉 sentence

规则层已在 `internal/sentence` / `sentencereview` / `lessonimport`；根目录剩 CLI 编排与体检：

| 用例（现状函数） | 位置 | 复用 | 现状形态 | 处理 |
|---|---|---|---|---|
| `checkReviewSentences` 回炉句体检 | cmd_sentence_review.go:543 | CLI | 规则+IO | 拆 |
| `runSentenceMigrate` / `Lint` / `Bank` / `Preview` / `runSentenceImport` 等 | cmd_sentence.go / cmd_lesson_import.go | CLI | 编排 + 规则 | 二期 |
| `latestOrEmpty` / `firstOrEmpty` 等 | cmd_sentence.go | CLI | 纯规则 | 随域搬 |

### 3.10 明确不进用例层（留根）

| 部分 | 文件 | 理由 |
|---|---|---|
| 账号鉴权 / 会话 / 安全基线 | auth.go, wx.go, quota.go, api_auth.go, api_admin.go, api_trial.go, api_grade.go | 既定策略「账号鉴权留根」；三处 pending 拦截、fail-closed、限流都贴在这里 |
| 装配与入口骨架 | main.go, serve.go（`go:embed web`）, bootstrap.go | `package main` 装配、`go:embed` 路径约束、CLI 分发 |
| 类型别名中枢 | types.go | 让根包调用方继续用短名；分层后由各用例包自行 import internal 类型 |
| COS / 存储实现 | storage*.go（7 个） | 作为用例层接口的实现方，不被用例反向依赖 |
| HTTP 薄适配 | api_draft.go, api_knowledge.go, api_conjugation.go, api_review.go 等 | 多为查缓存/转发，业务规则已在 internal 包 |

## 4. 清单小结（数字）

- 识别业务用例约 **40 个**，归到 **9 个域**。
- **纯规则、可直接搬**（近零成本）：约 20 个 —— `BuildDuePlan` / `BuildHardPlan` / `buildLexicon` / 判句 prompt 系列 / 释义撞车系列 / 统计聚合 / 视觉解析 等。这批搬完就能实打实减少根目录文件。
- **规则+IO 黏合、要拆+注入接口**（改造主体）：约 10 个 —— `addWordsToArchive` / `ApplyRecord` / `ComputeStats` / `applyCircle` / `buildSentencePlan` / `judgeWithCache` / `lookupWordDefs` 等。
- **已被 HTTP+CLI 跨端共用**的用例：约 8 个（`BuildDuePlan` / `BuildHardPlan` / `addWordsToArchive` / `ApplyRecord` / `ComputeStats` / `buildLexicon` / `dueHold` 等）—— 说明"逻辑统一"不是从零开始，已有雏形，只是没独立成包。
- 其余为编排壳 / 纯 IO / 留根部分，不进用例层。

## 5. 接口定义

接口定义不凭空编：先照 `*Storage` 的真实方法签名和各用例实际调用面归纳。**最小接口**按"用例需要什么方法"定义，不复制全部 ~60 个方法；`*Storage` 隐式满足，无需显式声明。

### 5.0 前置事实：用例包不能 import main，类型得先分流

接口签名里出现的类型分两类：

- **已在 `internal/`、可直接用**：`Archive`(=`archive.Archive`)、`ReviewPlan`/`PlanWord`(=`offline.*`)、`WordMeta`(=`wordmeta.*`)、`PlanConfig`(=`planconfig.*`)、`SentenceBank`/`History`/`Wrong`(=`sentence.*`)、`ReviewBank`(=`sentencereview.*`)、`llm.Config`。**纯规则用例的入出参全是这些，所以它们能零阻力直接搬。**
- **仍定义在 `package main`**：`LangConfig`(types.go:14)、`RecordInput` / `RecordResultSummary`、`StatsResult` / `StatsSnapshot` / `StatsDetail` / `JlptProgress`、`WordDefItem`、`LexiconItem`、`WordToAdd` / `AddWordsOutcome`、`Draft`、`ReviewSnapshot`、`judgeResult`。

结论：**规则+IO 用例搬进用例包时，输入输出 DTO 随之迁入该用例包**；纯规则用例无此负担。这是"接口定义"落地的前提，不是一个可选优化。另注：新用例包名要避开已存在的 12 个 internal 包（尤其 `internal/conjugation` 已有领域包，词形归一要么并入要么归到 `internal/words`，不能重名）。

### 5.1 最小接口集（7 个端口 + 1 个 LLM）

| 接口 | 方法（真实签名） | 被哪些用例用 | 实现方 |
|---|---|---|---|
| `ArchiveStore` | `EnsureArchive` / `LoadArchiveForRead` / `DownloadLatestArchive` / `UploadArchive` | words / record / worddef / plan / circle / lexicon / conjugation | `*Storage` |
| `PlanStore` | `DownloadPlan` / `DownloadHardPlan` / `UploadPlan` / `UploadHardPlan` / `DownloadSentencePlan` / `UploadSentencePlan` / `DeleteSentencePlan` | record / plan / offline | `*Storage` |
| `MetaStore` | `DownloadWordMeta` / `UploadWordMeta` | lexicon / plan / circle / dueHold / worddef | `*Storage` |
| `ConfigStore` | `DownloadPlanConfig` / `DownloadJudgePrompt` | judge / 计划旋钮 | `*Storage` |
| `SentenceStore` | `Download/UploadSentenceBank` / `Download/UploadSentenceHistory` / `Download/UploadSentenceWrong` / `Download/UploadReviewBank` | plan 造句 / record / sentence 维护 | `*Storage` |
| `StatsArchiveReader` | `ListArchives` / `ReadArchiveBlob`（新增，见 5.2） | stats | `*Storage`（补方法） |
| `SnapshotStore` | `Upload/Download/DeleteReview`、`Upload/Download/DeleteDraft` | 仅入口编排（api_record / api_review / api_draft） | 本期**不设用例接口**，留入口层直调 |
| `LLMCaller` | `Post(endpoint, apiKey, body, timeout)` | judge / worddef / vision | 包 `internal/llm.Post` |

代表性签名（其余同 `*Storage` 现有签名，参数类型见 5.0）：

```go
// 用例包 internal/<域>/ports.go —— 由消费方定义，不由存储方定义
type ArchiveStore interface {
    EnsureArchive(ctx context.Context, lang string) (*archive.Archive, string, error)
    LoadArchiveForRead(ctx context.Context, lang string) (*archive.Archive, error)
    DownloadLatestArchive(ctx context.Context) ([]byte, string, error)
    UploadArchive(ctx context.Context, filename string, data []byte) error
}

type PlanStore interface {
    DownloadPlan(ctx context.Context, planDate string) (*offline.ReviewPlan, error)
    DownloadHardPlan(ctx context.Context, planDate string) (*offline.ReviewPlan, error)
    UploadPlan(ctx context.Context, plan *offline.ReviewPlan) error
    // UploadHardPlan / DownloadSentencePlan / UploadSentencePlan / DeleteSentencePlan 同签名
}

type LLMCaller interface {
    Post(endpoint, apiKey string, body []byte, timeout time.Duration) (string, error)
}
```

**为什么 `ConfigStore` 只有读**：`UploadPlanConfig` / `DeletePlanConfig` / `UploadJudgePrompt` / `DeleteJudgePrompt` 只被 CLI 管理命令用，用例层不需要 → 留 `*Storage` 直调，不进用例接口。这正是"最小"的含义。`judgeCache` / `judgePromptCache` 是**进程内内存缓存，不是外部 IO**，随用例一起搬，不注入接口。

### 5.2 三处必须先收口的泄漏（本次盘点的硬发现）

接口化前，这三点在 IO 层补掉，否则用例层会反向依赖 COS/全局：

| # | 现状 | 问题 | 收口办法 |
|---|---|---|---|
| 1 | `computeStats` 直接 `storage.store.GetAll(ctx, e.key)` (cmd_stats.go:169) | 用例摸底层对象存储，`objstore.Store` 泄漏 | 在 `*Storage` 加公开方法 `ReadArchiveBlob(ctx, key) ([]byte, error)`，`stats` 只认这个 |
| 2 | `ListAllArchives` 返回 `[]objstore.ObjectInfo` (cmd_stats.go:110) | 第三方类型渗进用例签名 | `StatsArchiveReader.ListArchives` 返回用例层自有的 `[]ArchiveRef{Key, Filename, Date}`，在适配层转换 |
| 3 | `addWordsToArchive` / `ApplyRecord` 用 `LangConfigs[lang]` 全局 map + `time.Now()` | 隐式全局/时钟依赖，无法纯函数化、无法单测 | `LangConfig` 与 `now time.Time` 作**参数**传入用例 |
| 4 | `UploadHistory` 被 merge/dedupe/normalize 用作备份 | 仅 CLI 词库维护域，非核心用例 | 留 `*Storage` 直调，不进用例接口 |

补完后 `stats` 用例形态：

```go
type ArchiveRef struct{ Key, Filename string; Date time.Time }
type StatsArchiveReader interface {
    ListArchives(ctx context.Context) ([]ArchiveRef, error)
    ReadArchiveBlob(ctx context.Context, key string) ([]byte, error)
}
// internal/stats.ComputeStats(r StatsArchiveReader, lang string, days int, now time.Time) (*StatsResult, error)
```

日期筛选、按天取最新版本、`ParseArchive` / `CountByStatus` / `buildStatsDetail` / `buildJlptProgress` 全是纯规则，留在用例/规则层。

### 5.3 实现方与依赖方向

- 所有端口由根包 `*Storage` 实现（Go 隐式满足；可加 `var _ words.ArchiveStore = (*Storage)(nil)` 编译期断言）。`LLMCaller` 由 `internal/llm` 之上的一层薄适配器实现，`LoadConfig` / `SkipModel` / `MarkResult` 冷却逻辑留在 IO 层，用例只管"给 body 拿 reply"。
- 入口层（留根）负责：构造 `*Storage` → 注入用例 → 拿结果序列化。用例不认识具体类型。
- 单测：给用例传内存 fake（map 模拟 `ArchiveStore` / `PlanStore`），不碰 COS / HTTP / LLM。

### 5.4 规则+IO 用例 → 接口映射（落地钩子）

| 用例 | 目标包 | 需要接口 | 随迁 DTO / 备注 |
|---|---|---|---|
| `addWordsToArchive` | `internal/words` | `ArchiveStore` | `WordToAdd` / `AddWordsOutcome`；`LangConfig`、`now` 入参 |
| `applyCircle` / `unholdLesson` | `internal/words` | `ArchiveStore` + `MetaStore` | 同包，复用释义撞车规则 |
| `ApplyRecord` | `internal/record` | `ArchiveStore` + `PlanStore` | `RecordInput` / `RecordResultSummary` |
| `computeStats` | `internal/stats` | `StatsArchiveReader` | `Stats*` 一族；先补 5.2 的洞 |
| `buildSentencePlan` | `internal/plan` | `PlanStore` + `SentenceStore` + `MetaStore` | `ReviewPlan` 已是 internal |
| `judgeWithCache` | `internal/judge` | `ConfigStore` + `LLMCaller` | `judgeResult`；**字面一致直接判对不调模型**等口径照搬，行为不变 |
| `lookupWordDefs` | `internal/worddef` | `ArchiveStore` + `LLMCaller` | `WordDefItem` |
| `visionChat` / `askVision` | 留 IO 层 | `LLMCaller` | 视觉模型名/重试/冷却留网关，不进用例 |
| 纯规则一批 | `internal/<域>` | 无 | 直接搬，零接口 |

## 6. 改造顺序

原则：**先样板、先纯规则、先跨端共用、一域一提交可单独 revert**；每批先写清「线上行为 / 代价 / 回滚」，跑通行为不变验证再进下一批。全程不自动 merge、不替换线上二进制、不动 COS / 线上数据；部署单独确认。

| 批次 | 范围 | 线上行为 | 代价 | 回滚 | 验证 |
|---|---|---|---|---|---|
| **P0 前置** | 补 5.2 的 `ReadArchiveBlob` 方法、`ArchiveRef` 转换、`LangConfig`/`now` 入参化 | **零变化**（只加新方法/新参数，不改老调用） | 3 处 IO 层薄改动 | 单 commit revert | 全量 go test + vet |
| **P1 样板：words** | `addWordsToArchive` → `internal/words.AddWords`，注入 `ArchiveStore`；两入口（api_words / cmd_add_words）改调 | **不变**（同一份档案字节、同一份 JSON） | 新增 `internal/words` 包 + 1 端口 | 单 commit revert | 三端各录一词，对比响应 JSON 与档案 `WriteArchive` 输出字节 |
| **P2 纯规则批量迁** | 约 20 个纯规则函数按域搬 `internal/<域>`（plan / lexicon / judge prompt / 释义撞车 / 统计聚合 / 视觉解析 / 词形归一） | **不变**（纯函数搬家 + 别名） | 近零，纯搬 + 别名 | 单 commit revert | 现有单测全绿 + 别名等价 |
| **P3 规则+IO 主域** | record → stats → plan 造句 → lexicon/conjugation 读路径；按跨端共用优先 | **不变** | 拆函数 + 注入接口，改两处入口 | 每域单 commit revert | 每域行为不变对拍（见下「验证口径」） |
| **P4 LLM 三域** | judge / worddef / vision 注入 `ConfigStore` + `LLMCaller` + `ArchiveStore` | **不变** | 最敏感：贴着限流 / pending 每日 2 图 / 模型冷却，靠后做 | 每域单 commit revert | 判句口径单测 + 字面命中不调模型断言 |
| **P5 维护域（二期）** | circle、词库维护 CLI 一批、句库 import/体检 | **不变**（仅 CLI） | 量大、复用价值低，最后或不做 | 单 commit revert | CLI 冒烟 |

**为什么 P1 选 words**（老师已认可的方向）：`addWordsToArchive` 已 HTTP+CLI 跨端共用、规则与 IO 黏合最典型、DTO 少，是验证"拆成用例 + 注入接口后行为不变"最省事的样板。跑通它，后面每域都是同一套动作。

**验证口径**（每批统一，可直接并入老师的功能回归对比）：

- `go test -mod=readonly -count=1 ./...`、`-shuffle=on -count=1 ./...`、`internal/... -shuffle=on -count=10`、`go vet ./...`、三平台 `CGO_ENABLED=0` 构建、`git diff --check`、LF 版 `gofmt`（Windows `core.autocrlf=true` 下直接 gofmt 会误报）。
- **行为不变**的三条硬指标：HTTP 响应 JSON 字节、COS 存档字节（`archive.WriteArchive` 输出）、Excel 渲染字节——三项都不许因分层改变。用真数据快照前后对拍。
- 一域一 commit，`git revert` 单批可退；数据不用回滚。

**边界**：本文件是纸面设计。截至当前：**没有改任何生产代码、没有提交、没有推送、没有部署**。是否开工、按不按 P0→P5 推进，等老师点头；两份 SKILL.md 包边界章节与工作日志，等真正开始动代码时再同步。
