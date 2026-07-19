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
~/.workbuddy/skills/jrp/bin/jrp
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
│   ├── plans/       # Review plan JSONs + Excel backups
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
| 5+ | 15 days |

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
| ☠️钉子户 | ErrorCount >= 3 | 0 (highest) |
| 🔴待巩固 | errorRate >= 30%, ErrorCount < 3 | 1 |
| 🔄待测试 | reviewCount == 0 | 2 |
| 🟡基本掌握 | reviewed but not mastered | 3 |
| 🟢抽查 | reviewCount >= 5, errorRate < 15% | 4 (lowest) |

Words are sorted by priority and grouped into separate sections in the Excel.
Each section has a title row (e.g., "☠️钉子户 54词") and column headers with gray background (D9D9D9).
序号 cells contain plain numbers (no emoji); status is conveyed by the section title.
Continuous numbering across all sections.

### Excel Layout (matches IMA version)

- **Sheet names**: `✏️练习版` (practice) / `✅答案版` (answers)
- **6 columns, no gap**: A(序号,5) B(中文,17) C(日语,20.5) D(序号,5) E(中文,17) F(日语,22.5)
- **Per-category sections**: Each non-empty category gets its own section with title + header + word rows
- **Word rows**: Two-column layout (left A/B/C, right D/E/F), 序号 has gray bg + center align
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
1. Run: `jrp --lang ja gen-plan` (defaults to today) or `jrp --lang ja gen-plan --date 2026-07-18` (for a specific date)
   - If due_count is 0, inform the user — no review needed for that date
   - If no archive exists for the target date, gen-plan auto-initializes today's v1.0 archive (with a changelog entry "新日初始化（gen-plan）") before generating the plan, so the Excel uses v1.0 instead of inheriting the previous day's version
2. Read the due words from the JSON output
3. Read knowledge points from COS for grammar points from recent lessons:
   - `jrp --lang ja list-knowledge` to see available lessons
   - `jrp --lang ja get-knowledge --name <filename>` to fetch a lesson's content
   - (Fallback: IMA MCP tools if a lesson is not yet in COS)
4. Generate 10+ sentence exercises:
   - Each sentence uses grammar points from learned lessons
   - Each sentence's Chinese translation is provided for the user to translate
   - The target language answer is the reference
   - Cover variety: different grammar patterns, different lesson topics
5. Save sentences to a JSON file:

```json
[
  {"chinese": "今天天气很好", "answer": "今日はいい天気ですね"},
  {"chinese": "我喜欢吃寿司", "answer": "私はすしが好きです"}
]
```

6. Run: `jrp --lang ja gen-plan --date YYYY-MM-DD --sentences /tmp/sentences.json`
   - Default output: `outputs/review_YYYY-MM-DD_vA.B.xlsx` (version auto-parsed from archive)
   - **Always copy the final xlsx to the workspace `outputs/` directory** before present_files
7. Present the Excel file to the user using present_files (path must be in workspace `outputs/`)

**Excel structure**:
- Sheet names: `✏️练习版` / `✅答案版`
- Words grouped by status section: ☠️钉子户 → 🔴待巩固 → 🟡基本掌握 → 🟢抽查 → 🔄待测试
- 6-column layout: 序号 | 中文释义 | 目标语言 | 序号 | 中文释义 | 目标语言
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
5. Report: how many correct/wrong, updated stats, new version

### 5. Update Word Definition

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

### 6. Show Statistics

**Trigger**: User asks for stats, learning progress, 最近7天 etc.

**Steps**:
1. Run: `jrp --lang ja stats --days 7`
2. Present the JSON output as a readable summary (table or chart)

### 7. Save Lesson Knowledge Document

**Trigger**: User sends textbook photos for knowledge extraction.

**Steps**:
1. Read photos — extract lesson text, grammar points, example sentences
2. Format as a knowledge document following the existing template:

```markdown
# 标准日本语初级上册 第N课 知识点

## 本课主题
## 一、基本课文（4句，日文+中文翻译）
## 二、语法解释（编号列出，每条带例句）
## 三、表达及词语讲解要点
## 四、应用课文（场景对话）
## 五、易错助词重点（表格）
复习造句重点
```

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

## Language Codes

| Code | Language | Archive Prefix | IMA Knowledge Base |
|---|---|---|---|
| ja | 日语 | 日语学习进度档案 | 自学日语 (7452509467574409) |
| en | 英语 | 英语学习进度档案 | 英文知识库 |
| fr | 法语 | 法语学习进度档案 | (to be created) |

## Binary Path

```
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
| `record` | `--input <json>` | Record review results |
| `update-def` | `--input <json>` | Update word definition |
| `stats` | `--days <N>` | Show statistics for last N days |
| `save-lesson` | `--file <path> --name <name>` | Save knowledge doc to COS |
| `list-knowledge` | (none) | List all knowledge documents in COS |
| `get-knowledge` | `--name <filename>` | Download a knowledge document from COS |
