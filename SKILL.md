---
title: "Language Review Planner (jrp)"
summary: "Ebbinghaus-based vocabulary review system. Manages word archives, generates Excel review plans with sentence exercises, records review results, and tracks progress. Also ships a self-hosted web review UI (flashcard + handwriting + 活用练习). Supports Japanese/English/French."
read_when:
  - User wants to review vocabulary words
  - User sends photos of textbook vocabulary sections
  - User sends text with new words to learn
  - User asks for today's review plan or Excel
  - User reports review results (correct/wrong word numbers)
  - User asks to update a word's definition
  - User asks for learning statistics
  - User sends textbook photos for knowledge document creation
  - User mentions 日语/英语/法语 单词复习, 生词, 记忆曲线, 复习计划
  - User asks about the web review UI / 网页版 / 活用练习（动词て形、形容词过去式等）
  - User asks to build/update/deploy jrp to the server (部署、上线、服务器)
---

# Language Review Planner (jrp)

## Overview

A Go CLI tool that manages vocabulary learning archives using the Ebbinghaus forgetting curve.
The AI handles photo recognition, text parsing, and textbook-sentence selection; the Go binary handles
all archive operations (parse, update, version, upload to COS).

## Binary

- Windows: `~/.workbuddy/skills/jrp/jrp.exe`
- macOS:   `~/.workbuddy/skills/jrp/bin/jrp`

## ⚠️ 双机开发（Windows 工作机 / macOS 家用机）

同一个 repo `zhangyf/jrp`，**两台机器各有一份工作副本**，但只有一份共享的 `SKILL.md`：

| | Windows（工作机） | macOS（家用机） |
|---|---|---|
| 源码目录 | `C:\Users\efrainzhang\jrp-src` | `~/jrp` |
| 二进制 | `~/.workbuddy/skills/jrp/jrp.exe` | `~/.workbuddy/skills/jrp/bin/jrp` |
| 编译 | PowerShell + `$HOME\go-sdk\go\bin` | `$HOME/.workbuddy/binaries/go/bin` + `GOPROXY=goproxy.cn` |
| 默认 shell | Git Bash | zsh |
| 代理 | `http://127.0.0.1:26698` | `http://127.0.0.1:7897` |

**规则（2026-09-20 定）**：
1. **SKILL.md 是共享文件**，写之前先想清楚这条是通用规则还是只在一台机器上成立。
   只在一台机器上成立的内容（路径写法、shell 行为、符号链接命令、代理端口）
   **必须写进文末的「Windows Environment Notes」/「macOS Environment Notes」**，
   不准塞进共用章节——Mac 上写的 `ln -sf` / `.zshrc` 在 Windows 上无意义，反之亦然。
2. **行为差异要修在代码里，不要修在文档里**。能用 Go 抹平的平台差异（路径分隔符、
   目录解析顺序）就抹平，让两台机器跑同一份二进制得到同样结果；文档里写"记得加 env var"
   这种靠人记的约定，迟早会漏。
