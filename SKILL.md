---
title: "Language Review Planner (jrp)"
summary: "Ebbinghaus-based vocabulary review system. Manages word archives, generates Excel review plans with sentence exercises, records review results, and tracks progress. Supports Japanese/English/French."
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

Note: `CountNailHouseholds` in `ebbinghaus.go` keeps a separate, error-count-based
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

Current Japanese knowledge docs (标准日本语初级上册 第1-15课，其中第15课为 2026-09-24 增补):
`标准日本语初级上册_第N课知识点.md`

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
   `add-words` / `update-def` 现在会自动检测并输出 `dup_definitions` /
   `same_def_as` 警告 —— 看到警告就必须补区分，不能无视。Create a JSON file:

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
      - **Cover ALL learned lessons (第1-15课；句库 v3 覆盖到第15课，第4单元仅第16课待补), NOT just the last 2-3.** Balance the 20
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
      - **应用课文短问答可"成对"出题**：甲问乙答两句合成一道题（中文提示写成一问
        一答），把「あちらです」「5,800円です」「わたしのです」这类一句两三个词的
        短应答也纳入句池，避免有效句池被压缩到只剩每课 4 句基本课文。
      - **⚠️ NEVER invent your own sentences.** Every sentence must trace to a
        textbook original in the knowledge docs. This replaced the old "generate from
        scratch" rule at the user's request (2026-08-27): textbook lines are guaranteed
        correct and within learned scope, so the old leak risk disappears.
      - **⚠️ Hard scope guard**: Only pick sentences from lessons the user has learned
        (currently 第1-15课，第4单元仅13-15课入库). If a consolidated-原文 doc has a placeholder
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
课本原文、句库 v3 覆盖第1-15课（第4单元仅第16课待补）、近 7 天归一化不重复、错句重练强制插入、变形句。
- 变形句上限按比例放大：普通模式 20 句最多 4 句 → **纯句子 50 句最多 10 句**。
- 50 句量大，**归一化自检必须做**（近 7 天重复 + 当天内部重复），否则重复率会明显上升。
- 跨课覆盖要摊平：第1-15课每课至少 2-3 句，别被最近学的课吃掉一半（程序侧已有轮转起点轮换+最新两课加权）。

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
5. Report: document saved to COS

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
  + `git reset --hard origin/main`。
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
- Always use GitHub MCP connector for code operations (read, push, create files)
- Direct git push may fail with 502; MCP or API is more reliable

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
| `save-lesson` | `--file <path> --name <name>` | Save knowledge doc to COS |
| `list-knowledge` | (none) | List all knowledge documents in COS |
| `get-knowledge` | `--name <filename>` | Download a knowledge document from COS |
| `encrypt-env` | (none) | Encrypt the plaintext `.env` into `.env.enc` (AES-256-GCM, keyed to machine/user/skillDir). Backs up the old `.env.enc` first. Does NOT need `--lang` | 
| `serve` | `--addr <ip>` `--port <n>` | Start the web review UI (keyboard + handwriting input) |

## Web Review UI (`serve`)

`jrp --lang ja serve` starts a self-contained web app (embedded via go:embed, works offline)
for **active flashcard-style review** — one word at a time, answer by keyboard IME or handwriting.

- `GET /api/plan?date=YYYY-MM-DD` — returns today's due-word plan (auto-inits v1.0 archive if
  the latest is from a previous day; uploads the plan JSON so `record` can resolve numbers)
- `POST /api/record` — applies results (same logic as CLI `record`), bumps version, uploads archive
- Frontend `web/index.html` + `web/kanji/` (KanjiCanvas, MIT). Handwriting recognizes
  **kanji + hiragana + katakana** (kana patterns were generated from KanjiCanvas XML and appended
  to `ref-patterns.js`). Recognition is per-character → user taps candidate → assembled into the word.
- Grading: lenient (kana match is enough, ignores kanji/parens; katakana folded to hiragana) or
  strict (kana+kanji must match). Wrong cards requeue for immediate re-practice. Only the **first
  attempt** per word is recorded.

Run: `jrp --lang ja serve --addr 0.0.0.0 --port 8080` to expose on a server; default `127.0.0.1:8080`.
COS credentials load the same way as other commands (`.env.enc` or env vars).
