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
The AI handles photo recognition, text parsing, and sentence generation; the Go binary handles
all archive operations (parse, update, version, upload to COS).

## Binary

```
~/.workbuddy/skills/jrp/jrp.exe
```

## COS Credentials

Auto-loaded from `~/.workbuddy/skills/tencentcloud-cos/.env.enc` (AES-256-GCM encrypted).
No manual env var setup needed. The Go binary decrypts at runtime using the same key derivation
as the COS skill (SHA-256 of hostname:username:skillDir).

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

Current Japanese knowledge docs (8 lessons, 标准日本语初级上册 第1-8课):
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
3. Create a JSON file:

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

   c. **Generate 20 sentences**:
      - Uses ONLY grammar points and sentence patterns found in the knowledge docs
      - Chinese prompt for the user to translate, Japanese answer as reference
      - Cover variety of lessons and grammar patterns learned so far
      - 对比的は已在第5课学过，可用于 `Aは～が、Bは～` 之类的对比句式
      - **⚠️ Generate sentences from scratch every time.** NEVER reuse or reference
        sentences from previous days' plans, COS plan JSONs, or any other source.
        Old sentences reflect the user's proficiency at that time and often cover
        lessons ahead of current progress (leading to L10+ vocabulary/grammar leaks).

   d. **Self-check before saving**: Verify every sentence against the knowledge docs.
      If any sentence uses grammar not in those docs, rewrite it. If any sentence
      uses vocabulary (nouns, verbs, adjectives) from lessons beyond what's in the
      knowledge docs, replace it with vocabulary from the learned lessons.
5. Save sentences to a fresh JSON file (always create a new file, never append to an old one):

```json
[
  {"chinese": "今天天气很好", "answer": "今日はいい天気ですね"},
  {"chinese": "我喜欢吃寿司", "answer": "私はすしが好きです"}
]
```

6. Run: `jrp --lang ja gen-plan --date YYYY-MM-DD --sentences /tmp/sentences.json`
   - This is the FINAL call that produces the deliverable Excel with both words and sentences
   - Default output: `outputs/review_YYYY-MM-DD_vA.B.xlsx` (version auto-parsed from archive)
   - **This is the only Excel you present to the user** — the Excel from Step 1 (no sentences) was a data-extraction artifact and MUST NOT be presented
7. Present the Excel file to the user using present_files (path must be in workspace `outputs/`)

**Excel structure**:
- Sheet names: `✏️练习版` / `✅答案版`
- Words grouped by status section: ☠️钉子户 → 🔴待巩固 → 🟡基本掌握 → 🟢抽查 → 🔄待测试
- 练习版: 8-column layout: 序号 | 中文 | 日语 | 比对 | 序号 | 中文 | 日语 | 比对
- 答案版: 6-column layout: 序号 | 中文 | 日语 | 序号 | 中文 | 日语
- 比对列(D/H)含自动比对公式，WPS 打开后自动显示匹配结果
- Gray header rows (D9D9D9), centered bold
- Sentence exercises: `📝 造句 共N句` title, S1-SN numbering, B:C merged Chinese, D:F merged target language
- Output naming: `review_yyyy-mm-dd_vA.B.xlsx` (version from current archive)

### 4. Record Review Results

**Trigger**: User reports review results (e.g., "1,3,5写错了，其他对").

**Steps**:
1. Parse user input: identify which word numbers were correct/wrong
2. Support batch recording — user may report multiple batches per day
3. Create JSON:

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

4. Run: `jrp --lang ja record --input /tmp/results.json`
   - Add `--hard` when the numbers come from an **export-hard** Excel (see workflow 5),
     otherwise the numbers will be resolved against the wrong plan
5. Report: how many correct/wrong, updated stats, new version
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
21. **⚠️ Sentences MUST be generated from scratch every day.** NEVER reuse, reference, or
    carry forward sentences from previous days' plans. The user's proficiency evolves,
    and old sentences may contain vocabulary/grammar from lessons the user hasn't reached
    yet (e.g. L10+ content when the user is on L8). The COS `plans/plan_<date>.json` files
    are stripped of sentences before upload (Go code does this), so checking old plans
    for sentence content is both unnecessary and misleading. When generating sentences for
    `gen-plan`, use ONLY: (a) the due-word list from Step 2, (b) knowledge docs from Step 3,
    and (c) nothing else.

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

## Language Codes

| Code | Language | Archive Prefix | IMA Knowledge Base |
|---|---|---|---|
| ja | 日语 | 日语学习进度档案 | 自学日语 (7452509467574409) |
| en | 英语 | 英语学习进度档案 | 英文知识库 |
| fr | 法语 | 法语学习进度档案 | (to be created) |

## Binary Path

```
JRP_BIN=~/.workbuddy/skills/jrp/jrp.exe
```

All commands: `$JRP_BIN --lang <ja|en|fr> <command> [flags]`

## GitHub

- Repo: https://github.com/zhangyf/jrp (public)
- Always use GitHub MCP connector for code operations (read, push, create files)
- Direct git push may fail with 502; MCP or API is more reliable

## Environment

The Go binary needs `PATH` to include the Go SDK for toolchain auto-download:
```
PATH=$HOME/go-sdk/go/bin:$PATH
```

Or set the `JRP_COS_SKILL_DIR` env var if the encrypted COS credentials are in a non-default location.

Set this before running jrp commands if the binary was compiled with a newer Go toolchain.

## Source Code

GitHub: https://github.com/zhangyf/jrp (private)

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
| `normalize-words` | `--dry-run` | Normalize word forms to `かな(漢字)`. Backs up to `history/` first; aborts if word count changes. **Always dry-run first** |
| `dedupe` | `--dry-run` | Remove duplicate word entries from archive. Keeps the one with highest reviewCount. Backs up to `history/` first. **Always dry-run first** |
| `stats` | `--days <N>` | Show statistics for last N days |
| `save-lesson` | `--file <path> --name <name>` | Save knowledge doc to COS |
| `list-knowledge` | (none) | List all knowledge documents in COS |
| `get-knowledge` | `--name <filename>` | Download a knowledge document from COS |
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