3. 看到远端有提交，先判断它来自哪台机器（macOS 痕迹：`.zshrc`、`ln -sf`、goproxy.cn；
   Windows 痕迹：PowerShell、`C:\`、Git Bash 沙箱）。别默认"本机落后就要跟上"。

## COS Credentials

⚠️ **凭证放在所有技能目录之外：`~/.workbuddy/cos-credentials/`**
- `.env` — 明文主本（0600，唯一真源，保留）
- `.env.enc` — AES-256-GCM 加密，key = SHA-256(hostname:username:normalizeSkillDir(dir))

**解析顺序（代码自动完成，两台机器都不需要设 env var）**：
1. `JRP_COS_SKILL_DIR` 环境变量（显式覆盖）
2. `~/.workbuddy/cos-credentials/`（只要里面有 `.env` 或 `.env.enc` 就选它）
3. `~/.workbuddy/skills/tencentcloud-cos/`（旧位置，未迁移的机器仍能跑）

**⚠️ 不要把凭证放进 `~/.workbuddy/skills/tencentcloud-cos/`。** 该目录由市场托管，
每次技能更新整体替换、清空本地文件（2026-09-10 那次清掉了 `.env.enc`，第三次了）。
`cos_node.mjs` 只认技能根目录下的 `.env`/`.env.enc`，所以那里放一个指向主本的链接/副本：
- macOS: `ln -sf ~/.workbuddy/cos-credentials/.env ~/.workbuddy/skills/tencentcloud-cos/.env`
- Windows: 见文末 Windows 章节（mklink / 直接复制）

改了 `.env` 之后重新生成 `.env.enc`：
`$JRP_BIN encrypt-env`（会自动落到解析出来的目录）

**⚠️ 升级到 2026-09-20 之后的构建必须重跑一次 `encrypt-env`**：密钥种子的 skillDir
改成了规范化路径（Clean + 正斜杠），旧的 `.env.enc` 全部失效，报错形如
`decryption failed (wrong machine/user?): cipher: message authentication failed`。
这不是文件损坏——用 `$JRP_BIN encrypt-env` 从明文 `.env` 重新加密即可，无损。
（明文 `.env` 缺失时才需要走下面的找回流程。）

**凭证彻底丢失时的找回流程**：
1. 确认 bucket：搜 `~/.workbuddy/audit-log/*.jsonl` 里 `TENCENT_COS_BUCKET=` 出现最多的值
   （本项目 = `openclaw-backup-tx-1251036673`，region=`ap-beijing`）。不要靠猜。
   注意：audit-log **只记录了 region/bucket**，secret id/key 是脱敏的，取不到。
2. 找回明文密钥：在旧工作区脚本里搜 `AKID`。同一 appid 下所有桶共用这套密钥。
3. 重建：写明文 `.env`（4 变量）到 `~/.workbuddy/cos-credentials/.env` → `$JRP_BIN encrypt-env`
   → 按上面各平台的方式重建链接。
4. 只是要把**旧位置还没坏的 .env.enc 导出来**：`$JRP_BIN decrypt-env --out <新位置>/.env`。

## COS Storage Structure

```
language-review/
├── ja/
│   ├── archives/    # Current archive files (日语学习进度档案_YYMMDD_vA.B.md)
│   ├── history/     # Historical archive snapshots
│   ├── plans/       # plan_<date>.json + review_<date>_vA.B.xlsx (daily)
│   │                # hard_<date>.json + hard_words_<date>_vA.B.xlsx (export-hard)
│   └── knowledge/   # Lesson knowledge documents
├── en/
│   └── ... (same structure)
└── fr/
    └── ... (same structure)
```

## Archive Naming Convention

```
{语言}学习进度档案_YYMMDD_vA.B.md
```

- Each new day: A=1, B=0
- Each update same day: B+1 (v1.0 → v1.1 → v1.2...)
- **句子轮换也算 update，但只在「当天已出过带句子的版本」之后才 bump**：`gen-plan --sentences` 时，若当天 changelog 已有「句子」标记记录，则 bump 小版本（B+1）并写 changelog「重新生成复习文件（句子轮换）」；**当天第一次带句子**则用当前版本号（新日初始化的 v1.0），只写「生成复习文件（含句子N句）」不 bump——否则每天第一个复习文件会错误地从 v1.1 起步（2026-09-09 老师指出）。
- Major bump (A+1, B reset to 0): format change, 20+ word import, or user request
- Next day: new file, A resets to 1, B resets to 0

## Ebbinghaus Intervals

| Review Count | Interval |
|---|---|
| 0 (new/just wrong) | 1 day |
| 1 | 2 days |
| 2 | 4 days |
| 3 | 7 days |
| 4 | 10 days |
| 5 | 15 days |
| 6 | 30 days |
| 7 | 60 days |
| 8 | 90 days |
| 9 | 120 days |
| 10+ | 180 days |

Words with errors use consecutiveCorrect count for interval; words without errors use reviewCount.

## Status Rules

- 🔄待测试: reviewCount == 0
- 🔴待巩固: errorRate >= 30%
- 🟡基本掌握: reviewCount < 5 or errorRate >= 15%
- 🟢已掌握: reviewCount >= 5 and errorRate < 15%

## Review Categories (Excel)

The Excel review plan uses 5 categories with priority sorting:

| Category | Condition | Priority |
|---|---|---|
| ☠️钉子户 | **accuracy < 60% AND reviewCount >= 3** | 0(highest) |
| 🔴待巩固 | errorRate >= 30%, not a hard word | 1 |
| 🔄待测试 | reviewCount == 0 | 2 |
| 🟡基本掌握 | reviewed but not mastered | 3 |
| 🟢抽查 | reviewCount >= 5, errorRate < 15% | 4 (lowest) |

### ⚠️ Hard words are defined by ACCURACY, not absolute error count

`钉子户` used to mean `ErrorCount >= 3`. That was wrong: absolute error counts grow
monotonically with review count, so the rule conflated "often wrong" with "reviewed a lot".

Measured on a 669-word archive, the old rule flagged 220 words, of which 27 had80–90%
accuracy — e.g. `こんにちは` (28 reviews, 3 errors, 89%) was flagged while `しょっぱい`
(6 reviews, 6 errors, **0%**) was also flagged at the same severity, and words like
`おんせん` (5 reviews, 2 errors, 60%) were missed entirely. The accuracy rule yields 96 words.

The `minReviews >= 3` guard prevents a brand-new word answered wrong once (1/1 = 0%)
from being flagged on its first miss.

Note: `CountNailHouseholds` in `internal/review/ebbinghaus.go` keeps a separate, error-count-based
formula on purpose — it feeds the changelog's 钉子户 column, and changing it would make
historical version rows incomparable.

Words are sorted by priority and grouped into separate sections in the Excel.
Each section has a title row (e.g., "☠️钉子户 54词") and column headers with gray background (D9D9D9).
序号 cells contain plain numbers (no emoji); status is conveyed by the section title.
Continuous numbering across all sections.

### Excel Layout

- **Sheet names**: `✏️练习版` (practice) / `✅答案版` (answers)
- **练习版 8 columns**: A(序号,5) B(中文,17) C(日语,20.5) **D(比对,6)** E(序号,5) F(中文,17) G(日语,20.5) **H(比对,6)**
- **答案版 6 columns**: A(序号,5) B(中文,17) C(日语,20.5) D(序号,5) E(中文,17) F(日语,22.5)
- **Auto-check formulas (练习版 only)**: D and H columns contain `_wpsfn.REGEXP` formulas that compare the user's handwritten answer (C/G) with the answer sheet after stripping parenthetical kanji (e.g. `ちがいます(違います)` → `ちがいます`). WPS auto-evaluates these; match returns 1, mismatch returns 0.
  - D column: `=IF(Cn=(_wpsfn.REGEXP(✅答案版!Cn,"[（(][^）)]*[）)]",2,"")),1,0)`
  - H column: `=IF(Gn=(_wpsfn.REGEXP(✅答案版!Fn,"[（(][^）)]*[）)]",2,"")),1,0)`
- **Per-category sections**: Each non-empty category gets its own section with title + header + word rows. Section title rows merge A:H (练习版) or A:F (答案版).
- **Word rows**: Two-column layout (left A/B/C, right E/F/G for 练习版; left A/B/C, right D/E/F for 答案版), 序号 has gray bg + center align
- **Sentences**: `📝 造句 共N句` header, S1/S2 numbering, B:C merged for Chinese, D:F merged for answer

## Knowledge Documents (COS)

Lesson knowledge points are stored in COS `knowledge/` directory. This is the **primary
programmatic source** for sentence generation — jrp can list and fetch them directly without
any external dependency.

- `jrp --lang ja list-knowledge` — list all knowledge documents (name + size)
- `jrp --lang ja get-knowledge --name <filename>` — download a document's full content

Current Japanese knowledge docs —— 知识点 `标准日本语初级上册_第N课知识点.md` **第1-17课齐全**，
课本原文汇编 `课本原文汇编_第N单元（基本课文/应用课文）.md` **第1-5单元齐全**
（第1单元=第1-4课、第2=5-8、第3=9-12、第4=13-16、第5=17-20，故汇编已覆盖到第17课）。
另有 `造句易错语法速查（第12-13课）.md`。

### 知识点板块（web + 小程序，2026-11-03）

- 接口：`GET /api/knowledge`（课号列表）/ `?lesson=N`（分块正文）。
  **markdown 在服务端拆块**（`h1/h2/h3/p/quote/list/table` 六种 + 行内 `b`/`c` 标记），
  前端只按 type 摆样式 —— 小程序没有 md 渲染能力，而且「哪几块模糊」必须按块切，
  拆块这件事只能在服务端干。
- 两端入口：小程序 `pages/knowledge`；web 顶部导航「知识点」tab（`web/knowledge.js`）。
  ⚠️ **两条端都要有**：2026-11-03 老师发现 web 端没这个 tab（当初只按小程序需求做的，
  web 那排导航从建站起就是那 7 个）。补的时候是**纯前端新增** —— 接口、解析、模糊
  判据全在服务端，前端只是多画一屏。
  ⚠️ `/api/knowledge` 是唯一不走 `withUser` 的读接口（匿名要能进），自己认会话。
- 未登录（含体验版）也能进，只是看不全：服务端回 `visible`，下标 >= visible 的块盖模糊。
  **这是展示层软限制**（响应里全文都在，抓包能拿到），目的是引导登录，不是防攻击。
- 网页端两块端各自维护登录入口：小程序弹登录浮层，web 链到 `login.html`。

### IMA Knowledge Base (legacy / human-readable fallback)

IMA is read-only. Knowledge points have been migrated to COS; IMA remains as a human-readable
browsing interface only. Prefer COS `get-knowledge` for programmatic access.

IMA MCP tools (fallback only):
- `mcp__ima-mcp__get_knowledge_list`: List documents in a knowledge base
- `mcp__ima-mcp__fetch_media_content`: Read a document's content
- `mcp__ima-mcp__search_knowledge`: Search for specific topics

Knowledge base IDs:
- Japanese (自学日语): `7452509467574409`
- English (英文知识库): check `get_knowledge_base_list` for current ID

## Workflows

### 1. Add Words from Photos

**Trigger**: User sends photo(s) of textbook vocabulary section.

**Steps**:
1. Read the photo(s) — identify the vocabulary section
2. Extract each word: target language word (including kanji) + Chinese definition
3. **先对照档案去重**：下载最新档案，逐词核对——词形完全相同的跳过；
   同读法不同汉字（如已有的 あたたかい(暖かい) 与本课 あたたかい(温かい)）
   不要新增条目（会拆分复习历史），用 `update-def` 把区分写进现有词条释义；
   同读法同动词不同汉字（とります(撮ります) vs とります(取ります)）语义确实
   不同才新增。
   **释义不许跟已有词一字不差**（老师明确要求 2026-09-24）：同义/近义词也要在括号里
   写上区分点（用法、搭配、范围），否则复习时看中文根本分不出该写哪个词。
   例：だめ「不行，不可以（口语，可单独用；也指没用、白费）」vs
   いけません「不行，不可以（接て形表示禁止：～てはいけません）」；
   エアコン「空调（冷热两用）」vs クーラー「空调（冷气，只制冷）」。
   **不只是「一字不差」才算撞车（2026-09-28 老师二次反馈）**：两个词的中文释义只要
   **共享一个义项**，复习时就没区分度。例：なかなか「相当，很，非常」vs とても「非常」、
   とおります「通过，经过」vs すぎます「过」、がくせい「学生」vs せいと「学生(中小学)」。
   所以释义写成「A、B」这种多义项时，每个义项都要单独判一次有没有跟别的词共用。
   **合格写法**：主释义只留一个主义项，次要义项和搭配说明写进括号——
   おふろ「浴室，浴缸（泡澡用；洗澡这个动作说 お風呂に入ります）」。
   日语里一词多义很常见，但复习义项只能有一个，否则老师看中文不知道该写哪个。
   `add-words` / `update-def` 会自动检测并输出 `dup_definitions` / `same_def_as`
   （一字不差）以及 `dup_core_definitions` / `same_core_as`（共享义项）——
   看到警告就必须补区分，不能无视。
   **⚠️ 录完还要核「假名↔汉字」读音（2026-09-29 事故）**：第16课把「黒い」的假名
   标成 くらい，生成了不存在的词形 `くらい(黒い)`「黑，黑色」，跟已有的
   `くろい(黒い)`「黑色的」重复 —— 老师练习时写 くろい 被判错才发现。体检办法：
   拉 `/api/lexicon`，按 kanji 分组找「同一汉字多种假名」（正常只剩 家 いえ/うち、
   降ります おり/ふり 这种真两读）。发现同一个词有两个词形（其中一个错音）→
   用 `merge-words` 合并，**不能**用 `update-word`（new_word 已存在会中止）；
   合并后记得删 `word_meta.json` 里那条废弃词形的 pos 条目。Create a JSON file:

```json
{
  "language": "ja",
  "group": "第8课 生词表（7/13）",
  "words": [
    {"word": "すし", "definition": "寿司"},
    {"word": "さしみ", "definition": "刺身"}
  ]
}
```

4. Run: `jrp --lang ja add-words --input /tmp/words.json`
5. Report: how many words added, duplicates skipped, new version, total word count

**Note**: If 20+ words are added, the Go binary auto-triggers a major version bump.

### 2. Add Words from Text

**Trigger**: User sends vocabulary in text form (e.g., "すし 寿司, さしみ 刺身").

**Steps**: Same as photos, but parse from text instead of image.

### 3. Generate Daily Review Plan

**Trigger**: User asks for today's review, 复习计划, or daily Excel.

**Steps**:
1. Run: `jrp --lang ja gen-plan --date YYYY-MM-DD` (no `--sentences` flag yet)
   - If due_count is 0, inform the user — no review needed for that date
   - If no archive exists for the target date, gen-plan auto-initializes today's v1.0 archive (with a changelog entry "新日初始化（gen-plan）") before generating the plan, so the Excel uses v1.0 instead of inheriting the previous day's version
   - **This call is read-only for data extraction.** The Excel it generates will be overwritten by Step 5. Do NOT present the Excel from this call.
2. Read the due words from the JSON output (`plan_words` field)
3. Read knowledge points from COS for grammar points from recently learned lessons:
   - `jrp --lang ja list-knowledge` to see available lessons
   - `jrp --lang ja get-knowledge --name <filename>` to fetch a lesson's content
   - (Fallback: IMA MCP tools if a lesson is not yet in COS)
4. **⚠️ Sentence Grammar Constraint**: Sentences MUST use ONLY grammar points the user has
   already learned. The authority for "what has been learned" is the COS knowledge documents.

   a. **Read ALL knowledge docs**: Fetch every document returned by `list-knowledge` via
      `get-knowledge`. This establishes the exact grammar scope. The docs are the single
      source of truth — if a grammar pattern is not in them, the user hasn't learned it.

   b. **Known traps** (grammar the AI often overuses but is NOT yet learned):
      - て形 (verb て, い-adj くて, な-adj で) → ~Lesson 14-16
      - た形 (plain past) → ~Lesson 19
      - から (because/cause) → Lesson 11
      - ～にくい/～やすい (difficult/easy to) → N4
      - Plain/casual form (～だ ending) → Lesson 20+
      - Relative clauses (verb-modifying nouns, e.g. 「昨日買った本」) → Lesson 25+

      When you need to connect two sentences, use そして / でも / が instead of て形.

   c. **Select 20 textbook-original sentences (课文原文)**:

      > ⚠️ **这一段已经过时（2026-10-09）**：造句早就不是「AI 每天挑 20 句」了。
      > COS 上有独立句库 `language-review/ja/plans/sentence_bank.json`（495 句课本原文，
      > 覆盖第1-18课），Go 的 `BuildSentencePlan`（`sentence.go`）自己按课轮转挑句，
      > `api_plan.go` 里 `sentenceBaseCount = 20`。**AI 只在建库、每学一课追加句库时介入**，
      > 日常出题不要插手。下面 a~e 的规则现在主要是「往句库里加句」时的选材标准，
      > 以及变形句 / 回炉句的生成约束——轮换、去重、错句重练这些 Go 全做了。

      - Sentences MUST be verbatim lines from the textbook, taken directly from the
        knowledge docs' 基本课文 / 应用课文 / 语法例句 tables — NEVER AI-composed.
      - **Pick priority order**:
        1. **FIRST** look in the consolidated textbook doc `课本原文汇编_第N单元（应用课文）.md`
           (full dialogues from 应用课文 + 单元末场景对话 + 阅读文) — this is the
           richest and most authentic source.
        2. **Then** look in `课本原文汇编_第N单元（基本课文）.md` if it exists for the
           target unit — has the 4-sentence 基本课文 + 4-group short dialogue for each
           lesson (clear, declarative lines work well as writing prompts).
        3. **Then** fall back to each lesson's knowledge doc 基本课文 tables.
        4. **Lastly** fall back to 语法例句 tables within learned-scope lessons.
      - Chinese prompt = the doc's own Chinese translation of that line; Japanese
        answer = the verbatim original.
      - **Cover ALL learned lessons (第1-17课；知识点第1-17课、单元汇编第1-5单元都已入库), NOT just the last 2-3.** Balance the 20
        sentences across the full learned scope. Prioritize by (a) lessons where today's
        due words cluster, (b) lessons where 钉子户 are densest — notably 第7课生活动词
        (おろします/はらいます/ぬぎます/あけます/しめます/つけます/けします/きます/はきます)
        and 第9课一类形容词 (からい/あまい/つめたい/にがい/しょっぱい/すっぱい…) — and
        (c) the most recent 1-2 lessons. Do NOT let the last lesson (e.g. 第12课比较句)
        crowd out earlier lessons every day; 第1-6课 basic patterns (～は～です / 存在句
        あります・います / で工具 / 交通工具 / 频率副词) MUST rotate in regularly.
      - **⚠️ 轮换约束（2026-09-08 起，防造句重复）**：老师指出"每课句子总是那几句"，
        排查发现根因有二：①"优先覆盖重点语法"的规则把选择收敛到每课的少数典型句
        （第12课 → 永远「ほど+否定/いちばん/より」，第11课 → 永远「歌が好き/韓国語が
        分かる」，第13课 → 永远「1週間に2回/机の上に3冊」），实测 2026-09-03~08 四天
        80 句里「小野さんは歌が好きです」等 4 句连出 4 次、7 句连出 3 次；②每天从零
        挑句、无历史记录，AI 无法避开近期出过的句子。修正规则：
        1. **挑句前先读轮换记录** `language-review/ja/plans/sentence_history.json`
           （COS，用 `cos_node.mjs download` 取，key 同上；⚠️ 脚本实际路径是
           `~/.workbuddy/skills/tencentcloud-cos/scripts/cos_node.mjs`，不是技能根目录，
           且必须 cwd=技能根 运行才能加载 `.env`；npm 依赖若丢失，用
           `NODE_PATH=~/.workbuddy/binaries/node/workspace/node_modules` 指过去），
           它按日期记录每天出过的 answer 原文。
        2. **同一句 7 天内不重复出**。除非某课可用句池 < 需要的句数，才允许复用，
           且优先复用它"最久没出过"的一句。
        3. 把"覆盖重点语法"从硬优先降为**软参考**：语法点要覆盖，但不等于只能出
           那一句典型句——先按"最近没出过/从没出过"筛选，再在其中挑覆盖语法点的。
        4. 挑句后把当天 20 句 answer 追加进 `sentence_history.json` 并上传回 COS，
           清理 30 天前的旧日期记录。
        5. **⚠️ 自检必须做「归一化」比对（2026-09-16 踩坑）**：历史上同一句可能被
           存成带句号和不带句号两个版本（如「ガレージに車が5台あります」vs「…あります。」），
           所以**不能用 `last[s]=max(dates)` 这种按原字符串求最新日期的办法**来判断
           「多久没出过」——它会把带句号的近期版本当成另一句，让你误判为「>=7 天可用」。
           正确做法：把近 7 天所有句子做成归一化集合
           `norm(s)=re.sub(r'[\s。．、,.]','',s)`，候选句也归一化后再比对；
           自检同时查「近 7 天重复」和「当天 20 句内部重复」。实测这一步能在提交前
           拦下 5~6 句假可用句。
        6. **⚠️ 错句重练（2026-09-16 起）**：`record` 的 `sentence_results` 是死字段，
           造句对错不落库，所以造句的间隔重复在外部文件做：
           `language-review/ja/plans/sentence_wrong.json`（下载/上传方式同 sentence_history）。
           规则：
           - 挑句时先取 `status=open` 且 `next_due <= 今天` 的条目，**强制放进当天 20 句**
             （不受「7 天不重复」限制，错句就是要近期重出），放进去后 `last_seen=今天`；
           - 老师反馈该句写对 → `status=archived`；写错 → `wrong_count+1`、`last_wrong=今天`、
             `next_due=今天+interval`，interval = 3（count=1）/ 7（count=2）/ 14（count≥3）；
           - 老师反馈的错句本文件里没有 → 追加条目，count=1，next_due=今天+3。
           - ⚠️ 错句也是课本原文，照抄 answer 即可，**不要自造**。
        7. **变形句「同骨架换词」（2026-09-16 老师批准采用）**：每天 20 句里**最多 4 句**
           可做变形（纯句子模式 50 句 → 上限 10 句，同比例）。默写原文可能靠背，换词才真正验句型是否内化。硬约束：
           - **骨架 = 课本原句的助词序列与句型结构，一字不改**（は/が/を/に/へ/で/と/より/
             ほど/の，以及 〜ほど〜ない、〜に〜回、〜へ〜に 行きます 这类框架）。
           - **可替换**：主语/宾语/场所/时间名词、数量词、形容词（同类互换：い形↔い形、
             ナ形↔ナ形）、动词。
           - **替换词的状态门槛（2026-09-16 老师放宽）**：只要词在档案里存在即可用，
             🟢已掌握／🟡基本掌握／🔴待巩固／☠️钉子户 **全部允许**；
             唯一排除 🔄待测试（从未测过，等同生词）。
             老师原话：「待巩固的词可以出现在替换词里」——放进句子里反而是额外一遍复习，
             不用担心拖累正确率。（此前版本把 🔴 也排除了，导致「だします(出します)」
             这类 🔴 动词用不了，已废弃该限制。）
           - **不可替换**：助词、句型框架、谓语后缀（です/ます/ない 等）。
           - ⚠️ **替换词必须先核验在档案里存在**（档案行首 `|词`）。实测踩坑：
             「りんご」根本不在档案；「図書館／映画／有名／広い」**按汉字查不到**，
             表里写法是「としょかん(図書館)」「えいが(映画)」「ゆうめい(有名)」「ひろい」——
             按汉字去查会误判"已学过"，结果拿生词造句。动词同理要查「かいます(買います)」。
           - 中文提示前加「【变形】」前缀便于识别；answer 给正确日文。
           - 变形句同样遵守「近 7 天不重复」，且必须能指回原型课本句。
        8. **回炉变形句（2026-10-09 老师定的，2026-10-13 起每天固定 +20 句）**：
           这是上面第 7 条的**独立通道**，不占「20 句里最多 4 句」的变形句名额。
           - **背景**：档案里有一批正确率≥80%、但 30 天以上没复习的词 —— 实测 **290 个**，
             最久闲置 56 天。有个反直觉的点：**超过 60 天没复习的词一个都没有**
             （每天复习量大，60 天内都轮到过一遍），所以「好久」的实际刻度是 45~60 天，
             不是半年。按艾宾浩斯间隔它们还没到期，可实际已经忘了。老师明确不要
             「加进单词表重背」，所以走造句：拿原型句换掉一个词，助词序列不动。
           - 句库 `language-review/ja/plans/sentence_review.json`，读取与挑句逻辑在
             `sentence_review.go`；每天 **20 句**（2026-10-13 从 12 提到 20 —— 290 条的池子
             按 12 句/天要 24 天才轮完，实际练过的只有 37 个词），排在 20 句原文之后
             （编号 21-40，共 40 句/天）。**配额已外置**到 COS `configs/plan_config.json`，
             `jrp --lang ja plan-config --set sentence_review_count=N` 改（不用重新部署），
             内置默认在 `plan_config.go`。
             网页端 + 离线导出 Excel 都带（`BuildSentencePlanFrom` 里也追加了）。
             ⚠️ 调这个配额只改常量即可，但池子受「锚点词闲置≥30天」过滤，
             练过的词越多可用池越小，某天不足 20 条时总数会相应少几条（不是 bug）。
           - **为什么用变形句而不是「从课文里找含该词的句子」**：实测 290 个回炉词只有
             **58%** 能在课本句子里找到，没命中的全是 カナダ / ブラジル / ピザ / アニメ /
             まんが / やきゅう / べんごし 这类生词表名词 —— 标日初级前 17 课课文里压根
             没出现过。变形句不看运气，覆盖率 100%。
           - 挑句三条筛选（顺序不能反）：① 排除当天已出的句子；② 锚点词最近复习过
             （闲置 < `DefaultReviewIdleDays` = 30 天）就跳过；③ 按「最久没出过」排序。
             ⚠️ 第 ② 条不能省 —— 句库是静态的，不刷这层的话练过的词会一直占着名额，
             回炉就退化成随机抽句。
           - **圆级优先（2026-11-03 老师定的）**：三圆词（手册最高频）拿
             `sentence_review_circle_ratio`% 的名额（默认 **70**，20 句里 14 句），
             剩下的给二圆 / 一圆 / 没标；**层内仍按「最久没出过」排**。
             锚点词 `ReviewSentence.Word` 就是档案词形，跟 `word_meta.json` 的
             `circle` 是同一个 key，**精确匹配、不需要分词**。
             ⚠️ 配额是**上限不是保底**：三圆池子不够就由其他档补满，题量永不缩水；
             `circle` 读不到（词表炸了）或 ratio≤0 → 整步跳过，行为跟没这功能时一致
             （`TestReviewSentencesCircleDisabledKeepsOldBehaviour` 盯着这条回滚路径）。
             ⚠️ 只认 `三圆`/`二圆`/`一圆` 三个中文值，写成「3圆」一律落「没标」档。
             ⚠️ **只加在回炉句上，不加到 20 句课本原文** —— 原文那 20 句靠按课轮转
             覆盖课文，Go 里没分词器，靠词形子串猜「句子里有没有三圆词」必然有误差，
             而且会破坏跨课摊平。体检看 `sentence-preview` 的 `review_circle`。
           - **同一原型句当天只用一次**：多个回炉词可能共用同一个骨架，且原型句本身可能
             就在当天 20 句里。不去重会出现第 20 句写「森さんは 先週 休みました」、
             第 22 句写「森さんは 今日 休みました」—— 骨架刚练过一遍，变形句等于白出。
           - 句库由 **AI 生成**：词性匹配、语义合理性由 AI 把关（Go 里没有分词器，判断不出
             句子里哪个位置能换什么词性）。生成后跑 `jrp --lang ja sentence-preview`
             确认 `review_added` = 12、`inner_repeat` = 0。
           - **句库要分批补**：当前 43 条覆盖回炉池的前 60 个词里的实词（助词 / 感叹词 /
             数字 / あります・います・します 这类功能词跳过 —— 它们本来就遍布句库，
             变形句没意义）。剩下 230 个词分批补，每批 50 个。
           - **补批走 CLI（2026-11-03 才有的入口，以前只能 COS 直传）**：
             ```bash
             jrp --lang ja sentence-review-todo --limit 0         # 谁还缺变形句（每词带 3 条推荐原型句）
             jrp --lang ja sentence-review-bank --dump            # 整库下载
             jrp --lang ja sentence-review-bank --merge new.json  # 只追加（按锚点词/句子去重，补批用这个）
             jrp --lang ja sentence-review-bank --file all.json   # 全量覆盖
             ```
             `--merge` 上传前体检：**致命（阻断上传）**—— 锚点词不在档案里、原型句不在
             主句库里、answer 跟课本原文一模一样、id 重复；**只提醒**—— 变形句跟原型句的
             空格分段数不一致（多半是助词被改动了，人看一眼）。
             `id` / `source` / `source_doc` / `prototype_id` / `type` 都能按原型句自动补，
             写的时候只给 `word / pos / lesson / prototype / answer / chinese` 就够。
             id 从现有最大号往后编（R309、R310…）。
           - **2026-11-03 补批实况**：`sentence-review-todo` 报 missing=89（qualified 286 /
             covered 197），一次补满 → 句库 **397 条**、missing 归零。
             ⚠️ 句库自 2026-10-10 那批之后一直没补 —— **它不会自动加新词**，
             每过几周就得跑一次 `sentence-review-todo`；当时还有 552 个「在路上」的词
             （7 天内 37、8-14 天 131、15-21 天 214、22-29 天 170）会陆续到期。
           - **轮转周期有公式，别拍脑袋**：回炉库**一词一句**（397 条 = 397 个锚点词），
             所以同一个词不会今天这句明天那句地反复考。
             三圆词轮一遍 = 三圆池 ÷（回炉配额 × ratio%）= 101 ÷ (20×70%) ≈ **7.2 天**；
             其余档 6 条/天摊 296 条 ≈ 49 天。嫌三圆太勤就调小 ratio（0 = 不分层）。
      - **应用课文短问答可"成对"出题**：甲问乙答两句合成一道题（中文提示写成一问
        一答），把「あちらです」「5,800円です」「わたしのです」这类一句两三个词的
        短应答也纳入句池，避免有效句池被压缩到只剩每课 4 句基本课文。
      - **⚠️ NEVER invent your own sentences.** Every sentence must trace to a
        textbook original in the knowledge docs. This replaced the old "generate from
        scratch" rule at the user's request (2026-08-27): textbook lines are guaranteed
        correct and within learned scope, so the old leak risk disappears.
      - **⚠️ Hard scope guard**: Only pick sentences from lessons the user has learned
        (currently 第1-17课，第1-5单元汇编全部入库). If a consolidated-原文 doc has a placeholder
        "（待补 — 用户未提供...）", SKIP that section entirely.

   d. **Self-check before saving**: Verify each of the 20 sentences is a verbatim
      textbook original (copy-pasted from a knowledge doc's 基本课文/应用课文/语法例句
      table, not paraphrased) and that its Chinese prompt matches the doc's translation.
      If a sentence is your own invention or a paraphrase, discard it and pick a real
      textbook line instead.
5. Save sentences to a fresh JSON file (always create a new file, never append to an old one):

```json
[
  {"chinese": "小李比森先生年轻", "answer": "李さんは森さんより若いです"},
  {"chinese": "日本料理中寿司最好吃", "answer": "日本料理の中で寿司がいちばんおいしいです"}
]
```

6. Run: `jrp --lang ja gen-plan --date YYYY-MM-DD --sentences /tmp/sentences.json`
   - This is the FINAL call that produces the deliverable Excel with both words and sentences
   - Default output: `outputs/review_YYYY-MM-DD_vA.B.xlsx` (version auto-parsed from archive)
   - **带 `--sentences` 的版本号规则**：当天 changelog 已有「句子」标记（即当天已出过带句子的版本）→ bump 到 vX.(Y+1) 写「句子轮换」changelog；当天第一次带句子 → 保持当前版本（新日初始化 v1.0），写「生成复习文件（含句子N句）」不 bump。Step 1（无 `--sentences`）不 bump。
   - **This is the only Excel you present to the user** — the Excel from Step 1 (no sentences) was a data-extraction artifact and MUST NOT be presented
7. Present the Excel file to the user using present_files (path must be in workspace `outputs/`)

**精简版（出差/到期量过大时）**：老师出差或到期词爆量（>150）时，主动提出「精简版」——
只保留 ☠️钉子户 + 🔴待巩固 + 🟡基本掌握，砍掉 🟢抽查（已掌握词的抽查，砍掉不丢进度，
明天照常到期）。CLI **没有过滤参数**，做法：
   1. 照常 `gen-plan`（全量），记住分区在计划里是**连续排序**的：钉子户 → 待巩固 →
      基本掌握 → 抽查，序号连续。用 `tmp_due_*.json` 找出最后一个要保留的序号 N。
   2. 复制 xlsx，用 openpyxl 对**两个 sheet 都** `delete_rows(start, count)` 删掉
      「🟢抽查」那一整段（含它上面/下面的空行）。行号从 `✅答案版` 全表 dump 里确定。
   3. **不要重排序号**——保留原始序号 1..N，这样老师报的错词序号能直接用 `record`
      回填（record 按 COS 里 plan_YYYY-MM-DD.json 的序号匹配）。
   4. 造句段在抽查段之后，删行后会自动接上，无需另处理；练习版公式引用的是答案版
      同一行号，两个 sheet 删同样的行即保持对齐。
   5. 输出文件名加后缀，如 `outputs/review_2026-09-16_v1.0_精简版64.xlsx`。

**Excel structure**:
- Sheet names: `✏️练习版` / `✅答案版`
- Words grouped by status section: ☠️钉子户 → 🔴待巩固 → 🟡基本掌握 → 🟢抽查 → 🔄待测试
- 练习版: 8-column layout: 序号 | 中文 | 日语 | 比对 | 序号 | 中文 | 日语 | 比对
- 答案版: 6-column layout: 序号 | 中文 | 日语 | 序号 | 中文 | 日语
- 比对列(D/H)含自动比对公式，WPS 打开后自动显示匹配结果
- Gray header rows (D9D9D9), centered bold
- Sentence exercises: `📝 造句 共N句` title, S1-SN numbering, B:C merged Chinese, D:F merged target language
- Output naming: `review_yyyy-mm-dd_vA.B.xlsx` (version from current archive)

**纯句子模式 `--sentences-only`（2026-09-18 新增）**

**Trigger**：老师要「纯句子模式」「只出句子不出单词」「出 50 句」等。

**命令**：
```
jrp --lang ja gen-plan --date YYYY-MM-DD --sentences-only --sentences tmp_sentences_XXXX.json
```
- `--sentences-only` 必须配 `--sentences`，否则 CLI 报错退出。
- 行为差异（相比普通模式）：
  - `plan.Words` 全清 → Excel **没有任何单词区块**，造句区从第 1 行开始（标题「📝 造句 共N句」）。
  - **即使当天到期词为 0 也会正常出文件**（普通模式 due_count=0 直接返回不生成）。
  - 默认输出名 `outputs/review_YYYY-MM-DD_sentences_vA.B.xlsx`（普通模式无 `_sentences`）。
  - COS plan 存到**独立 key** `language-review/ja/plans/sentences_<date>.json`（普通模式是
    `plan_<date>.json`），Excel 备份 `sentences_<date>_vA.B.xlsx`。**两者互不覆盖**——
    这是必须的：纯句子 plan 里 words 为空，若写进 daily plan 会让当天 `record` 全部 not_found。
  - 输出 JSON 里 `kind="sentences"`、`sentence_count=N`、`due_count=0`。

**题量**：默认 **50 句**（普通模式 20 句）。挑句规则**完全沿用上面第 3 节 c 的全部约束**：
课本原文、句库覆盖第1-17课（第1-5单元汇编齐全）、近 7 天归一化不重复、错句重练强制插入、变形句。
- 变形句上限按比例放大：普通模式 20 句最多 4 句 → **纯句子 50 句最多 10 句**。
- 50 句量大，**归一化自检必须做**（近 7 天重复 + 当天内部重复），否则重复率会明显上升。
- 跨课覆盖要摊平：第1-17课每课至少 2-3 句，别被最近学的课吃掉一半（程序侧已有轮转起点轮换+最新两课加权）。

**回填**：纯句子模式**没有单词，不要跑 `record`**（跑了也只是写一条 0 对 0 错的 changelog）。
造句对错走 `sentence_wrong.json`，规则不变（错 → +3/+7/+14 天重出；对 → archived）。

**同日两种模式并存**：先出单词版、再出纯句子版（或反过来）都安全，plan key 独立；
但两次都会各写一条 changelog，且第二次会因当天 changelog 已有「句子」标记而 bump 小版本
（v1.0 → v1.1）。属正常现象，不用修。

### 4. Record Review Results

**Trigger**: User reports review results (e.g., "1,3,5写错了，其他对").

**⚠️ 铁律（2026-08-22 事故后确立）**：`record` 只更新 JSON 里出现的序号，未出现的词**完全不处理**。
老师只报错词 = **当天到期词里其余全部写对**。绝不能只提交错词——否则写对的词 ReviewCount 不 +1，
正确率永久冻结、钉子户上不了岸。每次 record 必须把当天 plan 的**全部到期词**都写进 `word_results`：
错词 `correct:false`，其余 `correct:true`。唯一例外：老师明确说"某部分没写/没来得及写"时，按 `word.status` 排除那部分
（如 2026-09-14 老师只写了钉子户+待巩固 21 词、未写 41 词，`word_results` 就只写 1–21；未写的词完全不处理、下次照常到期）。

**⚠️ changelog 描述无法表达"未写 Z"（2026-09-15 确认）**：`record` 的 changelog 描述是 CLI 固定生成的
「复习结果：X词写对，Y词写错」，CLI **没有** `--desc` / `--note` 参数，传了也会被忽略。所以当 `M < N` 时，
档案里永久查不到"还有 Z 词没写"这一事实，只有 X 和 Y。在这个参数加上之前，必须把
`到期N / 实写M / 写对X / 写错Y / 未写Z` 五个数字写进**工作区 memory 当天日志**，否则当天实况不可追溯。
TODO：给 `jrp-src` 的 `record` 加 `--note <string>`，拼进 changelog 描述。

**Steps**:
1. 等老师当天反馈**收尾**（报完所有错词，通常以"XX部分全对"或"XX部分没写"收尾）再一次性 record，
   不要每报一批就匆忙提交。老师分多批报错词 → 先累积全部错词序号，收尾时一起提交。
2. 下载当天 plan JSON 拿全量到期词：`cos_node.mjs download --key "language-review/ja/plans/plan_<date>.json"`。
   ⚠️ 不要 re-run `gen-plan` 来拿词表——它改档案后编号会变、还会覆盖 COS 上的 plan。
3. Create JSON——`word_results` 覆盖全量到期词：

```json
{
  "plan_date": "2026-07-18",
  "language": "ja",
  "word_results": [
    {"number": 1, "correct": true},
    {"number": 2, "correct": false},
    {"number": 3, "correct": true}
  ],
  "sentence_results": []
}
```

**⚠️ `sentence_results` 目前是死字段（2026-09-02 发现）**：`ApplyRecord` 完全不读它，
造句对错**不会**写入档案、不影响任何词的 ReviewCount/正确率，也不进 changelog。
所以**绝不能对老师说"报句号我给你补记"** —— 那是承诺了 CLI 不具备的能力。
老师反馈造句错误时的正确做法：
1. 明确告知句子结果当前不落库，只做口头讲解 + 语法纠正；
2. 若错句暴露了**某个具体单词**忘了（例：2026-09-02 第5/6句卡在 `にんき(人気)`），
   而该词**不在当天 plan 里**（因此 `record` 无法按 number 定位），就在回复里点名
   该词并说明它的当前状态（如刚摘帽），提醒它实际未牢固；不要伪造 record。

4. Run: `jrp --lang ja record --input /tmp/results.json`
   - Add `--hard` when the numbers come from an **export-hard** Excel (see workflow 5),
     otherwise the numbers will be resolved against the wrong plan
5. Report to 老师 with a fixed template so each version is traceable at a glance:
   `到期N词 / 实写M词 / 写对X / 写错Y / 未写Z`，加上 updated 钉子户数、new version。
   这五个数必须与 changelog 描述、`word_results` 严格一致——N = 当天 plan 到期词总数，
   M = 实际写了的词数（word_results 长度），Y = 错词数，Z = N − M（老师明确说没写的部分）。
6. **⚠️ To show the user which words they got wrong, read the original Excel from `outputs/`**
   (e.g. `review_2026-08-06_v1.0.xlsx`). Do NOT re-run `gen-plan` — after `record` changes the
   archive, `gen-plan` produces different numbering and **overwrites** the COS plan JSON.

### 5. Export All Hard Words (钉子户专项)

**Trigger**: User asks for the hard-word list / 钉子户清单 / "把所有钉子户导出来".

**⚠️ Do NOT hand-parse the archive markdown to build this list.** Use the command —
it applies the canonical accuracy rule and produces the standard Excel layout.

**Key difference from `gen-plan`**: `gen-plan` filters by `IsDue()` and only surfaces
words **due today**. `export-hard` is a **full census** of every hard word in the archive
regardless of due date.

**Steps**:
1. Run: `jrp --lang ja export-hard`
   - `--min-accuracy` (default 0.60) — accuracy below this counts as a hard word
   - `--min-reviews` (default 3) — small-sample guard
   - `--date` only affects the output filename; the command is read-only and never
     initializes a new-day archive or bumps the version
2. Output: `outputs/hard_words_<date>_vA.B.xlsx`, 2 sheets (`✏️练习版` / `✅答案版`),
   grouped into three severity sections by accuracy:
   - 🔥重度钉子户（正确率<30%）
   - ⚠️中度钉子户（正确率30~45%）
   - 💤轻度钉子户（正确率45~60%）
   Sorted by accuracy ascending, continuous numbering across sections.
3. Present the Excel to the user with present_files (already in workspace `outputs/`)
4. Report the counts from the JSON: `hard_count` / `severe_count` / `moderate_count` / `mild_count`
5. To record results from this Excel, use `record --hard` — the plan lives at a separate
   COS key (`plans/hard_<date>.json`) so it never clobbers the daily plan

### 6. Update Word Definition

**Trigger**: User asks to update a word's Chinese definition.

**Steps**:
1. Create JSON:

```json
{
  "language": "ja",
  "word": "すし",
  "definition": "寿司（一种日本料理，用醋饭和生鱼片制成）"
}
```

2. Run: `jrp --lang ja update-def --input /tmp/def.json`
3. Report: old definition → new definition, new version

### 6b. Update Word Form (词形修正)

**Trigger**: User points out a typo in a word's target-language form (the 日语单词 column),
e.g. a stray long-vowel mark in the kanji annotation (`さんぽします(散歩ー)` should be
`さんぽします(散歩します)`). `update-def` only changes the definition column, so it
cannot fix the word form itself — use this command instead.

**Steps**:
1. Locate the exact current form (download the latest archive and grep, or read the
   original Excel) — the match is exact, so every character must line up.
2. Create JSON:

```json
{
  "language": "ja",
  "word": "さんぽします(散歩ー)",
  "new_word": "さんぽします(散歩します)"
}
```

3. Run: `jrp --lang ja update-word --input /tmp/word.json`
4. Report: old form → new form, new version

**Guard**: aborts if `new_word` already exists elsewhere in the archive (would create a
duplicate). Word count is unchanged; no `history/` backup is taken (single-entry edit,
mirrors `update-def`).

### 6c. Word Notes (备注：近义词怎么区分)

**Trigger**: 两个词的中文释义容易混（老师复习时看中文分不出该写哪个），
或老师直接说「X 和 Y 的释义没什么区分」。

**备注写什么**：写区分点 —— 用法、搭配、范围、语气。不写例句，不重复释义。
例：`とおります(通ります)` → `空间上从某处穿过（道・橋・トンネル を通ります）；過ぎます 是时间流逝/超过某点`

**什么时候不用写**：词义明确、跟别的词不撞车的，空着。空着是正常的，不是缺数据 ——
老师的原话是「如果词义比较明确不需要额外说明，那么备注说明这里可以空着」。

**存在哪**：`word_meta.json` 的 `items[词形].note`，跟词性同一份表（不在档案里 ——
档案表格是 7 列，加列会牵动解析/写入/Excel 全链）。

**命令**：

```bash
# 单条设置 / 清除
jrp --lang ja word-note --word とおります(通ります) --text "空间上从某处穿过；過ぎます 是时间流逝/超过某点"
jrp --lang ja word-note --word とおります(通ります) --clear

# 批量合并（JSON: {词形: 备注}）
jrp --lang ja word-note --file /tmp/notes.json

# 列出现有备注（只看写了的，没写的不列）
jrp --lang ja word-note
```

**⚠️ 匹配是精确的完整词形「假名(汉字)」**。对不上会报错并给几个候选（只打假名也能搜到）。
不猜、不做模糊匹配 —— 猜错会把另一个词的备注贴上来。

**批量改词性**：上传是**整份覆盖**，所以必须先导出再改再传，不能直接手搓一份新的：

```bash
jrp --lang ja word-meta --dump /tmp/word_meta.json   # 先导出（整份）
# 改完（补齐未标注的词的 pos/sub）
jrp --lang ja word-meta --file /tmp/word_meta.json   # 整份覆盖上传
jrp --lang ja word-meta                              # 看分类统计，确认「未标注」为 0
```

怎么知道哪些词还没标：查 `/api/lexicon` 里 `pos` 为空的条目（`word-meta` 的统计里
也会出现「未标注」这一类，为 0 就是全标完了）。**导入新课生词后要补一遍。**

**页面怎么显示**（`/api/plan` / `/api/review` 按词形 join 出来，前端同一套）：
- 释义只摆**主部**（第一个括号前的部分），括号里那截挪到「备注」行 —— 跟
  「主释义只留一个义项」的录词规矩一致，卡片上三行信息各归各位。
- 词性和备注**左右并排**（左格词性、右格备注；窄屏才退回上下）。
- 词表里手写了 `note` 就以手写的为准；没写就用释义括号里那截兜底。
- 词性来自同一个 `word_meta.json` 的 `pos`/`sub`（动词·一类 这种）。
- 两样都没有就显示 `—`，行还在，版式不塌。

### 7. Normalize Word Forms (词形规范化)

**Canonical word form: `かな(漢字)` — reading outside the parens, kanji inside.**
E.g. `おんがく(音楽)`, `すくない(少ない)`. Never the reverse (`音楽(おんがく)`).

**Trigger**: User reports word-form inconsistency, or after a bulk kanji-annotation import.

**⚠️ Do NOT write a script to rewrite the archive.** Use this command — it backs up
to `history/` before touching anything and aborts if the word count changes.

**Steps**:
1. **Always dry-run first**: `jrp --lang ja normalize-words --dry-run`
   - Read-only. Reports `change_count` and every `{old, new}` pair.
   - Review the list — verify no already-correct entry is being flipped and that
     compound forms (e.g. `万里の長城`, `少し/一寸`) look right.
2. Execute: `jrp --lang ja normalize-words`
   - Uploads the current archive to `history/<name>_backup_<timestamp>.md` **first**;
     aborts without touching the live archive if the backup fails.
   - Bumps the version (major bump when20+ entries change).
   - Hard-aborts before upload if the total word count differs from the original.
3. Verify idempotency: re-run `--dry-run`; `change_count` must be 0.
4. Re-export any affected Excel (`export-hard`, `gen-plan`) so the user sees the new forms.

**Swap rule** (`NormalizeWordForm`): only swaps when the orientation is
*unambiguously* reversed — outer segment contains kanji AND inner segment is pure kana.
Entries with no parens, no kanji, or already correct are left untouched, which makes
the command safely idempotent.

### 8. Deduplicate Words (去重)

**Trigger**: User reports untested words that never get reviewed; or discovers duplicate entries.

**⚠️ Do NOT manually delete lines from the archive.** Use this command — it backs up
to `history/` first and aborts if the word count doesn't match expectations.

**Steps**:
1. **Always dry-run first**: `jrp --lang ja dedupe --dry-run`
   - Read-only. Reports every duplicate group, which entry is kept (highest reviewCount), and which are removed.
2. Execute: `jrp --lang ja dedupe`
   - Uploads the current archive to `history/<name>_backup_<timestamp>.md` **first**.
   - For each duplicate group, keeps the entry with the highest reviewCount (tie-break: fewer errors).
   - Removes duplicate entries; if a group becomes empty, the entire group is dropped.
   - Hard-aborts before upload if the final word count ≠ (original − removals).
   - Minor version bump to the archive.
3. Verify the archive is clean: re-run `--dry-run`; `dup_groups` must be 0.

**Common cause**: `add-words` imports new words that already exist under a different group
with a different word form (e.g. `晴れ(はれ)` vs `はれ(晴れ)` before normalize). After
normalizing forms, these become exact duplicates with different reviewCounts.

### 9. Show Statistics

**⚠️ stats is the sole authoritative data source — NEVER parse the archive markdown
manually to build statistics or reports.** The command already downloads, parses, and
analyzes every archive; all statistical data must come from its JSON output.

**Trigger**: User asks for stats, learning progress, 详细数据, 统计 etc.

**Steps**:
1. Run: `jrp --lang ja stats --days <N>` (default 7; use 365 for all-time)
2. Parse the JSON output — it contains **everything**:
   - `snapshots[]` — daily breakdown (date, version, total, mastered, basic,
     needsConsol, untested, errors) ← for trend charts
   - `changes{}` — first→last deltas with `+N/-N` annotations ← for summary tables
   - `detail{}` — per-lesson distribution, accuracy buckets, hard-word breakdown,
     top-reviewed words ← for "what do I need to work on?"
   - `jlpt{}` — JLPT 等级进度（**仅 `--lang ja`**，其他语言是 `null`）：
     `vocab_total` / `vocab_firm`（牢固词数）/ `lesson`（已学到的课号，从分组名「第N课」正则取最大）
     / `n5{vocab,lesson}` / `n4{vocab,lesson}` 门槛。算的是**牢固词**（掌握+基础），
     不是总词数——所以进度条会低于「总词数 ÷ 门槛」的直觉值。CLI 与 `/api/stats` 都有这个字段。

   The `detail` section comes from the latest archive and includes:
   - `by_lesson[]` — word count per group/lesson
   - `accuracy_distribution{}` — word counts in 5 accuracy buckets (0-30%, 30-60%,
     60-80%, 80-90%, 90-100%)
   - `hard_words{}` — severe/moderate/mild/total counts (canonical
     `IsHardWord` rule)
   - `top_reviewed[]` — top 10 words by review count, each with accuracy
3. Present a readable summary: overview table (total/mastered/errors deltas),
   accuracy distribution (bar chart or list), per-lesson breakdown, top-reviewed
   words.

**What NOT to do**:
- Do NOT write Python/Node scripts to download and parse archive markdown files.
- Do NOT call `export-hard` to supplement stats — `stats --days` already includes
  hard-word counts in `detail.hard_words`.
- Do NOT combine `stats` output with ad-hoc archive parsing. If `stats` output
  is missing a field you need, add it to `buildStatsDetail` in `cmd_stats.go`.

### 10. Save Lesson Knowledge Document

**Trigger**: User sends textbook photos for knowledge extraction.

**⚠️ Core principle**: Do NOT copy-paste the textbook. The point is to **distill and reorganize** —
the textbook already exists on the user's desk. Your summary should be the "study guide" version
that makes patterns visible and traps avoidable.

**🎯 Theme-first structure** (MUST follow, highest priority):

Every lesson has ONE core theme. Identify it, state it upfront, and build the entire document around it.

1. **Identify the core theme**: Read the lesson and ask "what is the ONE thing this lesson is really
   teaching?" Not the title, not the topic — the skill. Examples:
   - 第9课 → 形容词谓语句 · 现在/过去 × 肯定/否定 四种活用
   - 第7课 → 动作的授受关系（あげる/もらう）
   - 第8课 → で的三种用法（工具/地点/方式）

2. **Theme as the backbone**: The theme should be stated in the document title line, and every
   subsequent section (课文拆解、语法解释、应用对话、副词、反义词) should explicitly **reference back**
   to the theme — showing how each piece of content serves or illustrates the core skill.

3. **Don't follow the textbook's TOC**: The textbook orders content as 基本课文 → 语法解释 → 表达讲解 →
   应用课文. That's for teaching. Your document is for **review**. Reorder content so the core skill
   comes first, and auxiliary knowledge (感叹词、读音注释) comes later.

4. **课文拆解 serves the theme**: Instead of listing 4 textbook sentences under a "基本课文" heading,
   group them by which form of the core pattern they demonstrate (e.g., a table: 现在肯定 / 现在否定 /
   过去肯定 / 过去否定, with the corresponding textbook sentence in each cell). This makes the
   structure of the lesson visible in a single glance.

**Style rules** (MUST follow):

1. **Grammar → comparison tables, not paragraphs.** When a grammar point has multiple forms
   (e.g. adjective conjugations, verb tenses), use a table with columns for form/rule/example.
   Always highlight the rule (e.g. "い→く＋ない") rather than just listing examples.

2. **Distinguish similar concepts.** If two things are easily confused (e.g. に vs で, あげる vs もらう,
   熱い vs 暑い), put them side by side with clear contrast notes.

3. **Mark error traps explicitly.** Use ⚠️ annotations for:
   - Common mistakes from Chinese L1 interference (e.g. "形容词修饰名词不加の")
   - Irregular forms (e.g. いい→よくない)
   - Special usage constraints (e.g. "あまり/全然 必须搭配否定")
   - Words that look like one category but are another (e.g. きれい is ナ形容詞 not イ形容詞)

4. **Pair antonyms.** When a lesson introduces adjectives or directional words in pairs,
   group them (e.g. 大きい↔小さい, 熱い↔冷たい, 高い↔低い/安い).

5. **Give mnemonic rules, not just descriptions.** For patterns, distill to one-line formulas:
   - "い→く做否定，い→かっ做过去" (adjective conjugation)
   - "存在に、动作で" (に vs で)

6. **Application dialogue → grammar breakdown, not transcript.** Don't just reproduce the dialogue.
   Annotate which grammar points each exchange demonstrates and why.

**Document structure** (theme-driven, NOT textbook-order):

```markdown
# 标准日本语初级上册 第N课 知识点

> **本课核心：[一句话说明这一课到底在教什么]**

## [核心技能的速查表]
（本课最核心的规则/活用表，放在最前面。比如形容词四种活用、で的三种用法。
这是整个文档的"索引"，后续所有内容都回指这个表。）

## 核心技能 → 课文拆解
（不是罗列课文句子，而是按核心技能的不同形态/用法分组，
标注每句课文对应哪个形态，做成对照表。）

## [核心技能的相关要点]
（易错点、不规则变化、搭配限制——只讲直接服务核心技能的内容。）

## [辅助知识点]
（程度副词、特殊词汇用法、反义词组等，按主题分组，用表格呈现。）

## 应用课文「标题」
（语法拆解，不是抄对话。标注每句里核心技能的具体体现。）

## 复习造句重点
（基于本课核心技能，列出最需要练习的句型/考点。）
```

**Steps**:
1. Read photos — extract lesson text, grammar points, example sentences
2. Reorganize following the style rules above — this is the key step
3. Save to local file
4. Run: `jrp --lang ja save-lesson --file /tmp/lesson.md --name 第N课知识点.md`
5. **课文句子进句库：不用单独敲命令** —— `save-lesson` 存的是汇编文档时会自动跑一遍：
   输出里的 `lessons_in_doc` 是文档里有哪几课，`auto_import.new_count` 是新加几句，
   `auto_import.per_lesson` 每课每份文档 `rows_in_doc / new_count / skipped`。
   ⚠️ 这步以前不在流程里，2026-10-16 才发现第19课的知识点 / 汇编 / 单词都在，
   **唯独句库没有第19课**，造句永远练不到。现在抽句逻辑：从两份「课本原文汇编」
   文档里按 `## 第N课` 段落抽表格里的句子，生词表和「待补」占位自动跳过，
   与已有句库按归一化去重，ID 连号（L19B001…/L19A001…）。
   想手动重跑/只看不写：`jrp --lang ja sentence-import --lesson N`（默认干跑，`--apply` 才写回）。
   写完照旧能过 `sentence-lint`。
   规则就两条，**不做覆盖**：① 句库已有一模一样的（归一化比，句号/空格/斜杠
   差异都算同一句）→ 跳过，所以重复导入不涨条目；② 其余一律追加。
   ⚠️ 别再加「很像就覆盖」的逻辑（2026-10-16 加了又被老师否掉）：
   「帰らなければ」和「帰らなくては」意思一样、句式不同，是两句真句子，
   相似度说得出"很像"、说不出"就是同一句"，自动覆盖会把真句子盖没。
6. **单词去重入库**：本课生词跟档案已有的对一遍，只补差异（`add-words`）
7. Run: `jrp --lang ja sentence-lint` — 校验新加的句子真是课本原文
8. Report: 文档已存 + **句库现在覆盖到第几课** + 新增多少词

> 想确认某一课有没有漏项：`jrp --lang ja lesson-status --lesson N`
> （一次性查四件套：知识点文档 / 汇编·基本课文 / 汇编·应用课文 / 单词 / 句库，
> 缺哪样列在 `missing`，`ready: true` 才算齐）。
> `jrp --lang ja lesson-status --all` 扫全部课，`not_ready` 直接给课号列表。
>
> ⚠️ **单元 ↔ 课的对应不固定**，别按"一单元 4 课"去推：第1单元 1-4、**第2单元只有 5-6**、
> **第3单元塞了 7-12**、第4单元 13-16、第5单元 17-20。代码里 `resolveLessonDocs`
> 是按文档内容（`## 第N课` 段落）定位的，不靠算术。

## 造句的语义判定（大模型，2026-09-29 起，默认关闭）

**为什么需要**：判分一直是**前端纯字面比对** —— `normSentence(我写的) === normSentence(原句)`，
归一化只折全角半角、删空白和标点。换说法、换词序、换同义词一律判错。
大模型只用来补这一块：**字面不一致时判断「意思成不成立」**。

**四条底线（改这里之前先读）**：

1. **字面一致永远直接判对**，一次模型都不调 —— 0 延迟、0 成本、不依赖外网。
2. **只判意思，不判语法**，但**助词从严**（2026-10-12 老师要求「不能太松」，已收紧）：
   - 汉字↔假名、全角半角、标点、空格、词序微调、同义词、敬体简体差异 → 算对。
   - **助词缺失判错**：该有的 を/が/に/へ/で 没写，「谁对谁做了什么」说不清 → 判错
     （练造句就是练助词，省了等于没练到）。**助词替换仍判对**（へ↔に 都成立），
     多加了不改变意思的助词也算对 —— 只留一半就走极端，两边都得在。
   - **感叹词/语气词/开场填充词（ええ/えっ/えっと/あの…）互相不同不算错**
     （2026-09-29 实测 えっと vs ええ 被判错过，已补规则+例句；唯一例外是肯定翻否定
     はい↔いいえ）；没学过的表达只要意思成立也算对。这几条不能删 ——
     删了模型就变成语法警察，会判一堆老师本来会的句子是错的。
   - **拿不准时按「关键要素齐不齐」判**，不是「拿不准就判对」也不是「拿不准就判错」：
     主体/动作/对象/时态/肯否齐全、只是写法不同 → 判对；要素缺失 → 判错。
     ⚠️ 别改成「拿不准就判错」—— 那样会把 へ↔に 这类同样成立的替换一起拖下水。
   `TestJudgePromptKeepsConservativeRules` 盯着内置模板不许删、不许只留一半。

   ⚠️ **2026-10-12 起整段模板外置到 COS**（老师要求随时改不用重新部署）：
   - 模板在 COS `configs/judge_prompt.txt`，占位符 `{answer}` `{chinese}` `{input}`；
     内置模板 `builtinJudgePrompt`（`api_judge.go`）只当兜底。
   - **四道护栏**（缺一不可，全在 `api_judge.go` / `judge_prompt_test.go`）：
     ① 模板必须含 `{answer}` 和 `{input}`，缺一个判坏回退内置；
     ② 渲染后没提 JSON 就自动补输出格式要求（否则模型自由发挥，判定全线失灵）；
     ③ COS 上没有/读失败/为空 → 用内置，练习绝不卡住；④ 结果缓存 60 秒，别每句打 COS。
   - **改 prompt 不用重启**：`jrp --lang ja judge-prompt --file x.txt`（上传前先校验，
     坏的直接中止不写）。另：`--builtin` 打内置、`--reset` 删回退内置、无参看当前。
     服务最多 60 秒后生效。
   - **模型自动切换（2026-10-15 老师要求，见 `llm_failover.go`）**：
     `JRP_LLM_MODEL` 主模型 + `JRP_LLM_FALLBACK` 备用（逗号分隔，去空去重、自动排除主模型）。
     - 什么算失败：网络错、非 2xx、空内容、超时、**模型回废话解析不出 JSON** —— 都换下一个。
     - 冷却：连续失败 2 次 → 2 分钟内跳过它（少了这条，每句都要干等超时）；共享状态在 `internal/llm/health.go`。
     - 全挂 → 维持原行为：返回 `by:"literal"` 退回字面判定，不返回 5xx。
     - 响应带 `model` 字段（实际判这句的是谁），排查「现在用的哪个模型」看它。
     - 改链条只改 env + restart，**不用编译**。
   - ⚠️ **模型会下线**：`GET /v1/models` 每个对象带 `status`，`pre-offline` 就是要下线
     （2026-10-15 发现 `deepseek-v4-flash-0731` 已是 pre-offline）。换模型前先查这个字段。
     现网：主 `deepseek/deepseek-flash`（原厂直供，name=DeepSeek-V4.1-Flash），
     备 `glm-5.3-flash`，单模型超时 6 秒。
   - **出题配额同样外置（2026-10-13）**：COS `configs/plan_config.json`，
     `sentence_base_count`（课本原句数，默认 20）+ `sentence_review_count`
     （回炉变形句数，默认 20），允许 0-50。读不到/解析坏/越界 → 内置默认 20+20，
     缓存 60 秒。
     第三个键 `sentence_review_circle_ratio`（2026-11-03，允许 0-100，默认 70）：
     回炉句里三圆词的名额占比，**写 0 等于关掉圆级优先**（回退到纯「最久没出过」）。
     ⚠️ 它是指针字段 —— `nil` = 没配过（用内置 70），`0` = 有意关掉，两者语义不同，
     所以别用 `== 0` 判断「没配」（比结构体时用 `planCircleRatio()` 取生效值）。
     **改配额不用编译、不用部署**：`jrp --lang ja plan-config --set sentence_review_count=24`
     （多个用逗号分隔），`--reset` 删回退内置，无参看当前生效值与来源。
     三处引用（网页出题 `api_plan.go`、离线 Excel `api_offline.go`、预检
     `cmd_sentence.go`）都读同一份配置，改一次三处同步。
     ⚠️ CLI 里直读 COS 不走缓存 —— 否则刚 `--set` 完 `--show` 会看到旧值。
   - ⚠️ **改了 prompt 不作废旧结论**（2026-10-12 老师定的）：版本＝模板 sha256 前 12 位，
     随结论存草稿（`judge.pv`）**只作记录**。**草稿按天隔离**（`DownloadDraft` 只恢复
     当天的），恢复永远发生在同一天内 —— 若按版本作废旧结论，当天改一次 prompt 就会把
     老师已经判过的句子全部打回重判，看着像「白练了」。
     老师的口径：**当天已判的维持原样，新规则从第二天（新一天的草稿）自然生效。**
     服务端判定缓存键仍带版本（同一进程内 prompt 改了不会复用旧判定），这条只是
     前端恢复草稿时不再拿版本当作废依据。
3. **模型不可用（没配密钥 / 超时 / 报错 / 返回 `by:"literal"`）就退回字面判定**，
   练习绝不卡住，也不许把字面结论标成模型结论。
4. **判定不漂移**：结论按 (原句, 写的句子) 缓存在进程里，并随草稿存 COS ——
   刷新、换设备回来必须是同一个结论。改过字则作废重判。
   草稿里的结论带**规则版本号**（`DraftJudge.V`，当前 2）：prompt 的松紧改了，
   旧版本结论恢复时一律作废，重新问模型（v1 没有版本号，全部作废）。

**开关与配置**（服务端环境变量，**密钥只在服务器，前端永远拿不到**）：

```bash
JRP_LLM_ENDPOINT=https://tokenhub.tencentmaas.com/v1/chat/completions  # OpenAI 兼容的 chat/completions
JRP_LLM_API_KEY=sk-...
JRP_LLM_MODEL=deepseek-v4-flash-0731   # 可选
JRP_LLM_TIMEOUT=8                      # 可选，秒，上限 60
```

任一为空 → `/api/judge-sentence` 返回 `{"enabled": false}`，前端关掉开关，
造句跟以前一模一样。**没配密钥时部署上去是安全的**。

**实际在用的模型（2026-09-29 老师给的）**：腾讯 TokenHub 上的
`deepseek-v4-flash-0731`，endpoint `https://tokenhub.tencentmaas.com/v1/chat/completions`，
`Authorization: Bearer <key>` —— 标准 OpenAI 兼容格式，`internal/llm.Call` 直接支持，
不用改代码，只填环境变量。

**⚠️ 请求体必须带 `"stream": false`**（`internal/llm/client.go` 的私有 `buildRequest`，有单测盯着）：
不写这条，tokenhub 这类网关可能按 SSE 流式返回，`choices[0].message.content`
会拿不到东西，判定全线失败。

**密钥别在对话里传**：聊天层会脱敏/改写 token 样式的字符串，我这边拿到的
可能已经是另一个值（2026-09-29 实测拿到的是无效 key，401。同样的坑在 GitHub
PAT 上犯过两次）。**要配就由老师直接写进服务器 `/etc/jrp/jrp.env`**（权限 600），
或者放到本地一个文件里告诉我路径，我读文件，不走对话。

**老师的取舍（2026-09-29 明确选的）**：模型判「意思成立」但字面不同的句子
**算过**，不进错句本；界面上写明「模型判定，与原句写法不同」并提示
「还是没把握就点『不会』」—— 真没掌握的由老师自己勾，不会漏。

**测试**：后端 `TestParseJudgeReply`（含 ```json 围栏、前后废话、缺 correct 报错）
+ `TestLoadLLMConfig*`（没配就不启用）+ `TestJudgeCacheKeyFoldsWidth`
+ `TestJudgePromptKeepsConservativeRules`（prompt 规则不许被删）；
前端 `outputs/sentence_judge_test.js` 21 项（含「写对了不调模型」「关掉后不再问」
「模型挂了不冒充」「改字后旧结论作废」）。

### 字幕精校回调（大模型第四用途，2026-11-06 起，默认关闭）

老师在 COS 桶里搭了数据万象工作流：上传视频 → 转封装 + ASR 生成 SRT → 回调 jrp。
服务端拉同名 `.srt`，调大模型**保守精校**，直接覆盖回 COS。

- **接口**：`POST /api/listening/refine`，body 是数据万象的 XML（`Response/JobsDetail`）。
  从 `Operation.Output.Object` 拿 **.srt 完整 key**（老师确认回调里给的就是它）；
  兜底按 `Input.Object` 的视频 key 换扩展名。
- ⛔ **不走 withUser**（工作流没有用户会话）。它**能写 COS = 后门**，鉴权用独立密钥
  `JRP_REFINE_TOKEN`：回调请求带 `Authorization: Bearer <JRP_REFINE_TOKEN>` 才放行，
  常量时间比较。没配这个密钥 → 403，精校不启用（部署上去是安全的）。
- **方案 A（纯文本、不给视频）**：只修「语言层能确定」的错（同音词/搭配/固定指令句/番号），
  数字/专名/选择题选项这类必须听音频才知道的**保持原样**——宁可漏改不改坏。
  这是与老师对齐的口径（2026-11-06），纯文本模型解决不了「八番 vs 一番」这种。
- **纯函数层** `internal/listening/refine.go`：`RefinePrompt`（内置兜底，保守规则有单测盯）、
  `BuildRefineInput`（带编号、**不带时间轴**喂模型）、`ParseRefineReply`（只认「编号: 文本」，
  忽略代码块/解释/「无」）、`ApplyRefinements`（只替换点名的编号，越界忽略，时间轴不动）、
  `RenderSRT`（毫秒**四舍五入**不能截断，否则 round-trip 偏 1ms）。40 句一批防超 token。
- **幂等**：精校完落一个 `<srt>.refined` 标记对象，工作流重试回调直接跳过，不重复烧钱。
- **只处理 `State=Success`** 的回调；失败/进行中的确认收到、不做事。
- **配置**（`/etc/jrp/jrp.env`）：`JRP_REFINE_TOKEN`（回调密钥，必配才启用）；
  模型默认走现网文本链（`JRP_LLM_MODEL`+`FALLBACK`），可用 `JRP_REFINE_MODEL`/
  `JRP_REFINE_FALLBACK`/`JRP_REFINE_TIMEOUT`（默认 60s，精校比判句慢）覆盖。
- **测试**：`internal/listening/refine_test.go`（金标准取自 2026-11-06 手工精校的那份：
  懲戒→聴解、問題要旨→問題用紙、泡→青いの；含全角编号/冒号、噪声过滤、越界编号、
  round-trip、时间码边界）。

## Critical Rules

1. **Never manually edit archive markdown** — always use the Go CLI for archive operations
2. **Always use absolute paths** for temp files (e.g., `/tmp/words.json`, not `words.json`)
3. **The Go binary handles versioning automatically** — do not calculate version numbers manually
4. **The Go binary handles COS upload automatically** — do not manually upload archives
5. **IMA is read-only** — never attempt to write to IMA
6. **Sentence generation is the AI's job** — the Go binary does not generate sentences
7. **Photo recognition is the AI's job** — the Go binary does not process images
8. **All commands output JSON to stdout** — parse the JSON for results
9. **Output files must go to workspace `outputs/` directory** — not `/tmp/`. Copy the final xlsx to `outputs/` before present_files, otherwise the mini-program notification won't fire.
10. **Excel output naming**: `review_yyyy-mm-dd_vA.B.xlsx` — gen-plan auto-initializes today's v1.0 archive if none exists for the target date; otherwise version is parsed from the current archive filename
11. **钉子户 is an accuracy threshold, not an error count** — use `export-hard` (or `IsHardWord`) rather than filtering on `ErrorCount >= N` by hand. Never reimplement this rule in an ad-hoc script.
12. **Run the CLI from the workspace root** so that the default relative `outputs/` path lands in the workspace.
13. **⚠️ NEVER test write-commands against the user's real archive.** `DownloadLatestArchive`
    picks the archive with the newest **COS lastModified** — *not* the highest filename version.
    A stray test archive therefore hijacks every subsequent `add-words` / `record`, which can
    produce a new archive whose version number is *lower* than the real one and silently
    overwrite it. For experiments use read-only commands (`export-hard`, `stats`,
    `list-knowledge`) with `--output` pointed at a temp path, or switch to an empty language
    (`--lang en`).
14. **Before `add-words` / `record`, confirm which archive will be picked up** — check the
    `old_filename` field in the JSON output and verify it is the expected latest version. If it
    names an unexpected file, stop and clean up the stray archive first.
15. **⚠️ After `record`, NEVER re-run `gen-plan` to look up word mappings.** `record` changes
    word error counts in the archive, which causes `gen-plan` to produce **different numbering**
    than the original plan the user reviewed against. When the user asks "what words were at
    numbers 23, 42, 66...", read the **original Excel** (workspace `outputs/`) or the
    **original plan JSON** that was stored *before* `record` ran. Re-running `gen-plan` also
    **overwrites** the COS plan JSON, destroying the only canonical record of the user's actual
    review plan.
16. **⚠️ `stats` is the ONLY stats source.** Do NOT write Python/Node scripts to download and
    parse archive markdown for statistical data. The `stats` command already downloads and
    analyzes every archive internally and includes `detail{}` (per-lesson distribution,
    accuracy buckets, hard-word counts, top-reviewed words). If a needed stat is missing,
    add it to `buildStatsDetail()` in `cmd_stats.go` — do not work around it with scripts.
17. **Canonical word form is `かな(漢字)`** — reading outside the parentheses, kanji inside
    (e.g. `おんがく(音楽)`, not `音楽(おんがく)`). When importing words via `add-words`, write
    them in this form. To fix existing entries use `normalize-words --dry-run` then
    `normalize-words` — never a hand-rolled script (a reversed script is exactly what
    created the 42broken entries on 2026-08-05).
18. **⚠️ Any bulk archive rewrite MUST back up to `history/` before uploading and MUST
    verify the word count is unchanged.** `normalize-words` does both. If you ever need a
    new bulk-edit command, copy that pattern: upload backup → mutate → assert count →
    upload. Never mutate-then-backup.
19. **⚠️ Sentence grammar MUST stay within learned scope.** Before writing any sentence for
    `gen-plan`, read ALL COS knowledge documents and extract the learned grammar points.
    Sentences that use unlearned grammar (て形, た形, から, ～にくい, plain form, etc.) are
    BANNED. The knowledge docs are the single source of truth for what the user has learned.
    This rule directly addresses the 2026-08-07 incident where 3 of 15 sentences used て形
    (unlearned) and 1 used ～にくい (unlearned). Contrastive は is NOT a trap — it was taught
    in Lesson 5.
20. **⚠️ Duplicate word entries MUST be cleaned with `dedupe`, never by hand.** Duplicate
    entries (same word text in different groups) are caused by `add-words` importing words
    whose form differs from the existing entry (e.g. `晴れ(はれ)` vs `はれ(晴れ)` before
    normalize). After `normalize-words` unifies the forms, run `dedupe --dry-run` to
    check, then `dedupe` to clean. Never delete lines manually — that corrupts the
    word count and `dedupe` already handles backup + assertion.
    **⚠️ `dedupe` only matches byte-identical word text.** A bare-kana entry and its
    kanji-annotated twin (`すし` vs `すし(寿司)`) are NOT caught — they survive forever and
    split one word's review history into two rows, so accuracy is computed against a partial
    history and the total word count is inflated. For that case use `merge-words`:
    `jrp --lang ja merge-words --input merges.json --dry-run`
    with `{"language":"ja","merges":[{"from":"すし","into":"すし(寿司)"}]}`.
    Merge rules: ReviewCount + ErrorCount are **summed** (both rows are real history),
    ConsecutiveCorrect takes the **max** (summing would invent a streak that never happened),
    LastReview takes the later MM/DD, Status is recomputed. Target keeps its group; the source
    row is deleted. Backs up to `history/` first.
21. **⚠️ 造句必须是课本原文 (verbatim textbook lines).** NEVER compose your own sentences.
    Select 20 lines verbatim from the knowledge docs' 基本课文 / 应用课文 / 语法例句 tables;
    the Chinese prompt is the doc's own translation. This replaced the old "generate from
    scratch" rule at the user's explicit request (2026-08-27). Rationale: textbook lines are
    guaranteed correct and inside learned scope, so the old risk of AI-invented sentences
    leaking unlearned grammar/vocabulary disappears entirely.
    **Selection priority**: (1) `课本原文汇编_第N单元（应用课文）.md` if present — it has
    the richest完整对话 sentences; (2) `课本原文汇编_第N单元（基本课文）.md` if present
    (4 句基本课文 + 4 组短对话 for each lesson); (3) each lesson's
    `标准日本语初级上册_第N课知识点.md` 基本课文 tables; (4) 语法例句 tables. Skip any
    "（待补 — 用户未提供...）" placeholder sections. When selecting sentences for
    `gen-plan`, use ONLY: (a) the due-word list from Step 2, (b) knowledge docs from
    Step 3, and (c) nothing else.
22. **⚠️ 每个版本必须在 changelog 描述里写清"当天到底发生了什么"。** The changelog's
    `描述` column is the ONLY timeline that can reconstruct history. Every operation that
    bumps a version (`record` / `add-words` / `update-def` / `update-word` /
    `normalize-words` / `dedupe`) must leave a description that **independently answers**:
    这个版本相对上一版改了哪些词、复习结果是多少（到期N词 / 实写M词 / 写对X / 写错Y /
    未写Z）、改了哪个词形或释义、导入/删除了多少词。绝不能只写"复习结果：0词写对 N词写错"
    这种只反映「提交了什么」、不反映「当天实况」的描述。2026-08-22 事故的根因正是
    `record` 只提交错词 → changelog 统计列（已掌握/待巩固/错误数/钉子户）持续失真 →
    正确率冻结 + 8/6 一天无法追溯。记录必须在**操作当下**写清，事后补回必然残缺。
22. **⚠️ COS 凭证放在 `~/.workbuddy/cos-credentials/`** — 不带任何 skill dir 的 jrp 命令一律写
    `~/.workbuddy/cos-credentials/`（迁移首选）→ `~/.workbuddy/skills/tencentcloud-cos/`（旧位置回退）。
    解析在代码里做，不用设 `JRP_COS_SKILL_DIR`，也不依赖 shell 是否加载了 `.zshrc`。
    NEVER place credential files inside any `~/.workbuddy/skills/<marketplace-skill>/`
    directory: marketplace skill updates wholesale-replace those directories and wipe
    local files (this destroyed `.env.enc` on 2026-09-10).
23. **⚠️ 报「练完了但页面还是显示没做」时的排查顺序 —— 先拿证据，别猜。**
    ① `nginx access.log` 找当天 `POST /api/record` 的时间和响应码。COS 对象的
       `LastModified` 是 **UTC，`+8` 才是北京时间**，跟 access.log 一一对上就能锁定
       「哪个请求写了哪个文件」，也能一眼看出提交是不是发生在部署之前。
    ② 读 `reviews/review_<date>_<mode>.json` 的 `answer` / `blank` 分布：
       `answer` 有值但 `blank=true` ⇒ **填了答案但没判分就提交了**；`answer` 空 ⇒
       真没填。这一步直接分开「没做」和「做了没提交」。
    ③ 档案 changelog 那行「N词写对 M词写错」才是提交内容的真相。
    ⚠️ `/api/record` 返回 **200 也可能一个词都没写**（题号映射不上时 `notFound`
    照样 200），别拿 200 当成功。
    历史坑（2026-10-12，已修）：列表模式回写按钮只看 `collect()`，而 `collect()`
    只收 `correct !== null` 的行 —— 填了一屏没点「对答案」时按钮只靠几个「不会」
    冒出来，点下去只提交「不会」，72 行答案全丢。修法见 `web/list.js` 的
    `refreshCommitBtn()` + `commit()` 里的自动 `gradeAll()`。
    另一半症状不是 bug：造句**全交完才换题**，下次刷新出全新一批空白 = 看着像
    「没做」，数据其实在 `sentence_history.json` / `sentence_wrong.json` 里。

## Windows Environment Notes

- **Build with PowerShell, not Git Bash.** `go build -o <path-under-home>` run from Git Bash
  exits 0 but silently writes nothing (sandbox path redirection). Use:
  ```powershell
  $env:PATH = "$env:USERPROFILE\go-sdk\go\bin;$env:PATH"
  cd C:\Users\efrainzhang\jrp-src
  go build -o C:\Users\efrainzhang\.workbuddy\skills\jrp\jrp.exe .
  ```
- **`rm` may fail under the home directory**: the safe-delete layer mangles Git Bash paths
  (`/c/Users/...` → `\c\Users\...`). Fall back to PowerShell
  `[System.IO.File]::Delete($absolutePath)`.
- **After editing `jrp-src\SKILL.md`, copy it to `.workbuddy\skills\jrp\SKILL.md`** — the two
  must stay in sync.
- **Git Bash 的 PATH 会间歇性被清空**（表现为 `dirname: command not found`、
  `grep: command not found`，2026-09-20 遇到过）。命令前补一行即可：
  `export PATH="/c/Users/efrainzhang/go-sdk/go/bin:/usr/bin:/bin:$PATH"`。
  PowerShell 的 stdout 有时也不回显，验证结果用 bash 的 `ls`/`head` 更可靠。
- **凭证迁移已完成（2026-09-20）**：`C:\Users\efrainzhang\.workbuddy\cos-credentials\`
  已建（`.env` + `.env.enc`），符号链接
  `tencentcloud-cos\.env -> cos-credentials\.env` 已建并验证 `cos_node.mjs` 可读。
  建链接用 PowerShell：`New-Item -ItemType SymbolicLink -Path <link> -Target <target>`
  （本次成功；失败就退化为直接复制 `.env`）。旧位置的 `.env.enc` 保留作回退。
- **本机源码目录** `C:\Users\efrainzhang\jrp-src`（macOS 那边是 `~/jrp`，别混）。

## macOS Environment Notes

- **Homebrew 不可用**：本机 brew 的 go 依赖 portable-ruby 且已损坏（ruby 版本冲突），
  `brew install go` 会失败。改用 go.dev 官方预编译 tarball（Apple Silicon 用 darwin-arm64）：
  ```bash
  curl -o /tmp/go.tar.gz https://go.dev/dl/go1.27.1.darwin-arm64.tar.gz
  mkdir -p ~/.workbuddy/binaries && tar -xzf /tmp/go.tar.gz -C ~/.workbuddy/binaries/
  ```
  Go 二进制位于 `~/.workbuddy/binaries/go/bin/go`。
- **GOPROXY 用国内镜像**：`proxy.golang.org` 被墙（502/超时），依赖下载改用
  `GOPROXY=https://goproxy.cn,direct`。
- **编译命令**：
  ```bash
  export PATH="$HOME/.workbuddy/binaries/go/bin:$PATH"
  export GOPROXY="https://goproxy.cn,direct"
  cd ~/jrp
  go build -o ~/.workbuddy/skills/jrp/bin/jrp .
  ```
- **git clone 可能只生成半个 `.git` 就断**（走 `http://127.0.0.1:7897` 代理时），但
  `git fetch --depth 1` 正常。首次获取源码的可靠方式：codeload tarball
  （`https://codeload.github.com/zhangyf/jrp/tar.gz/refs/heads/main`），或 `git init` +
  `git remote add origin https://github.com/zhangyf/jrp.git` + `git fetch --depth 1 origin main`
  +   `git reset --hard origin/main`。
- **`git pull` 报 `LibreSSL SSL_connect: SSL_ERROR_SYSCALL` 时，别反复重试网络**：本机代理
  不稳，`pull` 会挂但 `git fetch origin` 通常能成。改成「先 fetch、再用本地引用快进」：
  ```bash
  git fetch origin && git merge --ff-only origin/main
  ```
- **merge 被 untracked 同名文件挡住**（`The following untracked working tree files would be
  overwritten by merge`）：`git fetch` 之后工作区里可能已经出现了和远端同名的新文件。
  **不要直接删** —— 先备份再逐字节比对，一致才删：
  ```bash
  cp api_judge.go /tmp/jrp_backup/ && git show origin/main:api_judge.go | diff api_judge.go - && rm api_judge.go
  ```
  比对不一致说明本地有独立改动，必须先问老师怎么处理。
- **⚠️ 反复 `merge` 会陷入「删了又回来」的死循环**（2026-10-08 踩到）：失败的
  `git merge --ff-only` 自己会在工作区留下同名文件——删掉再 merge，它又冒出来，
  而且第二次报的可能**不是同一批**（删 `cmd_stats_test.go` 之后变成
  `api_record_test.go` + 两份 `knowledge/…第5单元….md`，删完再试前者又回来了）。
  **出路是改用 `git reset --hard origin/main`**：它和 ff 合并等价，且不会被 untracked 文件挡住。
  前提是**工作区其它内容本来就干净**（`git status --short` 只有这些 ?? 文件），
  且这些 ?? 文件已逐字节比对确认与远端一致——满足这两条才用，否则会丢本地改动。
- **macOS 自带 `grep` 不支持 `\|` 基本正则**（2026-09-29、2026-10-08 各踩一次）：
  `grep -rn "jlpt\|JLPT" --include="*.go" .` 返回空，会误判成「源码里根本没写这个功能」。
  改用 `grep -E`、或直接用 Grep 工具（ripgrep 支持）。
- **After editing `~/jrp/SKILL.md`, copy it to `~/.workbuddy/skills/jrp/SKILL.md`** — the two
  must stay in sync（与 Windows 同理）。
- **⚠️ 拉到 2026-09-20 之后的构建，第一次跑之前先重加密**（Windows 那边改了密钥种子的
  路径规范化，所有旧 `.env.enc` 失效）：
  ```bash
  cd ~/.workbuddy/cos-credentials && $JRP_BIN encrypt-env
  ```
  若报 `cannot read .env.enc` 说明本机还没迁移过——先把明文 `.env` 放到
  `~/.workbuddy/cos-credentials/.env` 再执行。
- **本机源码目录** `~/jrp`（Windows 那边是 `C:\Users\efrainzhang\jrp-src`，别混）。

## Server Deployment（腾讯云）

老师日常用的是**服务器上的 serve**，不是本机页。改完代码必须双端部署。

| 项 | 值 |
|---|---|
| 机器 | `root@154.8.216.179`（已配免密 SSH 公钥，直接 ssh 即可） |
| 对外地址 | `https://jrp.110105.xyz`（nginx TLS + Basic Auth） |
| 证书 | `/etc/nginx/ssl/tencent/jrp_bundle.crt` + `jrp.key` |
| 密码文件 | `/etc/nginx/.jrp_htpasswd` |
| 反代 | `/etc/nginx/nginx.conf` 里的内联 server 块，`proxy_pass http://127.0.0.1:8080`（**没有** sites-enabled） |
| 服务 | systemd `jrp.service`，`ExecStart=/usr/local/bin/jrp --lang ja serve --addr 127.0.0.1 --port 8080` |
| 二进制 | `/usr/local/bin/jrp`（老师访问的就是这个文件） |

### 部署流程（固化）

```bash
# 1) 本机交叉编译。CGO_ENABLED=0 必须——默认开 CGO 编出来的二进制依赖本机 glibc，服务器跑不起来
cd ~/jrp
export PATH="$HOME/.workbuddy/binaries/go/bin:$PATH"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/jrp-linux-amd64 .

# 2) 上传
scp /tmp/jrp-linux-amd64 root@154.8.216.179:/tmp/jrp-new

# 3) 停服 → 覆盖 → 启动（顺序不能反，见坑 1）
ssh root@154.8.216.179 '
  cp /usr/local/bin/jrp /usr/local/bin/jrp.bak_$(date +%Y%m%d_%H%M) 2>/dev/null || true
  systemctl stop jrp
  cp /tmp/jrp-new /usr/local/bin/jrp && chmod +x /usr/local/bin/jrp && rm -f /tmp/jrp-new
  systemctl start jrp; sleep 2; systemctl is-active jrp
'

# 4) 验证——不能只看服务活着，要验「内嵌资源真的换了」
ssh root@154.8.216.179 'strings /usr/local/bin/jrp | grep -c conj-input;
  curl -s http://127.0.0.1:8080/ | grep -o "page-conj";
  curl -s "http://127.0.0.1:8080/api/conjugation?type=verb&count=1"'
```

本机同时也要更新（命令行工具走的是它）：
`cp /tmp/jrp-mac ~/.workbuddy/skills/jrp/bin/jrp`（先备份成 `jrp.bak_<日期>`）。

### 踩过的坑

1. **`Text file busy`** —— 服务在跑时直接 cp 覆盖二进制必失败；**先 `systemctl stop`**。
2. **只更了中间产物忘了 `/usr/local/bin/jrp`** —— 老师访问的是这个路径，覆盖别处的副本不生效。
3. **两端要分别构建** —— Mac 版（`~/.workbuddy/skills/jrp/bin/jrp`）和 Linux 版是两个文件，
   容易只更一边。前端是 `go:embed` 打进去的，**改了任何 `web/` 下的文件两端都得重编**。
4. **`systemctl is-active` 不能证明部署成功** —— 服务活着但跑旧二进制，
   现象就是老师说的「刷新了网页还是老样子」。用 `strings` 查内嵌资源或直接 curl 接口。
5. **本机自测用另一个端口**（如 `--port 8099`，`--addr 127.0.0.1` 无鉴权），测完 `pkill`，
   别和本机常驻的 8080 打架。
6. **ssh/scp 输出里会夹腾讯云扫码横幅**，取数据时 `2>/dev/null` 过滤，别当成错误。
7. **⚠️ 判断「服务器是不是最新」别只看日期/大小 —— 用 ASCII 特征串比对**
   （2026-10-08 踩到）：服务器上那份二进制 mtime 是当天 10:11，看着像"已经最新"，
   但只靠日期会误判、只看大小更会被骗（本地加 `-s -w` 是 23 MB，服务器不加是 31 MB，
   差 8 MB 纯属编译参数差异，不代表版本不同）。可靠办法是从**最近几个提交**里挑独有的
   **ASCII 标识符**（前端函数名 / JSON 键名，如 `pruneDraft`、`cardReviewItems`、`jlpt`），
   两边各跑一次 `strings <二进制> | grep -c <标识符>` 对比计数：
   ```bash
   for k in page-conj conj-input pruneDraft cardReviewItems jlpt word_note; do
     printf "%-18s %s\n" "$k" "$(strings /usr/local/bin/jrp | grep -c "$k")"; done
   ```
   **别用中文字符串做特征** —— `strings` 默认不认多字节，CJK 一律返回 0，会误判成"没有"。
8. **只改文档（SKILL.md / knowledge/*.md）的提交不需要重新部署** —— 二进制里没有它们，
   部署只跟 `*.go` 和 `web/**` 有关。
9. **⚠️ `Permission denied (publickey)` 不等于「这台机器没配过密钥」**（2026-10-09 踩到）：
   Windows 的 `id_rsa`、`id_ed25519` 私钥都是有效的（`ssh -vv` 里能看到
   `Offering public key ... we sent a publickey packet`，说明签名发出去了），
   但服务器拒收 —— 是**服务器 root 的 `authorized_keys` 里没有这两把钥匙**，
   不是本机缺密钥。排查顺序：
   - 先确认服务器没被重装：`ssh-keygen -l -f ~/.ssh/known_hosts | grep <IP>`
     和本次连接的 `Server host key` 指纹比对，一致就说明还是同一台。
   - 别信腾讯云那个「微信扫码安全登录」横幅 —— **免密成功时它也会出现**，不是失败信号。
   - 腾讯云 API 那条路也走不通：本机 `~/.tccli/default.credential` 是 oauth 临时凭证，
     几个月前就过期了。
   - 出路：**让老师交互式登录一次**（密码或扫码），顺便把本机公钥追加回
     `authorized_keys`，免密就恢复了。现成脚本：`outputs/deploy_jrp.sh`（上传+写公钥+部署+验证一条龙）。

## Language Codes

| Code | Language | Archive Prefix | IMA Knowledge Base |
|---|---|---|---|
| ja | 日语 | 日语学习进度档案 | 自学日语 (7452509467574409) |
| en | 英语 | 英语学习进度档案 | 英文知识库 |
| fr | 法语 | 法语学习进度档案 | (to be created) |

## Binary Path

```bash
# Windows
JRP_BIN=~/.workbuddy/skills/jrp/jrp.exe
# macOS
JRP_BIN=~/.workbuddy/skills/jrp/bin/jrp
```

All commands: `$JRP_BIN --lang <ja|en|fr> <command> [flags]`

## GitHub

- Repo: https://github.com/zhangyf/jrp (public)
- **写操作走命令行 + PAT**：token 存在 `~/.workbuddy/cos-credentials/github-token`，
  git 已配 `credential.helper store`（`~/.git-credentials`，仅对 `github.com` 生效），
  `git push` 直接可用（2026-09-28 实测通过）。**绝不要把 token 内容写进仓库、回复或日志**。
- WorkBuddy 的 GitHub connector 是只读集成，创建/推送一律用 CLI。
- 偶发 `unable to get credential storage lock in 1000 ms: File exists` 是
  `~/.git-credentials.lock` 残留，`rm -f ~/.git-credentials.lock` 即可，不影响推送结果。

## Environment

The Go binary needs `PATH` to include the Go SDK for toolchain auto-download:
```
# Windows
PATH=$HOME/go-sdk/go/bin:$PATH
# macOS
PATH=$HOME/.workbuddy/binaries/go/bin:$PATH
```

Set this before running jrp commands if the binary was compiled with a newer Go toolchain.

Only set `JRP_COS_SKILL_DIR` when credentials live somewhere non-standard — the default
resolution (cos-credentials → legacy skill dir) covers both machines.

## Source Code

GitHub: https://github.com/zhangyf/jrp (public)

Local source: clone the repo to your preferred working directory (e.g., `~/jrp/`)

Go module: `github.com/zhangyf/jrp`
Dependencies: `github.com/xuri/excelize/v2`, `github.com/zhangyf/objstore`

### 首批功能包边界（2026-11-03）

- `internal/conjugation/rules.go`：纯活用规则，公开 `Verb` / `VerbNai` / `AdjI` / `AdjNa` / `VerbClass` / `AdjClass` / `SkipWord`；测试在同目录 `rules_test.go`。
- `internal/knowledge/markdown.go`：知识点 `Span` / `Item` / `Block` 类型，以及 `ParseMarkdown` / `Title` / `VisibleBlockCount`；纯解析测试在同目录 `markdown_test.go`。
- 两包只依赖标准库，不依赖 HTTP、Storage、session 或 COS；规则只保留一份实现。
- `api_conjugation.go` 保留 DTO、取词、抽题和 HTTP 入口；`api_knowledge.go` 保留 HTTP、鉴权和兼容类型别名。知识点 HTTP/权限测试仍在 `api_knowledge_test.go`，不迁移根包的内存存储夹具。
- 根目录仍是 `package main`，继续从根目录 `go build .`；`serve.go` 保留 `//go:embed web`，前端和部署方式不变。
- 目录重构不等于上线。后续模块仍须逐批确认范围，部署单独确认；不迁移或回滚学习数据。

### 单词复习核心包边界（2026-11-03）

- `internal/review/types.go` 定义 `Word`、`WordGroup`；根 `types.go` 只保留类型别名，不创建新类型，不做转换。字段、顺序和序列化形态保持不变；`Archive`、`ReviewPlan`、`PlanWord`、回写输入及统计 DTO 仍在根包。
- `internal/review/ebbinghaus.go` 是原复习规则的唯一实现，公开17个函数及 `DefaultHardMinAccuracy` / `DefaultHardMinReviews`。只依赖标准库 `fmt`、`time`，不读写存储、不访问网络；根调用方使用 `review.X`，不保留函数包装或第二份规则。
- `ebbinghaus_test.go` 的18个测试、214个子用例原样随包迁移。保护间隔、状态阈值、跨年日期、答对答错更新、分组顺序、`FindWord` 返回原对象指针和 `AllWords` 值拷贝；单包验证：`go test ./internal/review`。
- `CountByStatus` 按已存状态和原优先级计数，不能替换成 `DetermineStatus`；`CountNailHouseholds` 是历史统计口径，不能统一为当前 `IsHardWord`。既有异常输入兼容行为保留，纯迁包不顺手修规则。
- 根 `archive_test.go` 验证类型别名/JSON形态及七列档案往返；`api_record_test.go` 用内存存储验证每日/钉子户计划编号映射、回写日期、未提交词不变及历史统计，不触真实COS。
- 本批仅调整包归属和调用引用；HTTP/CLI流程、档案/计划格式、鉴权、COS key、`go:embed web` 和部署方式不变。无新增依赖或数据迁移；提交推送、合并、部署继续分别确认。

### 离线练习册解析侧包边界（2026-11-03）

- 只迁解析侧：`internal/offline/offline.go` 承接 `ExcelMeta`、`ImportWord`、`ImportSentence`、`ImportPreview`、三个 sheet 名常量（`MetaSheetName`/`ExerciseSheetName`/`AnswerSheetName`）、`ParseExerciseFile` 及 `col`/`firstNonEmpty`/`sortedKeys`/`dateFromFilename` 与三个正则。判分仍走 `internal/grading`，归一化仍走 `internal/sentence`。
- **渲染侧留在根包（本批结论；2026-11-04 后续批次已推翻）**：`excelWriter` 全套、四个 `Generate*`、`writeTwoSheetWorkbook`、`groupPlanWordsByStatus`、`reviewCategories`、`defaultExcelLayout`、以及 `(w *excelWriter) writeMetaSheet`（当时判断 receiver 不能搬，实际把整个 writer 类型连同方法一起搬即可）。原因是渲染侧吃 `ReviewPlan`/`PlanWord`（COS 契约类型），且 `hardCategories` 反向定义在 cmd 层，整体搬会牵动计划类型与多个 handler，风险大于收益。⇒ 上批把两个类型搬进 `internal/offline` 后本约束即解除，见「Excel 渲染侧包边界（2026-11-04）」。
- `api_offline.go` 的 HTTP 下载与 Storage、cmd 两处入口只改符号引用；离线文件结构与 `_meta` 内容不变。
- 测试：`internal/offline/offline_test.go` 覆盖解析各分支与四档判分（11 函数 / 103 子用例）；根包新增 `excel_test.go` 补原先零覆盖的钉子户导出、nil/空句子、左右分栏、句子区块与 `_meta` 可见性/活动表复位（5 函数 / 12 子用例）；原有 `excel_meta_test.go` 往返用例留根包不动。（2026-11-04 后续批次已随渲染侧全部迁入 `internal/offline`，见下节。）
- 按实现保留的行为（测试已固定）：`GenerateHardExcelWithMeta` 内部写死传 nil 句子；hard 过滤是 `Status` 精确匹配三个常量；分栏为前 `(n+1)/2` 个在左栏；`ExcelMeta.Kind` 写入 `_meta` 但 `ImportPreview` 不读回；`dateFromFilename` 会扫整条路径且取最左日期。

### 汇编导入包边界（2026-11-03）

- `internal/lessonimport/parse.go` + `plan.go`：承接原 `lesson_import.go` 的全部纯解析逻辑——单元/课映射、汇编文档定位与命名、Markdown 表格拆分与列判定、抽句与清洗、生词格过滤、导入计划与条目构造。常量、正则、去重与"只追加不覆盖"规则逐字迁移。
- 导出改名：原小写标识符按需要导出为 `UnitOfLesson`、`ResolveLessonDocs`、`LessonDocName`、`LessonLabelOf`、`LessonDocKind(s)`、`DocFetcher`、`ExtractLessonRows`、`HasLessonSection`、`LessonSectionsIn`、`MaxBankSeq`、`BuildImportPlan`、`NewBankSentence`、`ImportedRow`、`ImportPlan`、`SplitCells`、`TableColumns`、`LessonsPerUnit`、`MaxUnit`；`isSeparatorRow`/`cleanImportedJP`/`isVocabCell` 及 6 个正则保持私有。根包不设转发 wrapper，根 `lesson_import.go` 已删除。
- `cmd_lesson_import.go` 的编排（`resolveImportDocs`/`collectImport`/`cosFetcher`/`autoImportLessons`/`runSentenceImport`）与 `cmd_save_lesson.go`、`cmd_lesson_status.go` 只改包引用；CLI 行为、COS key 与导入报告结构不变。
- 测试：`lesson_import_test.go` 14 个用例随包迁移，依赖根 `collectImport` 的 `TestResolveAndCollectImport` 留根；新增 `rules_test.go` 覆盖单元映射越界、表格拆分与列号、清洗与生词格、文档命名、课号定位、最大序号与条目全字段；单包验证：`go test ./internal/lessonimport`。
- 已知按实现保留的行为（测试已固定，不要当 bug 顺手改）：`SplitCells` 不识别转义竖线；`NewBankSentence` 不填 `Prototype`；`UnitOfLesson` 不按 `MaxUnit` 封顶；`HasLessonSection` 只认 `## ` 标题前缀下的子串、`第190课` 不会误命中 `第19课`。

### 句子核心包边界（2026-11-03，普通句）

- `internal/sentence/sentence.go`：句库类型 `SentenceBank`/`BankSentence`/`SentenceHistory`/`SentenceWrong`/`WrongSentence`/`SentenceResult`，归一化 `Norm`（原 `normSentence`）与私有 `foldWidth`/`sentenceNormRe`，容错解析 `LoadSentenceHistory`/`LoadSentenceWrong`，出题 `BuildSentencePlan` 及轮换助手，落库 `ApplySentenceState` 及 `nextSentenceDue`/`pruneSentenceHistory`/`mergeUnique`/`chineseFromResults`。函数体、正则、阈值、注释逐字迁移，选题与错句规则不变。
- `internal/sentence/types.go`：`PlanSentence`；根 `types.go` 保留 `type PlanSentence = sentence.PlanSentence`，`ReviewPlan`/`RecordInput`/Excel/体验版因此零改动。
- 根 `sentence.go` 只留 9 个 `*Storage` 方法（三个 key + 六个读写），它们带 receiver 不能搬；`sentence_review.go`（回炉句）整文件仍在根包，另做一批。
- 导出改名：`normSentence→Norm`、`loadSentenceHistory→LoadSentenceHistory`、`loadSentenceWrong→LoadSentenceWrong`、`sortLessons→SortLessons`，其余无外部引用的保持私有；根包不设转发 wrapper。
- 测试：`internal/sentence/sentence_test.go` 随包迁移（依赖根 `sentencePlanLocked` 的 `TestSentencePlanLocked` 留根），另补 `rules_test.go` 覆盖归一化、容错解析、课号排序与轮换、到期推进、历史清理、去重与中文回填；单包验证：`go test ./internal/sentence`。
- 新包只依赖标准库，不 import 根包、不碰存储或网络。本批不改 HTTP/CLI 流程、句库 JSON 结构、COS key 与部署方式。

### 档案模块包边界（2026-11-03）

- `internal/archive/types.go`：`Archive`、`ChangelogEntry` 及两个方法 `AppendWord`、`SetDefinition`；根 `types.go` 只保留类型别名，字段顺序与序列化形态不变，`Groups` 为 `[]review.WordGroup`。
- `internal/archive/parse.go`：`ParseArchive` 与私有解析辅助（changelog 行、七列词行、非词行过滤、表格拆分）；状态机、表头行与全空行不进 footer、RawHeader/RawFooter 收集规则原样保留。
- `internal/archive/write.go`：`WriteArchive(arc, langName, wordColumn)`。语言名与词表首列列名由调用方查 `LangConfigs` 后传入，输出（emoji、`01/02` 时间、七列表头、总体进度统计段）与迁包前逐字节一致。
- `internal/archive/version.go`：`Filename(filePrefix, date, major, minor)`（原 `ArchiveFilename`，文件前缀改由调用方传入）、`ParseFilename`、`NextVersion`、`SameDay`（原私有 `sameDay` 导出）、`AddChangelogEntry`。文件名格式串与版本推进顺序不变。
- 新包只依赖标准库 `fmt/regexp/strconv/strings/time` 与 `internal/review`，不依赖 `LangConfigs`、COS 或 `Storage`。根包 `archive.go` 已删除，不留第二份实现。
- 测试：`internal/archive/version_test.go`、`parse_test.go` 覆盖文件名往返、非法文件名、版本推进（新日 / 同日 / 大版本）、`SameDay` 边界、解析各类场景、统计输出段、写出往返及两个方法返回值；单包验证：`go test ./internal/archive`。
- ⚠️ `WriteArchive` 的两个语言参数都是字符串，没有编译器保护：调用点传入的 `cfg` 必须与 `arc` 同源（同一 `lang` 变量或 `arc.Language`）。另外 `Archive.Language` 现在只写不读。
- 本批只改包归属与调用引用；档案格式、容错规则、COS key、鉴权、`go:embed web` 和部署方式不变，无新增依赖或数据迁移。

### 单词判分包边界（2026-11-03，后续批次）

- `internal/grading/grade.go`：单词四档判分、答案归一化和多写法匹配；公开 `GradeMode`、档位常量、`ParseGradeMode`、`GradeAnswer`，只依赖标准库。
- `internal/grading/grade_test.go`：原判分纯测试整体随包迁移；四档语义、空输入、IME 分隔符、片假名折叠和多形态断言不变。单包验证：`go test ./internal/grading`。
- `api_grade.go`、`api_offline.go` 和 `excel_meta.go` 调用该包；`api_grade_test.go` 和 `excel_meta_test.go` 仍在根包验证 HTTP 与 Excel 回填。`ImportPreview.GradeMode` 仍是字符串，JSON 字段不变。
- 网页仍使用原 `web/grade.js`，小程序仍调用 `/api/grade`，不新增第三份规则。目录迁移不改变判分口径，也不改变草稿、档案或造句判定。

### 大模型公共配置与请求包边界（2026-11-03，A批）

- `internal/llm/config.go`：`Config`、`LoadConfig`、`ParseFallbackModels`；配置来源仍为 `JRP_LLM_*`，内置文本默认模型、8秒默认超时及1至60秒有效范围不变，不覆盖服务器显式配置。
- `internal/llm/client.go`：`Call` 构造单次文本请求，私有 `buildRequest` 保留 `temperature:0`、`stream:false`；`Post` 是文本与视觉共用的单次HTTP发送路径，body原样发送，成功返回原始content。仍用原默认HTTP客户端、单次超时和1MiB限读，没有新增重试或严格超限校验。
- 两个对应的 `_test.go` 承接配置、备用列表解析和请求体原测试，补充请求头、错误响应、空内容、超时、读取上限及body关闭测试；单包验证：`go test ./internal/llm`。测试只用本地假传输，不访问真实网关或COS。
- `api_judge.go` 保留提示词、领域结果解析、COS模板与结果缓存、HTTP入口；`llm_failover.go` 保留两种切换循环，共享冷却状态已在后续B批迁入 `internal/llm/health.go`。只更新包调用，不改变失败判据。
- `api_vision.go` 保留视觉配置、内联图片请求体、并发、重试及总时间预算；`api_worddef.go` 保留档案优先和词义解析。视觉不能被改成文本请求，两者只共用 `Post`。
- 判句的JSON解析失败仍触发切换；词义/视觉仍按原流程在领域解析前记录请求成功，不统一这两类行为。冷却常量在后续B批移入 `internal/llm/health.go`，实际始终为2分钟，不能按旧注释改为5分钟。
- 本批无新依赖、无额外模型/COS请求、不迁移数据；仅改源码不代表上线，提交推送和部署按老师确认执行。

### 出题配额外置规则层包边界（2026-11-04）

- `internal/planconfig/config.go`：`PlanConfig` 类型、`DefaultSentenceBaseCount`/`DefaultSentenceReviewCount`/`DefaultCircleRatio`/`MaxPlanCount` 四个常量，以及 `ParsePlanConfig`、`BuiltinPlanConfig`、`PlanCircleRatio`、`ValidCircleRatio`、`ValidPlanCount`、`ChoosePlanConfig` 六个纯规则函数。只依赖标准库 `encoding/json`/`log`，不读写 COS、不碰 `Storage`。
- 根 `types.go` 只保留 `type PlanConfig = planconfig.PlanConfig` 别名；`storage_planconfig.go` 留 COS key `configs/plan_config.json` 与 `DownloadPlanConfig`/`UploadPlanConfig`/`DeletePlanConfig` 三个方法；`plan_config.go` 留 60 秒缓存与 `resolvePlanConfig`；`cmd_planconfig.go` CLI 留根。消费方（`api_plan.go`/`api_offline.go`/`cmd_sentence.go`/`sentence_review_test.go`）只改符号前缀。
- 行为逐字不变：内置默认 20+20、三圆占比 70、配额上限 50、0 合法 / 负数与越界回退内置、`ratio` 指针区分「没配(nil)」与「有意关掉(0)」、`omitempty` 序列化、60 秒缓存与 COS key 全部原样；无签名变更、无新依赖、无数据迁移。
- 测试：`internal/planconfig/config_test.go` 随包迁移原 `plan_config_test.go`，新增 `rules_test.go` 补内置默认值、三个校验函数的边界、`ratio` 指针与 JSON 往返、`ratio` 越界回退；固定输入，不连 COS/模型。根 `sentence_review_test.go` 保留 `TestPlanCircleRatioDefaults` 等边界测试。单包验证：`go test ./internal/planconfig`。
- 等价核对：6 个迁出函数与 `PlanConfig` struct 块级等价；8 个根文件还原前缀后按序匹配基线，仅 `storage_planconfig.go` 2 行、`types.go` 3 行为预期新增注释/别名，其余 0 漂移。
- 本批纯目录重构，线上行为不变；提交推送与部署继续分别确认。

### 大模型共享冷却状态包边界（2026-11-03，B批最小范围）

- `internal/llm/health.go` 只公开 `SkipModel` / `MarkResult`；失败阈值、冷却时间、map与互斥锁均为包内私有。状态仍按模型名在进程内共享，判句、词义、视觉以及不同网关使用同一份，不能改成每调用方独立实例。
- 连续失败2次后冷却2分钟；成功清零，冷却精确到期允许试探但不清失败计数，再失败立即重新冷却。查询未知模型不创建状态；时间点仍由调用方按原位置传入，不改为请求结束时重新取时。
- `llm_failover.go` 与 `api_vision.go` 只替换状态函数调用；不搬或统一切换循环、不改日志、领域成功判据、视觉请求体、整链重试、并发和总预算。`visionChat` 没有强制忽略冷却的分支，全链冷却时零请求并返回原错误。
- `health_test.go` 承接原冷却测试并补时间边界、成功清零、模型隔离和并发计数；根测试保留业务切换，并保护三类调用跨网关共享及解析时机差异。根测试使用独立模型名和公开成功记录清理，不为测试暴露全局Reset。
- 本批不新增依赖、不持久化健康状态、不更改学习数据或鉴权；提交推送、合并与部署继续分别确认。

### 回炉变形句规则层包边界（2026-11-03）

- `internal/sentencereview/types.go`：`ReviewBank`、`ReviewSentence`、`ProtoSuggest`（原私有 `protoSuggest`）；根 `types.go` 只保留 `ReviewBank` / `ReviewSentence` 两个类型别名，字段、顺序、JSON tag 与序列化形态逐字不变，不留转发 wrapper。
- `internal/sentencereview/review.go`：回炉挑句纯规则——`DefaultReviewIdleDays`、`CircleRank`（原 `circle.go` 的 `circleRank`）、`WordIdleDays`（原 `wordIdleDays`）、`wordIdleMap`、`BuildReviewSentences`。五条筛选（exclude / 原型句 exclude / 闲置过滤 / 最久没出过排序 / 同原型去重）+ 圆级分层逐字保留；`arc=nil` 跳过闲置过滤，`circle=nil` 或 `circleRatio<=0` 退回旧行为。只依赖 `internal/archive`、`internal/review`、`internal/sentence`，不碰 Storage/COS。
- `internal/sentencereview/prototype.go`：原型句挑选纯规则——`GroupLesson`、`LessonLabel`、`RoundAcc`、`JpLen`、`GroupProtosByLesson`、`ProtoScore`、`PickPrototypes`、`ProtoList`、`LoadReviewFile`、`NextReviewIDNum`。「长句优先 / 14-26 字 +15 / <10 字重扣」「跨课先往高课扩」「基本课文优先」等既有打分与排序规则原样保留。
- 根包只留 I/O 与编排：`sentence_review.go` 的 `DownloadReviewBank` / `UploadReviewBank` / `reviewCircle`，`circle.go` 的 `circleMap` / `applyCircle` / `unholdLesson`，`cmd_sentence_review.go` 的 `run*` / `checkReviewSentences` / `reportReviewCheck`。消费方（`api_plan.go` / `api_offline.go` / `cmd_sentence.go` / `cmd_lesson_status.go`）只改符号引用。（2026-11-04 注：`circleMap` 已在 wordmeta 批次迁往 `internal/wordmeta.CircleMap`，见下节；此处记录的是当时的边界。）
- 测试：`internal/sentencereview/review_test.go`、`prototype_test.go` 覆盖闲置天数跨年/非法值/边界、挑句各条筛选与圆级配额、长度三档打分、跨课扩展、两格式文件读取与序号推进；固定 `testToday`，不 sleep、不连 COS/模型。根 `sentence_review_test.go` 保留 `circleMap` 等根边界测试。单包验证：`go test ./internal/sentencereview`。
- 等价核对：14 个迁出函数与基线块级逐行等价（`ALL EQUIVALENT`）；`cmd_sentence_review.go` 编排代码去空白+还原前缀后按序匹配基线，0 行漂移（8 行差异仅为 `[]sentencereview.ProtoSuggest` 拉宽后的 gofmt 字段列对齐）。
- 本批纯目录重构，线上行为不变；不改 JSON / COS key / 数据格式，提交推送与部署继续分别确认。

### 词性表与三圆规则层包边界（2026-11-04）

- 这是**第一批动「共享类型归属」的拆包**：5 个类型跟着一起迁进新包，所以 6 个纯函数的函数体能逐字照搬、零类型前缀，只把函数名首字母大写导出。类型通过根 `types.go` 的别名压住引用面，十几个引用文件零改动。
- `internal/wordmeta/types.go`：`WordMeta` / `WordPOS`（原 `storage_wordmeta.go`）、`CircleItem` / `CircleInput` / `CircleOutcome`（原 `circle.go`）。字段、顺序、JSON tag、`omitempty` 逐字不变。根 `types.go` 加 5 条 `type X = wordmeta.X` 别名 —— `storage_wordmeta.go`、API、CLI、圆级、词性表 I/O、测试继续用原名，不留转发 wrapper。
- `internal/wordmeta/rules.go`：6 个纯函数 `CircleGroupOf` / `LessonOfGroup` / `CircleMap` / `LookupWordMeta` / `HoldSet` / `NearKeys`。只依赖标准库 `fmt`/`sort`/`strconv`/`strings`。语义原样：课号→分组「第N课 手册补录」、`CircleMap`/`HoldSet` 读不到或全空返回 **nil**（fail-open，不能因读不到词表就少出题）、`LookupWordMeta` 词形对不上不猜、`NearKeys` 去括号后子串匹配排序截断 5 个。
- 留根：`storage_wordmeta.go` 的 COS key `sharedBase()/word_meta.json` 与 `DownloadWordMeta`/`UploadWordMeta`（读不到返回空表兜底）；`circle.go` 的 `applyCircle`/`unholdLesson`（写档案）；`cmd_gen_plan.go` 的 `dueHold`（吃 `*Storage`）；`cmd_lexicon.go` 的 `buildLexicon`（依赖根包 `LexiconItem` DTO）；`sentence_review.go` 的 `reviewCircle`（改调 `wordmeta.CircleMap`，只加前缀）；CLI 全部留根。
- `circleMap` 归 `wordmeta`、`sentencereview.CircleRank` 归回炉句规则，**方向不反**：`CircleRank` 只吃 `CircleMap` 摊好的 `map[string]string`，不反向依赖。
- 本批无签名变更、无新依赖、无数据迁移；COS key、写回顺序、nil 语义、JSON 形态全部原样。
- 测试：`internal/wordmeta/rules_test.go`（6 函数边界：分组优先级、课号解析非法值、`CircleMap`/`HoldSet` 的 nil 与 trim、`LookupWordMeta` 裸假名不命中、`NearKeys` 排序截断）+ `types_test.go`（JSON tag / `omitempty` 形态与 round-trip）。根 `circle_test.go` / `api_wordmeta_test.go` / `sentence_review_test.go` 改前缀保留，**断言消息字符串里的旧函数名保留原样**（前几批口径）。固定输入，不连 COS/模型。单包验证：`go test ./internal/wordmeta`。
- 等价核对：6 个迁出函数 + 5 个类型 struct 与 `main` 基线块级逐行等价；12 个根文件还原前缀后按序匹配基线 **0 漂移**；`types.go` 5 行别名为唯一预期新增。
- 本批纯目录重构，线上行为不变；提交推送与部署继续分别确认。

### 复习计划数据模型包边界（2026-11-04）

- 这是**第一批动「COS 存档契约类型」的拆包**：`PlanWord` / `ReviewPlan` 原定义在根 `types.go`，为让离线 Excel 渲染侧能进 `internal/offline`（`package main` 不可被 import），先把这两个类型的家搬进包内、根留别名。**本批只搬类型，渲染函数仍在根 `excel.go`；下一批已迁完，见「Excel 渲染侧包边界」。**
- `internal/offline/plan.go`：两个 struct 逐字搬迁，字段、顺序、JSON tag、`omitempty` 全原样；唯一符号变化是 `ReviewPlan.Sentences` 从根别名 `[]PlanSentence` 改成 `[]sentence.PlanSentence`（`offline.go` 已 import `internal/sentence`，无循环依赖）。⚠️ `ReviewPlan` 是存进 COS 的存档 JSON，`storage.go` 的 `UploadPlan`/`DownloadPlan`/`UploadHardPlan`/`UploadSentencePlan` 都吃它；`PlanWord.Accuracy` 用 `*float64 + omitempty` 保真真正的 0%。改字段/顺序/tag 会静默改变线上存档格式。
- 根 `types.go`：加 `type PlanWord = offline.PlanWord` / `type ReviewPlan = offline.ReviewPlan` 两条别名 + 1 行 import；删两个 struct 定义。`PlanSentence = sentence.PlanSentence` 别名与其注释原样保留。15 个引用文件（`storage.go` / `api_plan.go` / `api_record.go` / `api_trial.go` / `cmd_gen_plan.go` / `cmd_export_hard.go` / `cmd_record.go` / `excel.go` / 各测试）**零改动**。
- 类型别名是同一类型而非新类型，`json.Marshal`/`Unmarshal` 走同一份结构定义与 tag ⇒ 存档 JSON 字节严格不变；无签名变更、无新依赖、无数据迁移、无 COS key 变化。
- 测试：`internal/offline/plan_test.go` 锁住契约——最小 `PlanWord` 的字节级 JSON 基线、日常 plan 的 `omitempty` 省略、`Accuracy=0` 指针不被吞、`ReviewPlan` 全字段形态与 `sentences` 嵌套、零值可选项省略、round-trip 等价。固定输入，不连 COS/模型。单包验证：`go test ./internal/offline`。
- 等价核对：两个 struct 与 `main` 基线块级逐行等价（43 行，去空白 + 还原 `sentence.PlanSentence` 前缀后 `diff` 为空）；`git diff --name-only main` 只有 `types.go`，其余引用文件 0 漂移。
- ✅ `internal/offline/offline.go` 包注释「渲染侧（生成 Excel）仍留在根包」本批仍成立；下一批迁渲染函数时已按要求同步改成解析侧 + 渲染侧同包。
- 本批纯目录重构，线上行为不变；提交推送与部署继续分别确认。

### Excel 渲染侧包边界（2026-11-04）

- 承接上批的编译前提（`package main` 不可被 import），把根 `excel.go` 渲染侧整体并入 `internal/offline`，与解析侧同包共用 `PlanWord`/`ReviewPlan`/`ExcelMeta` 及 sheet 名常量。**线上行为、Excel 字节、COS 存档全不变**，纯目录重构。
- `internal/offline/render.go`（新）：12 个函数 + `excelWriter`/`excelStyles`/`excelLayout`/`defaultExcelLayout` + `reviewCategories`/`hardCategories` + `HardSevere/Moderate/Mild` 三常量。⚠️ 三常量与 `hardCategories` 原定义在根 `cmd_export_hard.go`（判档逻辑用），现迁入渲染包作唯一数据源；根改留 `const HardSevere = offline.HardSevere` 等别名，删掉根里已无使用者的 `hardCategories`。
- `internal/offline/render_meta.go`（新）：`(w *excelWriter) writeMetaSheet` 随 `excelWriter` 一起搬（receiver 类型必须同包），根 `excel_meta.go` 删除。
- 根调用点改前缀：`api_offline.go` 三处 `offline.Generate*WithMeta`、`cmd_gen_plan.go` → `offline.GenerateExcel`、`cmd_export_hard.go` → `offline.GenerateHardExcel`；删根 `excel.go`/`excel_meta.go`/`excel_test.go`/`excel_meta_test.go` 四个文件。
- 测试随迁：`excel_test.go`→`render_test.go`、`excel_meta_test.go`→`render_meta_test.go`。⚠️ 同包后两处撞名，按「不丢覆盖」处理：① `col`/`sentenceNumRe` 本地 helper 删除，直接复用 `offline.go` 逐字等价的包内符号；② `TestParseExerciseFileGradeModes` 与 `offline_test.go` 表格驱动版同名但角度不同（这份是 生成→填答→解析 完整往返），改名 `TestExcelRoundTripGradeModes` 保留。两个测试补 `internal/sentence` import。
- `internal/offline/offline.go` 包注释按老师要求改掉「渲染侧（生成 Excel）仍留在根包」，改为解析侧 + 渲染侧同包。
- 等价核对：`excel.go`+`excel_meta.go` vs `render.go`+`render_meta.go` 归一化（去包声明/import/注释/空白、`offline.` 前缀、`PlanSentence` 别名）后 **330 行逐行零漂移**；唯一差异是有意迁入的 6 行 Hard 常量组，值与 `cmd_export_hard.go` 逐字一致。
- 验证全绿：13 包 go test -count=1 / -shuffle=on -count=1 / internal -count=10 / go vet；三平台 CGO_ENABLED=0 构建；gofmt（Windows 用 LF 版判读）；git diff --check。`go test ./internal/offline` 的字节敏感布局断言与完整往返测试全部通过。
- 本批纯目录重构，线上行为不变；提交推送与部署继续分别确认。

### P4 LLM 三域包边界（2026-11-04）

分层改造 P4：判句 / 查释义 / 拍照取词三域各单 commit，可单独 revert。**共同铁律**：用例层不 import `net/http` / `flag` / `*Storage` / COS / `internal/llm`；`llm.LoadConfig` / `llmChat` / `judgeWithFallback` / `visionChat` 的主备链与 `llm.SkipModel`/`MarkResult` 共享冷却全部留根包 IO 层，用例层只认最小接口。行为不变（HTTP 响应 JSON / COS 存档 / Excel 字节均不动）。

**判句 `internal/judge`（`ConfigStore` + `Caller`）**
- `ports.go`：`ConfigStore.DownloadJudgePrompt(ctx)`、`Caller.Judge(prompt)`。⚠️ 接口**不带 `llm.Config`** —— 配置由根包 `judgeCaller{cfg}` 持有，用例层不 import `internal/llm`（曾自校正过一版）。
- `prompt.go`：内置模板 `BuiltinPrompt` 全文 + `JudgePrompt`/`FillPrompt`/`ValidatePrompt`/`EnsureJSONRule`/`PromptVersion`/`ChoosePrompt`/`RenderPrompt`/`ParseReply`/`CacheKey` 与 `Result` DTO。保守判句口径（汉字↔假名/敬简算对、**助词缺失判错、替换判对**）有护栏单测盯着。
- `judge.go`：`ResolvePrompt` + `JudgeWithCache`；`promptTTL=60s` 与两个进程内内存缓存随用例搬入，不注入接口（设计文档 5.1）。
- 根包：`type judgeResult = judge.Result` 让 `llm_failover.go` 零改动；`judgeCaller` 调 `judgeWithFallback`；`resolveJudgePrompt`/`judgeWithCache` 薄适配；两个 HTTP handler 函数体与基线逐字一致。CLI `cmd_judgeprompt.go` 改 `judge.*` 前缀。

**查释义 `internal/worddef`（`ArchiveStore` + `Caller`）**
- `ports.go`：`ArchiveStore.DownloadLatestArchive(ctx)`、`Caller.Lookup(prompt) (reply, model, err)`。没配模型 / 全链失败都用 `err` 表达，用例层 `if err == nil` 才填 model 与解析 —— 与原 `llm.LoadConfig()` 的 `ok` 分支严格等价。
- `rules.go`：`Item`（根 `type WordDefItem = worddef.Item` 别名保 JSON）、`LLMItem`、`Prompt`、`FillPrompt`/`HasParen`/`FindWordByKana`/`ParseReply` + 私有 `stripCodeFence`。
- `lookup.go`：`Lookup(ctx, store, caller, lang, words)`，档案优先（精确 → 按假名，顺带补汉字）、剩下问模型、保序输出，分支逐字保留；`ctx`/`lang` 显式传参。
- 根包：`wordDefCaller` 持有 `LoadConfig`+`llmChat`（与判句共用冷却）；`s.lookupWordDefs` 薄适配给 `/api/word-def` 与 `/api/vision` 两入口共用；`hasParen` 留委托薄壳给 `api_vision.go` 借用；`handleWordDef` 函数体逐字一致。

**拍照取词 `internal/vision`（纯规则 + DTO，无 IO）**
- ⚠️ **形态与 judge/worddef 不同，按设计文档 3.7**：vision 的 `visionConfig`/`loadVisionConfig`/`visionChat`/`askVision`/`visionRetryable`/`visionHumanError` 带着共享冷却、`time.Sleep` 退避、并发 2 限流、120s 总预算、pending 每日 2 图配额 `takeQuota`，全是进程级时序状态，**注入接口反而会拆散冷却** ⇒ 全留根；`internal/vision` 只收纯解析规则，属 P2 那类规则包。
- `types.go`：`Candidate` / `ImageResult`（根 `VisionCandidate`/`VisionImageResult` 类型别名，tag 与 `omitempty` 逐字保留，HTTP JSON 字节不变）；`rules.go`：`Prompt`、`ParseReply`/`CleanCandidates`/`HasJapanese` + 私有 `stripCodeFence`。
- 根包 `api_vision.go` 只改 4 处（import / 两个 DTO 别名 / `const visionPrompt = vision.Prompt` / `askVision` 一处 `vision.ParseReply` 前缀）；`handleVision`/`askVision`/`visionChat`/`visionRetryable`/`visionHumanError`/`loadVisionConfig` 六个函数与基线字节级对拍 **全部 IDENTICAL**。
- ⚠️ `stripCodeFence` 在 worddef / vision 各一份逐字一致的私有副本（纯函数、无状态）。按老师「2 选 A」，P4 暂留两份副本不新建公共包；**已在 P5 收敛到 `internal/textutil.StripCodeFence`（见下方 P5 章节）**。

**验证**：三域各 `go build ./...` / `go test -mod=readonly -count=1 ./...` / `go vet ./...` / LF 版 gofmt / `git diff --check` 全绿；handler 与关键函数字节级对拍；新增 `internal/judge/{judge,prompt}_test.go`、`internal/worddef/{lookup,rules}_test.go`、`internal/vision/rules_test.go`（含 prompt 护栏、DTO JSON 字节、fake 接口不连 COS/HTTP/LLM）。收尾再加 `-shuffle=on -count=1 ./...`、`internal/... -shuffle=on -count=10`、三平台 `CGO_ENABLED=0` 构建，全绿。
- 本批纯目录重构，线上行为不变；提交推送与部署继续分别确认（本轮**不部署**）。

### P5 薄壳合并 + 收尾（2026-11-04）

老师选「薄壳合并 + 收尾」，**跳过维护域二期**（circle / 词库维护 CLI 一批 / 句库 import 体检，量大复用价值低，留待以后需要时再排）。收敛点选**甲**。一个独立 commit，可单独 revert，**不部署**。

**收敛点选甲：`internal/textutil`（新包）**
- 新建 `internal/textutil/fence.go`：`StripCodeFence` 去掉模型回复外的 ` ```json ... ``` ` 外壳。函数体与 P4 时 worddef / vision 的两份私有副本**字节级逐字一致**（`git show HEAD:` 对拍确认），`fence_test.go` 把九种输入形状锁住。
- `internal/worddef/rules.go`、`internal/vision/rules.go` 各删私有 `stripCodeFence`，`ParseReply` 改调 `textutil.StripCodeFence`。两用例包从此共用一份纯规则，互不 import。

**根薄壳清理（3 处）**
- `api_worddef.go`：删 `hasParen` 委托薄壳（`api_vision.go` 借用的名字）。
- `api_vision.go`：`hasParen` 两处调用改 `worddef.HasParen`；删 `const visionPrompt = vision.Prompt`，`askVision` 改直接用 `vision.Prompt`。
- `types.go` 的 24 条类型别名按设计文档 3.10「类型别名中枢」**留根不动**（保 HTTP / COS / Excel 契约字节，不是薄壳）。

**行为 / 代价 / 回滚**
- **行为不变**：全是纯规则搬家 + 别名直接化。三硬指标（HTTP 响应 JSON / COS 存档 / Excel 字节）一个不碰；`handleVision` / `askVision` / `handleWordDef` 逻辑零改动，6 处改动逐行核对。
- **代价**：internal 包 20 → 21；代价仅多一个无依赖纯规则包。⚠️ **根生产 `.go` 仍是 64，一个没减** —— 收敛的是函数/常量/重复实现，不是文件；64 个根文件都带真实职责（最小 39 行 3 个真函数），没有能整并删除的纯空壳文件。要真降文件数只能做「文件级合并」（把 `storage_*.go` 等并进大文件），那是另一档架构动作，本轮不做。
- **回滚**：`git revert` 单个 commit 即回到 P4 合并态，不碰数据、不碰线上。

**收尾**：`docs/layered-design.md` 两处过时文字（15 行「均为纸面设计」、295 行「没有改任何生产代码…等老师点头」）改为 P0→P5 已落地的实际状态。两份 SKILL.md 同步。全量回归同 P4 口径（build / test / shuffle / internal×10 / vet / 三平台 / gofmt / diff-check）全绿。
- **本轮不部署**。

### 腿一 + 腿二 2A 收尾包边界（2026-11-04）

老师判定 P0→P5 后「根文件没减、internal 小包过碎」→ 按「腿一 + 腿二 2A」分步重做，每期单 commit 可 revert，**全程未部署**。

**腿一：根目录物理合并**（同 `package main` 内拼接，函数体逐字不动、import 并集去重、文件头来源清单 + 分区标注）
- R1：26 个 `cmd_*.go` → 4（`cmd_words` / `cmd_sentence` / `cmd_lesson` / `cmd_admin`），根生产 `.go` 64→42。
- R2：17 个 `api_*.go` + 7 个 `storage*.go` → 5（`api_practice` / `api_llm` / `api_auth` / `api_data` / `storage`），42→23。
- 收尾：22 个根 `*_test.go` → 5（按被测域）。**根目录 `.go` 总数 86→28**（生产 23 + 测试 5）。`main.go` 分发 / `serve.go` 路由一字未改。
- ⚠️ 报「根目录文件数」要给**总数**（生产 + 测试），别只报生产口径。

**腿二 2A：internal 小包按功能相近合并**（21→17 包）
- R3-A 句子域：`sentence` + `sentencereview` + `lessonimport` → `internal/sentence`。三包零符号重名；后两包原本反向 import `sentence`，合并后删该 import + 去 `sentence.` 前缀；导出符号 122 / 测试 73 零丢失。
- R3-B 词条域：`words` + `wordmeta` + `lexicon` → `internal/words`。`lexicon` 原本依赖 `wordmeta`，合并后去前缀；`worddef` 改指 `words`；根 `types.go` 5 条别名、`api_data.go` 的 `LexiconItem` 别名改前缀；导出符号 46 / 测试 23 零丢失。
- ⚠️ **降级不做**：`record` + `grading`（`grading → offline → grading` 成环）、`planconfig → plan`（根文件反向依赖重包，语义倒退）。
- `archive` / `review` 不反向依赖这两个域，合并后无循环。

**当前 17 包**：`review` `archive` `sentence` `words` `grading` `offline` `plan` `planconfig` `record` `stats` `conjugation` `knowledge` `llm` `judge` `worddef` `vision` `textutil`。模型链 / 冷却 / 限流 / 预算 / 配额仍留根。

**唯一函数体改动**：R3-B 改前缀后，`api_practice.go` 的 `enrichWordMeta(..., words []PlanWord)` 参数名与 `api_practice_test.go` 同名局部变量跟包名 `words` 遮蔽冲突 → 两处改名 `planWords`（编译期等价，逻辑零变化）。其余只改前缀。

**验证**：三硬指标（HTTP JSON / COS 存档 / Excel 渲染字节）+ body 逐字对拍 + 导出符号/测试函数集合对拍 + 旧包前缀残留检查 + 全量 build / test / shuffle / internal×10 / vet / 三平台 / gofmt / diff-check / CLI 冒烟。3 处存量 gofmt 差异（继承自 `api_admin_test.go` / `api_wordmeta_test.go` / `bootstrap_test.go`）按小步不夹带保持原样。
- **本轮不部署**。

## Command Reference

| Command | Flags | Description |
|---|---|---|
| `import` | (stdin) | Import archive markdown from stdin to COS |
| `add-words` | `--input <json>` | Add new words to archive |
| `gen-plan` | `--date <YYYY-MM-DD>` `--sentences <json>` `--output <path>` | Generate review Excel (auto-initializes today's v1.0 archive if none exists) |
| `export-hard` | `--min-accuracy <0-1>` `--min-reviews <N>` `--date <YYYY-MM-DD>` `--output <path>` | Export ALL hard words (钉子户) to Excel. Read-only: never bumps the archive version |
| `record` | `--input <json>` `--hard` | Record review results (`--hard` resolves numbers against the export-hard plan) |
| `update-def` | `--input <json>` | Update word definition |
| `update-word` | `--input <json>` | Update a word's target-language form (fix a typo like a stray long-vowel mark) |
| `normalize-words` | `--dry-run` | Normalize word forms to `かな(漢字)`. Backs up to `history/` first; aborts if word count changes. **Always dry-run first** |
| `dedupe` | `--dry-run` | Remove duplicate word entries from archive (byte-identical text only). Keeps the one with highest reviewCount. Backs up to `history/` first. **Always dry-run first** |
| `merge-words` | `--input <json>` `--dry-run` | Merge two entries that are the same word under different forms (e.g. `すし` → `すし(寿司)`) — the case `dedupe` cannot catch. Sums ReviewCount/ErrorCount, max's ConsecutiveCorrect, later LastReview. Backs up to `history/` first. **Always dry-run first** |
| `stats` | `--days <N>` | Show statistics for last N days |
| `save-lesson` | `--file <path> --name <name>` | Save knowledge doc to COS（存的是汇编文档时，**自动**把里面每课的句子导进句库，见输出 `auto_import`） |
| `list-knowledge` | (none) | List all knowledge documents in COS |
| `get-knowledge` | `--name <filename>` | Download a knowledge document from COS |
| `sentence-import` | `--lesson <N>` `--apply` `--doc <name>` | 从「课本原文汇编」自动抽第N课的课文句子进句库。**默认干跑**，`--apply` 才写回。查重（一模一样就跳过）+ 直接追加，不做覆盖 |
| `lesson-status` | `--lesson <N>` \\| `--all` | 一次查某一课四件套：知识点 / 汇编·基本课文 / 汇编·应用课文 / 单词 / 句库 |
| `encrypt-env` | (none) | Encrypt the plaintext `.env` into `.env.enc` (AES-256-GCM, keyed to machine/user/skillDir). Backs up the old `.env.enc` first. Does NOT need `--lang` | 
| `serve` | `--addr <ip>` `--port <n>` | Start the web review UI (keyboard + handwriting input) |
| `dump-lexicon` | `--output <path>` | 导出词汇总表（跟 `GET /api/lexicon` 同一份数据）。⛔ 只读 |
| `circle` | `import --input <json>` \| `tag --input <json>` \| `unhold --lesson <N>` `--dry-run` | 三圆手册分级，见下节 |

### 三圆手册分级（circle，2026-11-02 起）

《标日初上 三圆词汇速记手册》把每课的词分成 **三圆（核心）/ 二圆 / 一圆**。
分级和「挂起」都存在 **`word_meta.json`** 的 `WordPOS` 里（`circle` / `hold` 字段），
⛔ **不动档案的 7 列 markdown 表格** —— 动表格要改所有读写档案的地方，不值。

- `circle import`：手册里有、总表没有的词 → 按课建「第N课 手册补录」组追加，**并 Hold**
- `circle tag`：只写 `circle` 标，不动词库（已有词用这个）
- `circle unhold --lesson N`：学到第 N 课了，把这一课的词放出来进练习（N=0 全放开）

⚠️ **Hold 的语义**：挂起的词**在词汇总表里照常显示**，只是不进 `BuildDuePlan`（每日练习）。
补录那批大多是还没学到的课（21–24 课），让它们涌进待复习毫无意义。

⚠️ **fail-open**：`holdSet(nil)` 返回 nil。读不到 word_meta 就一个都不排除 ——
拿不到挂起名单不能把整个计划清空，宁可多出题。

⚠️ **def_fix 必须在加词之前应用**。输入里的 `def_fix` 是给老词补区分括号用的；
顺序反了的话，新词撞车检查看到的是「还没补括号的老词」，会报一堆假警告
（2026-11-02 第一次 dry-run 就报了 11 条）。

⚠️ **部署必须先于写数据**：旧二进制不认 `hold`，先写数据会让补录的词全涌进每日练习。

**复习时显示圆级（2026-11-03 老师要的）**：单词复习的每一题都要能看到这个词是几圆词。
圆级跟 `pos`/`sub`/`note` 同一条路 join —— `lookupWordMeta()` 返回四个值，
`enrichWordMeta`（今日练习 + 钉子户）/ `enrichReviewMeta`（回看快照）各带一遍，
⛔ **不进 plan 存档也不进快照**（随时可能补录，存了就是过期数据）。

前端一处改、四处生效（web 的 `app.factsHtml()` 现在是**三格**：圆级 / 词性 / 备注）：
卡片模式、列表模式、钉子户、回看态。⚠️ **列表模式的 `list.js` 是白名单式 map**
（`set()` / `setReview()` 各一份），加字段漏了这里 = 列表模式永远显示「—」（踩过）。
手册没收的词 `circle` 为空 → 显示占位「—」，**不是不显示**（三格版式不许塌成两格）。

**画法是画圈，不写字**（老师 2026-11-03 定的）：三圆 3 个红圈、二圆 2 个、一圆 1 个。
个数本身就是级别，扫一眼知道轻重；文字只留在 web 的 `title`/`aria-label`
和词汇总表的详情行里。web 用 `.cdots`/`.cdot`（`app.circleHtml()`），
小程序用同一套类名（样式在 **`app.wxss`**，页面 wxss 只作用于本页）。

小程序：圈的个数只有一份 `miniprogram/utils/circle.js`（词库页 + 练习页共用），
WXML 里 `wx:for` 遍历 `circleDots`（⚠️ WXML 不能自己数数，数组要在 JS 里算好）。
体验版的题是服务端写死的、不带 circle → 那一组不出现，属正常。

**词性一律显示符号，不写中文**（2026-11-03 老师要的）：
N / V1 / V2 / V3 / Aい / Aな / Adv / P / Pron / Q / N.pr / Adn / Conj / Int / Expr / Phr / Idm / —。
⛔ **这是穷举表，不是「匹配不上就原文兜底」** —— 词性表 25 种取值全有确定符号；
真冒出新词性原样显示（不吞掉）。老数据「动/1」和新数据「动词/一类」两套写法都认。

- 两份实现必须同口径：`web/app.js` 的 `app.posSymbol()` 与
  `miniprogram/utils/pos.js` 的 `posSymbol()`。测试 `outputs/pos_symbol_test.js`
  两边各跑一遍再互相比对 —— 改一边忘另一边会挂。
- 中文没删，挪到 `title`（web 悬停）和词汇总表/详情行 —— 老师选的「悬停+详情」。
- ⚠️ **活用专项答题前仍只给 V / A**（`posSymbol(w, true)`），小类照样藏着；
  title 也只能给大类，否则符号藏了、悬停漏出「一类」等于白藏。

**跟手册对词时的匹配顺序**（`tmp/cmp2.py`，本地比对用）：
原形精确 → ます形（手册给原形 `洗う`、档案存 `洗います`）→ 平片假名互转（`ごみ`/`ゴミ`）
→ 单假名兜底（`き`/`ひ`/`め`，⚠️ 必须最后，否则 `の` 会误命中助词）→ 近似前缀。
漏了ます形那步，14/19 课会假缺一大片；漏了单假名，木/日/気/火/目/子 全假缺。

| `user` | `list` \\| `approve <uid>` \\| `set-role <uid> <owner\\|member\\|pending\\|disabled>` \\| `disable <uid>` \\| `rename <uid> <名字>` | 账号管理。列出/批准/改角色/停用/改名。第一个登录的人自动成为 owner |

## Web Review UI (`serve`)

`jrp --lang ja serve` starts a self-contained web app (embedded via go:embed, works offline)
for **active flashcard-style review** — one word at a time, answer by keyboard IME or handwriting.

- `GET /api/plan?date=YYYY-MM-DD` — returns today's due-word plan (auto-inits v1.0 archive if
  the latest is from a previous day; uploads the plan JSON so `record` can resolve numbers)
- `POST /api/record` — applies results (same logic as CLI `record`), bumps version, uploads archive
- `GET /api/conjugation?type=verb|adj&count=N` — 活用练习题（见下节）
- `POST /api/words` — 录入新词（2026-10-21 加，给小程序的「录入」用）：
  `{"items":[{"word":"みず(水)","def":"水"}]}`（`group` 可省略，省略落「未分组」）
  → `{"success":true,"added":1,"duplicates":0,"version":"v1.5","total_words":931,
  "dup_definitions":[…],"dup_core_definitions":[…]}`。
  **跟 CLI `add-words` 共用 `words_add.go` 一份实现**（重复词跳过、中文释义撞车点名、
  20 词以上跳大版本）。撞车只点名不阻断 —— 录完要看 `dup_definitions`。一次最多 200 词。
  ⚠️ **不要求选课**：老师说词不一定属于哪一课。出题是 `AllWords()` 遍历所有组、
  不按课过滤，所以「未分组」里的词照样会被练到。
- `POST /api/word-def` — 补汉字 + 查中文意思（2026-10-21 加，录入时只用打假名）：
  `{"words":["みず","たべもの"]}`
  → `{"success":true,"enabled":true,
  "items":[{"input":"みず","word":"みず(水)","def":"水"}],"missing":[…]}`。
  **`word` 是建议录进档案的词形**（补了汉字的），前端要拿它替换原输入。
  老师 2026-10-21 反馈「这个日语词的汉字不加上吗」—— 档案里词形是「假名(汉字)」，
  不补汉字这个词录进去就没有汉字，复习时学不到写法。
  **两条来源，按顺序**：
  ① **档案优先**（最准、不要钱，且保证跟复习时看到的中文是同一份）：
     先按原样 `FindWord`，再按**假名部分** `findWordByKana`（老师打「みず」，库里是「みず(水)」）。
  ② 剩下的问模型，prompt 要它一并给 `kanji`，服务端拼成 `kana(kanji)`。
  ⚠️ 老师自己写了括号（`みず(水)` / `さん（敬称）`）就**不再补**（`hasParen`）。
  ⚠️ 模型没配（`JRP_LLM_*`）不是错误：`enabled:false` + 已有那部分，缺的留前端补。一次最多 60 词。
  实测：`みず→水/水`、`たべもの→食べ物/食物、食品`、`さん→（空）/称呼`、`がくせい→学生/学生`。
- `POST /api/vision` — 拍照取词（2026-10-21 加）：`{"images":[base64...]}`（最多 9 张、每张 ≤4MB）
  → `{"success":true,"enabled":true,"images":[{"index":0,"ok":true,"model":"…",
  "candidates":[{"word":"みず(水)","def":"水"}]}],"candidates":[…扁平去重…],"failed":[…]}`。
  ⚠️ **走视觉大模型，不是 OCR**（老师一句「我不是配了大模型吗」问得对，2026-10-21 改的）。
  OCR 只能把字一行行抄出来，还得写正则猜哪半截是词；视觉模型看得懂版面，
  自己跳过例句和标题，直接给结构化结果 —— 正则那层整个省掉。
  实测（课本生词表截图）一次认对：`みず(水)/水`、`たべもの/食物`、`がくせい/学生`，
  例句「これは わたしの ほんです。」和标题「第3课 生词表」都被排掉。
  - **返回按图片分组**（`images[i]` 跟请求里第 i 张一一对上），前端按图分块展示、
    每块配原图缩略图 —— 老师 2026-10-21 原话：「既然是拍照取词，那就意味着有些词
    我是不认识的，你不把原始照片给我展示出来，我怎么知道你识别出来的单词是准确的
    还是不准确的呢」。扁平的 `candidates` 同时保留给不关心分组的调用方。
  - **认完顺手补中文 + 补汉字**（复用 `lookupWordDefs`，跟手打录入同一份口径）：
    课本照片上常常只有假名没中文，不补的话老师看到的就是一片空白。
  - 网关复用 `JRP_LLM_ENDPOINT` / `JRP_LLM_API_KEY`，**模型名单独用 `JRP_VISION_MODEL`**
    （默认 `deepseek/deepseek-v4-flash-vision-exp`，备 `hy-vision-2.0-instruct`，
    都实测过：2.2~2.5s / 2.1~5.1s）。
    ⛔ **不能拿文本模型当视觉模型用** —— 现配的 `deepseek-flash` / `glm-5.3-flash`
    是纯文本，吃不了图片。
  - ⛔ **链上的模型必须先在网关上确认存在**：原来的备胎 `glm-5v-turbo` 实测回 402
    「service ID does not exist」，主模型一超时备用根本接不住。
    ⛔ **别放推理型视觉模型**：`hunyuan-t1-vision-20250916` 两次实测 13.1s / 50.5s，
    30 秒超时下第二档必挂。
  - 多张图并发 2（给大图留余量），单张失败整链重试一次（402/400 不重试，纯属白等）。
  - 报错翻人话再回（`visionHumanError`）：网关那串 `permission_error / does not exist`
    甩给老师等于没说。
  - 没配网关 → `enabled:false` + `enabled_note`，不报错。只认词**不写档案**，
    老师勾完再走 `/api/words`。
  - ⚠️ **前端发图前必须缩图**（`shrinkToBase64`：`>250KB` 才重绘 JPEG 0.72，
    尺寸顺带压到长边 1280）：手机原图两三兆，模型算 30 秒都算不完 ——
    2026-10-21 老师传 4 张只回来 1 张，线上日志写的是 `context deadline exceeded`。
    ⚠️ **判断条件必须是文件体积不是像素尺寸**：`chooseMedia` 带 `sizeType:['compressed']`
    出来的图长边常常正好 1280，按尺寸判「不用缩」就漏了，文件仍有 800KB~1.5MB。
  - ⚠️ **三层超时必须挨着排**：服务端整批预算 120s < 小程序 `wx.request` 180s
    < nginx `proxy_read_timeout` 300s。**哪层先断，老师看到的就是哪层的报错**。
    2026-10-21 就因为 nginx 那层只有 60 秒（默认），整批被掐成裸 504。
  - 整批到点不报错：**返回 200 + 已认出的那几张 + 剩下的标超时**，老师点「重认这张」
    单独再来 —— 比整个请求 504 强得多。
- `POST /api/grade` — 批量判分（2026-10-19 加，**给小程序用**）：
  `{"mode":"kana|kanji|full|either","items":[{"word":"すし(寿司)","answer":"寿司"}]}`
  → `{"success":true,"mode":"either","results":[true]}`。复用 `internal/grading` 的 `GradeAnswer`，
  web 端仍在浏览器本地用 `web/grade.js` 判，两边同一份口径。空 word/answer 直接 false。
- Frontend `web/index.html` + `web/kanji/` (KanjiCanvas, MIT). Handwriting recognizes
  **kanji + hiragana + katakana** (kana patterns were generated from KanjiCanvas XML and appended
  to `ref-patterns.js`). Recognition is per-character → user taps candidate → assembled into the word.
- Grading: lenient (kana match is enough, ignores kanji/parens; katakana folded to hiragana) or
  strict (kana+kanji must match). Wrong cards requeue for immediate re-practice. Only the **first
  attempt** per word is recorded.

Run: `jrp --lang ja serve --addr 0.0.0.0 --port 8080` to expose on a server; default `127.0.0.1:8080`.
COS credentials load the same way as other commands (`.env.enc` or env vars).

### 网页端行为（2026-10 起）

- **练习支持部分提交**：主按钮只提交**已练的**条目，没练的留在当天 plan 里；
  卡片/钉子户的二次提交只发**增量**。所以一天可以分多次提交，不用一次做完——
  老师问「还有几道没练」时，指的是留在当天 plan 里的那些。
- **错词按快照累积**：当天多次提交后错词要累积显示；快照合并**按词形**而不是按题号
  （多批 plan 的题号会重复，按题号会互相覆盖，导致当天统计虚高），
  且空条目不再覆盖已答条目（否则刷新后整批重发会抹掉当天错词）。
- **未交的句子留在当天 plan**：造句部分提交后，没交的句子刷新还在，全交完才换一批。
- **统计页顶部有 JLPT N5/N4 进度条**（数据来自 `stats` 的 `jlpt` 段，仅日语）。
- **词库页显示近义区分备注**（`lexicon` 接口带出 `word_note`，写入方式见 Workflow 6c）。
- **活用专项答题前只显示词性大类**（动词/形容词），对完答案才显示一类/二类，
  避免提前把活用规则透给老师。

## 活用练习页（动词 / 形容词活用）

`serve` 端有三个**只练不计档**的活用练习页（2026-09-28 新增；ない形 2026-10-13 新增）：
给出档案里的原始词，老师自己写出活用形，比默写原词更能验规则是否内化。

| 页 | 接口 | 题型 |
|---|---|---|
| 动词活用 | `GET /api/conjugation?type=verb&count=N` | 一卡**三空**：原形 + て形 + た形 |
| 动词 ない形活用 | `GET /api/conjugation?type=verb_nai&count=N` | 一卡**两空**：原形 + ない形 |
| 形容词活用 | `GET /api/conjugation?type=adj&count=N` | 一卡**三空**：过去式 / 否定形 / 过去否定形（都带 です） |

- 词池 = 档案全部词条按词性表 `pos`/`sub` 过滤（当前 **125 动词 / 92 形容词**）。
  `sub` 兼容「一类/二类/三类」「い形/な形」**和**数字 `1/2/3` 两种写法——
  词性表实际用的是中文类别名，只认数字会让词池缩到个位数。
- 判分复用 `grade.js`：答案里用 `/` 分隔的多个写法**都算对**
  （ナ形否定 `じゃありません/ではありません/じゃないです/ではないです` 全认）。
- 特殊形按规则判：`いい→よかった`、`かっこいい→かっこよかった`；
  行く例外（`行って`）；特殊五段词干认 **い 结尾**
  （`ください/なさい/いらっしゃい/おっしゃい/ござい`）——写成「ござ」会让 `ございます` 算成 `ござう`。
- ない形两个坑：① 一类动词末音「い」→**わ**（`かいます→かわない`，不是かあない）；
  ② **`あります` 词性表标的是「一类」，但否定形是 `ない`**，不是「あらない」——靠规则躲不过去，
  在 `internal/conjugation` 的 `VerbNai` 里特判。`VerbNai` 的原形**复用 `Verb`**，变形规则只留一份。
- 跳过 `〜ています` 与寒暄固定说法；**纯前端作答，不写档案、不 bump 版本、不进统计**。
- 单测在 `internal/conjugation/rules_test.go`（含 `かいます` 这类普通五段的反面保护，防被特殊规则误伤）：`go test -run TestConj ./internal/conjugation`
- **た形（2026-11-03 加）**：跟て形是**同一套音便**，只差 `て→た`/`で→だ`。
  所以只在 `internal/conjugation.Verb` 里多算一个 ta（五段走 `taEnd` = `teEnd` 换尾音，
  一段/サ变/カ变各多拼一个字），**绝不把音便规则抄第二份** —— 抄两份迟早漂移。
  `TestConjTaMirrorsTe` 盯着这件事：拿て形换尾音跟た形比，对不上就红。
- 加新题型（可能形、ば形…）的姿势：在 `internal/conjugation` 增加导出的纯规则函数 → `api_conjugation.go` 的 `switch typ` 加一个 case →
  `web/index.html` 的 `#conjType` 加 option → `web/conjugation.js` 的 `typeNames` 补一条。
  前端按服务端返回的 `tasks` 渲染，填空数量不用改代码。
- ⚠️ **`api_trial.go` 的固定题要同步补**：体验版那几张卡的 label 数必须跟真接口一样
  （`TestTrialConjugationLabelsMatchRealAPI` 盯着），否则试用版比正式版少一个空。
- ⚠️ 速查表（动词活用表）**两处各一份**：`web/index.html` 的 `.gtable` 和
  小程序 `pages/conj/conj.js` 的 `GRAMMAR_ROWS`。加一列要两边都加。

⚠️ 改完 `internal/conjugation/*.go` / `api_conjugation.go` / `web/conjugation.js` 后，要上线须**重新交叉编译，并在老师单独确认部署后更新服务器**，
否则老师页面看不到变化（见下一节）。判断新旧最可靠的办法是 `strings <二进制> | grep conj-input`。

## 账号体系与小程序（2026-10-16 起）

登录方式 = **小程序码确认登录**：网页出小程序码 → 微信扫 → 小程序里点「确认登录」→ 网页轮询到确认后落地会话。
web 登录后进原来的页面，微信里扫码后进小程序。

### 数据隔离（零迁移设计）

- `Storage` 多了一个 `uid` 字段。**`uid == ""` ⇔ owner ⇔ COS key 与今天逐字节相同**，老数据不用搬。
- 普通成员：个人数据落在 `<语言根>/users/<uid>/...`（`personalBase()`），
  **教材/共享数据仍读 `<语言根>/...`**（`sharedBase()`）。
- 个人：档案 archivs / history / plans / 三个 xlsx / drafts / reviews / sentence_history / sentence_wrong
  共享：sentence_bank / sentence_review / knowledge / word_meta / configs
- ⚠️ `plans/` 目录里 personal 与 shared **混住**（`sentence_bank.json` 是教材共享，`plan_<date>.json` 是个人进度）
  → 只能按函数区分，**绝不能按目录切**。
- 拿用户维度的 Storage：`st.ForUser(uid)`（浅拷贝，不改原来的）。`NewStorage` 签名没变，CLI 一行没动。

### 会话与路由

- Cookie `jrp_sid`（`Path=/; Max-Age=30天; HttpOnly; Secure; SameSite=Lax`）；
  小程序端没有 cookie jar → 用 `Authorization: Bearer <token>`，后端同一张 session 表两种载体都认。
- 会话存内存 + COS 兜底（`_auth/sessions/<sid>.json`），重启不丢。
  ⚠️ **寿命分两种**（2026-10-28 老师定）：`sessKindWeb` 30 天，`sessKindMini` **7 天**，
  按最后一次使用滑动。`createSession(u, ua, kind)` 第三个参数别漏 —— 老会话没 `kind` 字段按 30 天算。
  ⚠️ **体验版会话不落 COS**（`uid == trialUID` 跳过 `persistSession`）：/api/auth/trial 匿名可领，
  落盘等于让人白嫖 COS 写对象。
  ⚠️ 内存表有清扫 goroutine（`sweepSessions`，10 分钟）+ 上限 `maxSessions=3000`：
  没有这两样，被丢弃的会话（尤其体验版）会一直占着内存只涨不落。
- 用户表 `_auth/users.json`（语言无关），角色 `owner | member | pending | disabled`。
  第一个登录的人自动 owner，其余 **pending**，要 `jrp user approve <uid>` 才能进。
- ⛔ **红线**：开了强制登录后，**无凭证绝不能退回 owner**（那就是把老师的账号给人白用）。
  `withUser` 里缺凭证直接 401。
- 编译期防串号：handler 签名是 `func(w, r, st *Storage)`，所有要用数据的 handler 必须走 `srv.withUser(...)`，
  漏改直接编译不过。
- `authRequired()`：没配凭证且用户表为空 → false（本地开发照旧裸奔）；一旦有用户 → 强制登录。

### 微信接口

- `JRP_WX_APPID` / `JRP_WX_APPSECRET` **只放服务器 `/etc/jrp/jrp.env`（chmod 600）**，
  不进 COS、不进 git、不写进 SKILL.md 正文（这里只写变量名）。AppID 不敏感（进 `project.config.json` 无所谓），
  AppSecret **别贴进对话/工单**，老师自己在服务器上写。
  验证（不打印 secret，只数有没有拿到）：`set -a; . /etc/jrp/jrp.env; set +a; curl -s "https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=$JRP_WX_APPID&secret=$JRP_WX_APPSECRET" | grep -c access_token` → `1` 就通。
- `JRP_WX_ENV`：小程序码指向哪个版本，`release`（默认）/ `trial` / `develop`。
  ⚠️ **小程序没发布时 `getwxacodeunlimit` 报 41030，开发期必须设 `trial`**；发布后删掉这行回默认。
- 微信后台「开发管理 → 开发设置 → 服务器域名」要加 `https://jrp.110105.xyz` 为 request 合法域名，否则真机请求被拦。
- ticket ≤ 32 字符（`wxacode.getUnlimited` 的 scene 硬上限，超了微信只报错不说原因）→ 16 字节 base64url = 22 字符。
- `wxacode.getUnlimited` **要求小程序已发布**，未发布报 41030；开发期用 `env_version:"trial"` + `check_path:false`。
- 个人主体小程序：拿不到手机号、头像昵称匿名化、**没有 unionid** → 确认页只写「用此微信登录」，跨端身份不依赖 unionid。

### 部署顺序（阶段 3 的红线）

切会话鉴权 + 摘 nginx Basic Auth 是**唯一的危险窗口**，顺序不可反：

1. 先部署 Go（此时已强制登录，nginx Basic Auth 还开着 = 双重保护）
2. nginx **先放行登录链路**：`login.html` / `style.css` / `auth.js` + `/api/auth/`
3. 扫码登录成功（用户表出现记录 = `authRequired()` 打开）
4. 才摘掉全部 `auth_basic`

⚠️ **原顺序里第 2 步做不了**（2026-10-19 发现）：`auth_basic` 在 443 **server 级**，
连 `/api/*` 一起挡。Basic Auth 还开着时，小程序请求 `/api/auth/*` 会被 nginx 401，
根本到不了 Go → 扫码验证无从谈起。所以必须先放行登录链路。

**每一步都要老师单独点头。** 中间任何一步出问题就停在那一步，别往前推。

### nginx 现状（2026-10-19）

已走到第 3 步之后、第 4 步之前。线上 `auth_basic` 仍在 server 级，但多了两条放行：

```nginx
location ~ ^/(login\.html|style\.css|auth\.js)$ { auth_basic off; proxy_pass http://127.0.0.1:8080; include /etc/nginx/snippets/jrp_proxy.conf; }
location /api/                                 { auth_basic off; proxy_pass http://127.0.0.1:8080; include /etc/nginx/snippets/jrp_proxy.conf; }
```

- 为什么 `/api/` 整体要放行：**小程序发不出 Basic Auth 口令**，它只有 Bearer token。
  只要还有一条业务接口被 Basic Auth 挡着，练习页就是 401。
- 放行不等于裸奔：`authRequired()` 恒为 true，没登录凭证的请求 Go 自己回
  `{"error":"unauthorized","login":"/login.html"}`（2026-10-19 实测 `/api/plan` 公网 401 且无 Basic 挑战头）。
- 页面文件（`/`、`/index.html`）**仍要口令**，第 4 步才摘。
- 公共转发头抽到 `/etc/nginx/snippets/jrp_proxy.conf`（备份在
  `outputs/deploy/jrp_proxy.snippet.conf`）—— 放行 location 和主 `location /`
  必须用同一份，漏一个 `X-Forwarded-Proto` 就会出现「页面能开、接口怪错」。
- ⚠️ **snippet 里没有 `proxy_*_timeout`** ⇒ 走 nginx 默认 60 秒。2026-10-21 拍照取词
  被掐成裸 504 就是这么来的，已在 `location /api/` 单独补
  `proxy_connect_timeout 10s; proxy_send_timeout 300s; proxy_read_timeout 300s;`。
  ⚠️ 别被 `grep proxy_read_timeout` 骗了：那三行 300s 是 **mistake-book 那个 server** 的，跟 jrp 无关。

### 小程序

`miniprogram/` 目录。`project.config.json` 里 appid 已填真值；
⚠️ `project.private.config.json` 是开发工具本地配置，已加进 `.gitignore`，别提交。

五个页面（2026-10-19 做完）：

| 页 | 路径 | 拉什么 | 落不落档 |
|---|---|---|---|
| 首页 | `pages/index` | 不发请求，只跳 | — |
| 列表复习 / 钉子户 | `pages/practice` | `/api/plan?mode=words` 或 `/api/hard`（`?mode=hard`） | 落，`/api/record` |
| 造句 | `pages/sentences` | `/api/plan?mode=sentences` | 落，`sentence_results` |
| 活用练习 | `pages/conj` | `/api/conjugation?type=verb\|verb_nai\|adj` | **不落**（只练不计档，跟网页端一致） |
| 统计 | `pages/stats` | `/api/stats?days=7\|30\|90` | 只读 |
| 词汇总表 | `pages/lexicon` | `/api/lexicon`（全量拉一次，本地搜） | 只读 |
| 录入新词 | `pages/addword` | `/api/vision`（取词）→ `/api/word-def`（查中文）→ `/api/words`（写入） | **落**，写新档案 |
| 知识点 | `pages/knowledge` | `/api/knowledge`（课号列表）/ `?lesson=N`（分块正文） | 只读 |

tabBar 三项：**练习 / 统计 / 录入**。
⚠️ 「录入」是工具不是练习 —— 老师 2026-10-21 明确要求不放首页色块，跟练习、统计并列在底部。

⚠️ **tabBar 是自定义的**（2026-10-21）：原生 tabBar 的 `app.json` 只有
`color` / `selectedColor` / `backgroundColor` / `borderStyle`，**没有 fontSize**，
老师嫌「练习 / 统计」字太小 → 改 `"custom": true` + 根目录 `custom-tab-bar/`。
两个必须记住的点：
① 自定义 tabBar **不占位是 fixed 的** → 每个 tab 页要自己留 `padding-bottom:180rpx`；
② 每个 tab 页有**各自的实例**，`onShow` 里必须 `this.getTabBar().setData({selected:n})`，
   否则从别的页切回来高亮还停在上一个。`app.json` 里 `tabBar.list` 仍要写全，
   `wx.switchTab` 靠它校验合法页面。

**知识点板块「新标日III知识点」（2026-11-03 老师要求）**：首页色块入口（`data-mode="knowledge"`），
页面 `pages/knowledge/knowledge`。⚠️ **不在 tabBar 里** —— 老师选的是首页卡片，tabBar 保持三项。

- **markdown 在服务端拆块**（`internal/knowledge/markdown.go` 的 `ParseMarkdown`，由 `api_knowledge.go` 调用）：小程序没有
  markdown 渲染能力，而且「哪几块模糊」必须按块切。块类型只有 h1/h2/h3/p/quote/list/table，
  行内拆成 spans（bold / code）。**不认识的行一律当段落** —— 丢内容比样式难看严重得多。
- **只认「第N课知识点」命名**：knowledge 目录里还混着 `课本原文汇编_第N单元（…）.md` 和
  `造句易错语法速查（第12-13课）.md`，它们不是按课的知识点，不能进列表。课号按**数字**排
  （字符串排序会把第 10 课排到第 1 课前面）。
- **接口 ⛔ 不走 withUser**（唯一一条对匿名放行的读接口）：知识点是教材内容，不属于任何人的
  个人数据；withUser 在鉴权打开后对匿名直接 401，那样未登录/体验版进来就是白屏。
  自己在 `knowledgeFullAccess` 里认会话：真登录给全文，匿名和 **体验版**给
  `locked:true` + `visible`（体验版凭证是公开的，等于匿名）。
  ⛔ **`sess.UID == ""` ⇔ owner，不是匿名**（2026-11-03 真事故）：这里原来写成
  `if sess.UID == "" || sess.UID == trialUID { return false }`，把 owner 本人挡在门外 ——
  表现是「小程序里每日复习已登录、知识点还是未登录」。匿名压根没有会话
  （`sessionUser` 先返回 false），能走到这行的 uid 为空只可能是 owner。
  任何**自己认会话**的路由都要记住这条；走 withUser 的路由不受影响。
- **模糊是展示层软限制，不是加密**：全文照样下发，前端把下标 >= `visible` 的块盖
  `filter: blur()`；抓包能拿全文 —— 这是有意的取舍，老师要的是「被挡住」的视觉。
  ⚠️ 部分安卓 WebView 不支持 CSS filter → 兜底 `color: transparent + text-shadow`。
- **可见块数** = min(文本量三成, 块数三成)，至少 3 块、**且至少留一块被挡**
  （实测第21课 13/44、第1课 8/27）。只按文本量算的话标题块太"便宜"，会放出 41% 的块。
- 登录后当场解开：页面挂 `syncTrial()`（app 广播会叫到）→ 重拉当前课；
  浮层里的「微信登录」走 `utils/trial.js` 的 `goLogin`，登录成功自动调 `load()`。

**判分只有一份实现**：新增 `POST /api/grade`（`api_grade.go`），复用 `internal/grading` 的 `GradeAnswer`，
小程序「对答案」整屏一次请求拿回 `results:[bool]`；web 端仍在浏览器本地用 `web/grade.js` 判。
⚠️ 别在小程序里再抄一份判分规则，两份迟早漂移。

#### 体验版（试用账号，2026-10-28）

`pages/welcome` 是 `app.json` 的第一页：微信登录 / 试用账号二选一。
试用 = 服务端一个**真实会话**（`POST /api/auth/trial`，uid=`trial`，**不写用户表**），
只是被 `api_trial.go` 的 `trialAllowed()` 白名单关在三个版块里：

- 放行：`/api/plan` `/api/hard` `/api/grade` `/api/judge-sentence` `/api/auth/me`
  `/api/review` `/api/draft` `/api/stats` `/api/lexicon` `/api/conjugation`
- 其余一律 403（`/api/words` `/api/record` `/api/vision` `/api/word-def` `/api/offline` …）
- ⚠️ 加路由进白名单前先问一句「会不会带出老师的个人数据」，会 → 不许加。
  判句放行是因为它只读请求里的句子 + `sharedBase` 下的 prompt，但**会花钱调模型**。
- 统计 / 词汇总表 / 活用三条走 `writeTrial*` 分支返回**内置假数据**，`TestTrialReadonlySections`
  盯着结构（snapshots 必须**升序**，否则 KPI 增量算反；accuracy_distribution 五档要跟
  前端 `DIST_ORDER` 对上，少一档整块不显示；conj 的 label 要跟真接口一致）。
  前端不认 trial，全靠结构对齐 —— 改真接口结构时**记得同步这三处**。

题面写死在 `api_trial.go`（每日复习 5 词 / 钉子户 5 词 / 造句 3 句课本原文），
**固定不轮换** —— `TestTrialQuestionsAreStable` 盯着。
`/api/plan` `/api/hard` 对 trial 直接 `writeTrialPlan()` 返回，**一次都不碰 COS**
（走正常路径会在 `users/trial/` 下凭空初始化档案）；`/api/review` `/api/draft` 直接回空。

返回里带 `trial:true` + `read_only:true`，前端据此：练习页/造句页**藏掉「提交」**并在
`submit()` 里再挡一次（`/api/record` 是 403，藏了不挡就是点了弹个错）。进度不落档。
五个版块**都能进**（首页色块全在、tabBar 三项全开），只是题量少、统计是假的、写了不保存。

录入页（`addword`）对体验版**只展示**：顶部一行「体验版不接大模型、不写词库，这里只展示
页面」，`addManual` / `pickImages` / `submit` 三个入口全部 `trialBlocked()` 挡掉
（`/api/vision` `/api/word-def` `/api/words` 都 403，不挡就是点了弹个错）。

首页底部**只有一个退出入口**（`onExit`）：试用态写「退出试用」，登录态写「退出登录」，
点一下直接 `app.logout()` —— 不弹确认框。
⚠️ 别在页面上再堆第二个退出/登录入口（2026-10-28 老师明确说「全在下方放着，让人不理解，
只留一个退出试用」）。练习页/造句页底部也不要再写「回到首页退出试用」这类引导 ——
状态说明一行 `体验版 · 固定 N 词 · 不保存进度` 就够了。

**未批准的用户（pending）有 7 个自然日的完整体验期**（2026-10-28 老师定的）：

| 时间 | 待遇 |
|---|---|
| 注册起 7 个自然日内 | **完整权限**，跟 member 一样（数据在 `users/<uid>/` 下，能练能录） |
| 第 8 天起 | `/api/*` 一律 403 +「管理员还没审核通过，7 天体验期已结束」 |
| 之后再登录 | 照样挡 —— 判断读的是库里那条记录的 `created`，`upsertUser` 只更新 `last_login`，**重新扫码不会重新计时**，只有删记录才会 |

⚠️ 之前一版是「pending 降级成体验版（假数据）」，已废弃 —— 老师要的是完整体验。
实现**三处**，少一处就漏：`withUser`（拦接口）+ `handleAuthWXLogin`（小程序登录时挡）+
`handleAuthStatus`（**网页扫码**那条路，2026-10-28 补）。网页端漏了的话，会话还有 30 天寿命，
过期 pending 的人照样能确认 ticket 拿到 web 会话，变成「能登录但用不了」。
天数按**自然日**算（`pendingDaysLeft`，不看时分秒）；老数据没有 `created` 按满格给，不误伤。
`/api/auth/me` 带 `pending` + `days_left`，首页摆一句倒计时。
⚠️ `/api/auth/me` 走 `withUser`：会话 role 是登录那一刻的快照，批准/冻结后要在
`withUser` 里 `sess.Role = u.Role` 自愈，不然用户得重新扫码。
⚠️ **认人靠 openid，不是 uid**：`upsertUser` 先 `findUserByOpenID`，命中就只改 `last_login`，
`newUID()` 只在新建那条分支里。所以同一个人第 8 天再登还是同一条记录同一个 uid，拦截照样生效。
换微信号 = 新 openid = 新记录又能用 7 天（没 unionid，堵不了）。
测试：`TestPendingGracePeriod` / `TestPendingDaysLeftCountsCalendarDays` /
`TestPendingExpiredCannotCompleteWebLogin` / `TestDisabledUserStillBlocked`。

**pending 的大模型配额**（2026-10-28 老师定）：pending 有 7 天完整权限，但拍照取词每张图
都真调视觉模型 → **每天最多 2 张**（`pendingVisionDaily`，见 `quota.go`，按自然日、按 uid 记在
`users/<uid>/quota/vision_<date>.json`）。⛔ 判定在**扣之前**：被拒的请求不落盘，
否则老师批准后那天的额度会平白少掉。`TestPendingVisionDailyQuota` 盯着这条。
体验版（trial）走不到这儿 —— `/api/vision` 不在白名单里。

⚠️ 光清 token 不够 —— `onLaunch` 原来没 token 就调 `login()` 静默登录，
退出后冷启动会被自动登回去，等于退不掉、也永远看不到登录页的「试用账号」。
所以 `logout()` 额外写 `jrp_logged_out` 标记，`autoLogin()` / `refreshMe()` 的失败重登
都先查它；只有 `login()` / `loginTrial()` 成功才 `clearLogoutFlag()`。

写这五个页踩到的坑，都写进注释了：

1. **JLPT 进度**：`jlpt.n5.vocab` / `n5.lesson` 是**门槛值**（800 词 / 第 22 课），
   不是当前值，当前值是顶层的 `vocab_firm` 和 `lesson`。拿门槛当进度 → 进度条恒 100%。
2. **拉计划不传 `date`**，日期一律由服务器按自己的时钟定。传设备本地日期看着更准，
   但老师在东京时比服务器快 1 小时，会把「今天」算成未来日期 → `read_only=true`
   → 不上传 plan → 回写时题号映射不到词，**等于白练**。
3. **WXML 里不能调 `.join()`**（那是 WXS 的活）。活用页要显示多个正确写法，
   得在 JS 里拼成 `expectedText` 再绑。
4. **写了答案但没判分就提交 = 白练**（网页端那条红线一样适用）。
   `practice` / `sentences` 都在 `submit()` 里先自动判一次再落库，不用老师多点一下。
5. **统计页的趋势在 `snapshots` 里，不在 `detail` 里**（2026-10-19 老师反馈「看不出趋势」）。
   `/api/stats` 返回 `snapshots`（每天一个存档点：total/mastered/basic/needs_consol/untested/errors）、
   `changes`、`detail`、`jlpt` 四段；只用 `detail` + `jlpt` 就没有走势、没有区间增量。
   小程序画不了 SVG → 走势用「每日堆叠柱」代替 web 的堆叠折线（一根柱 = 一个存档点，
   柱顶即总词量），90 天抽样到 30 根。版面与 `web/stats.js` 对齐：
   KPI（含增量）→ 掌握度构成 → 词库走势 → 正确率分布 → 钉子户 → 复习最多的词 → 每日明细（折叠）。

---

## 新账号引导（2026-11-02 审核被拒后补的）

审核员拒审两次的原因之一：新用户目录里**什么都没有**，/api/plan、/api/conjugation、
/api/lexicon 全 500「no archives found」—— 整个 app 默认老师那种已有档案的老账号。

- **写路径**（plan / hard / 录词）用 `EnsureArchive`：没档案就造一份空档案落盘，之后照旧。
  ⚠️ 里面有 `hasArchive()` 兜底：**有档案却读不出来 = 真故障**，报错，绝不拿空档案盖真数据。
- **纯读路径**（conjugation / lexicon）用 `LoadArchiveForRead`：没档案回空数据（**不落盘**），
  别让一次 GET 凭空写对象。conjugation 空池子回 `success:true + items:[]`（页面有「共 0 题」空态），
  ⛔ 不许再回 `success:false`——审核看到「页面报错」就是拒审。
- 测试 `TestBootstrapEmptyAccount`（memObjStore 桩，含空档案录第一个词）。

## 先浏览后登录（2026-11-02 审核要求；登录页已删）

「打开小程序就要求登录」违反运营规范。老师 2026-11-02 再进一步：**登录页这一屏整个不该存在**，
`pages/welcome/` 已删除。现在的形态：

- **入口是 `pages/index/index`**（app.json 第一项），也是唯一门户。
- 冷启动没 token → `autoLogin()` **静默领体验版**（loginTrial），不做 wx.login。
- ⛔ **refreshMe 验票失败不许静默 `login()` 登回账号**（2026-11-02 老师连环报「还能输入」
  的根源之一）：一律落回试用态。真实登录态只能来自用户亲手点的那下「微信登录」，
  否则界面上无迹可循，没人知道自己是不是登录了。
- 录入页顶部常驻状态条：试用态黄条 / 登录态绿条（「已登录 · 录入直接写入档案」），
  两种状态一眼分得清。排查这类问题先看黄条在不在。
- 退出过（`jrp_logged_out` 标记）**照样领试用身份**：试用态就是默认态，退出后照常逛。
  ⛔ 别做「已退出登录」的门（我做过一版，老师直接问「怎么变成这样了」）——
  标记只挡「静默 wx.login」，`loginTrial()` **不清**这个标记（自动领身份不是用户主动登录，
  清了的话下次 token 过期就被静默登回账号，退出白点）。标记只有 `login()` 清。
- 登录动作**就地发生**：试用态底部链接「微信登录后可保存进度」、锁定输入框的登录浮层、
  退出态的「微信登录」色块，全都直接 `app.login()`（wx.login 静默，不弹授权）。
  浮层的 `goLogin` 成功后顺手 `page.load()`（有这方法的页面）按真身份重拉数据 + 解锁。
- ⛔ 新代码里不许再出现 `wx.reLaunch('/pages/welcome/welcome')` —— 页面已经没了。

## 试用态：能看不能写（2026-11-02 老师定口径）

既然后台默认给试用身份，就别让人填完才发现不保存。**所有输入框在试用态一律锁死**，
点一下弹登录浮层（底部半屏，一个「微信登录」+ 一个「先随便看看」）。

- 共用 `miniprogram/utils/trial.js`（`sync` / `onTapLocked` / `closeLogin` / `goLogin` / `noop`）
  + `<include src="../../trial-sheet.wxml" />`。页面只要挂方法、data 里带 `trial`/`needLogin`。
- ⛔ **别写成「真 input + disabled + 透明遮罩」**：input/textarea 是原生组件，部分机型上
  盖在普通 view 上面，遮罩接不到点击 → 点了没反应更糟。
  正解是一对 `wx:if/wx:else`：试用态渲染同 class 的 `<view class="answer locked">`，
  正常态才是真 input。样式 `.locked` 在 app.wxss（灰底 + flex 居中）。
- `trial.sync` 挂 onShow：冷启动领身份是异步的，进页面那刻可能还没拿到；
  从登录页换身份回来也靠它把锁解开。addword 本来有 onShow，改成在里面调 `trial.sync(this)`。
  ⛔ **2026-11-03 的真事故**：`sync` 原来只认 `this`，而 `trial.sync(this)` 是普通函数调用
  （`this` = trial 模块对象，不是页面）→ `this.setData` 不存在 → **onShow 里抛异常**，
  `data.trial` 永远停在初始值 `true`（默认锁）。症状极隐蔽：**「进了页面还是未登录，
  点一下输入框才恢复」**——因为 `onTapLocked` 是挂页面上的方法，this 正常，点一下就纠正了。
  现在 `sync(page)` 参数、this 两种都认（都不是就静默返回）。
  ⚠️ 用 `onShow: trial.sync`（方法挂载）的页面从没出过这问题 —— 两种写法都能跑，别混着猜。
- **身份没确认完就进页面**：`sync` 里若「有 token 但 `logged`/`role` 缺一个」会主动
  `refreshMe()` 核一次（核完广播回各页解锁）。⛔ 没 token / role 已是 trial 时不核。
- 首页（`pages/index`）自己算 `trial`，也要挂 `syncTrial()` —— 否则在别的页面登录后，
  留在栈里的首页要等切回去才更新（2026-11-03 一并修）。
- ⚠️ **`role` 也要落本地存储（`jrp_role`）**，跟 token 一起在 login / loginTrial /
  refreshMe 写入、logout / 验票失败时清除，`autoLogin` 恢复 token 时一并恢复 role。
  ⛔ 只置 `logged=true` 不恢复 role：缓存的是试用 token 时，验票回来之前
  `isTrial()` 是 false，页面会按「已登录的真实账号」渲染，输入框全放开。
- **锁定态 = 没有确凿真实身份**：`lockedState()` 写成
  `!(logged && role && !isTrial())`，即 **默认锁、只有拿到真身份才开**。
  ⛔ 反过来「找出理由才锁」必漏：只看 `role==='trial'` → 领试用身份失败
  （限流 429 / 断网 / 首帧没回来）时 role 是空的 → 反而放开输入框；
  只看 `!logged` → 验票异步未回那一下该开的没开。
  页面 `data.trial` 初值一律 **true**（先锁着），靠 onShow 的 `trial.sync` /
  `broadcastIdentity` 同步到真身份才解开。首页同口径（三条件都看）。
- 判断统一用 `trial.locked(this)`；`onTapLocked` 每次也以 app 身份重同步一次兜底。
  ⚠️ `app.autoLogin()` 拿到缓存 token 时必须**先把 `logged` 置 true** 再 refreshMe ——
  否则每次冷启动都会闪一下「全锁住」。
- 身份一变（login / loginTrial / logout / refreshMe）app 会 `broadcastIdentity()`
  叫所有页面的 `syncTrial`（= `trial.sync`），别指望只有 onShow 会刷新。
- 锁的不止输入框：**practice 的「不会」「算对」「对答案」「再练错词」「提交」、
  sentences 的「对答案」「提交」、conj 的「换一批」「对答案」、addword 的
  「加入待录列表」「选图片」「录进档案」** 一律走 `onTapLocked`，按钮同步置灰
  （`.btn.locked` 在 app.wxss；addword 自己定义了 `.btn/.btn.ghost`，页面 wxss
  优先级更高，所以在它那里又写了一遍且必须放在 `.btn.ghost` 后面）。
- ⛔ 页面 onLoad 里不许 `app.login()`（替陌生人注册 + 违反先浏览后登录），
  要补身份就用 `app.loginTrial()`。lexicon 原来就是这么写的，已改。

## 安全基线（2026-10-28 全量评审后立的规矩）

老师问「从一个黑客角度反观下我的 web 端和小程序端」。评完立的几条铁律，
**以后加接口/改 nginx 前先对照一遍**：

1. **任何拼进 COS key 的参数都必须白名单或格式校验**。
   翻车现场：`date` 没校验就进 `drafts/draft_<date>_<mode>.json`，
   `mode` 有白名单唯独 `date` 没有 → `../../` 能捅到桶里别的目录。
   现在统一走 `safeDate()` / `safeDateParam()`（严格 `YYYY-MM-DD`，否则 400）。
   新增带日期的接口，第一件事就是调它，别自己拼。
2. **鉴权必须 fail-closed**：`authRequired()` 原来「读不到用户表」和「表真是空的」
   表现一样（空切片）→ COS 抖一下匿名就成了 owner。现在 `usersUnknown()` 时一律要求登录。
   ⛔ 任何"读失败就当没有"的写法都是这个坑。
3. **凭证不许走 URL**：`bearerToken()` 已不接受 `?token=`（会进 access log / 历史 / Referer）。
   下载类接口改 fetch + Blob + `a[download]`（见 `web/offline.js` 的 `export`）。
4. **匿名能领的东西必须限流**：`/api/auth/trial` 匿名可领会话，而体验版白名单里
   `/api/judge-sentence` 是真调大模型的 —— 不限流就是免费大模型代理。
   nginx 侧：`jrp_login`(10r/m) 挡 trial/wxlogin/ticket/qr，`jrp_llm`(30r/m) 挡
   vision/judge-sentence/word-def。⛔ **`/api/auth/status` 是登录轮询，绝对不能限。**
5. **安全响应头只写在 nginx server 级**：nginx 的 `add_header` 继承是「有则覆盖」不是合并，
   任何 location 里出现一个 add_header，server 级那一整摞就全失效。
6. **pending 有 7 天完整权限 = 也能烧你的模型钱** → 每日配额卡住（`quota.go`）。
   以后再加「调大模型」的接口，先问「pending 能不能打，打几次」。

### 已经确认安全的（别再改坏）

- COS 桶私有：匿名 GET `language-review/_auth/users.json` 和列目录都是 **403**。
- `appsecret` / LLM key 只在 `/etc/jrp/jrp.env`（600），从不进 COS。
- cookie `HttpOnly + Secure + SameSite=Lax`（Lax 顺带挡跨站 POST 的 CSRF）。
- `--auth-token` 用 `subtle.ConstantTimeCompare` 比。
- `group`、词形、释义只进 JSON 内容，不进 key。
- `/api/judge-prompt` 只有 GET，改 prompt 只能走 CLI。
- 静态资源走 `path.Clean`，没有目录穿越。

### 还没做（老师已知，性价比低）

- 会话不绑 IP/UA；sid 明文存 COS（桶私有，可控）。
- 小程序 token 明文落 `wx.setStorageSync`（微信沙箱内）。
- 扫码登录的固有钓鱼面：攻击者自造码骗受害者扫+确认 → 会话归攻击者。
