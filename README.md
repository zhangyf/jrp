# JRP - Language Review Planner

Ebbinghaus-based vocabulary review system with Excel generation, supporting Japanese/English/French.

## Overview

JRP is a Go CLI tool that manages vocabulary learning archives using the Ebbinghaus forgetting curve method. It generates daily review Excel files, records review results, and tracks progress over time.

## Features

- **Multi-language support**: Japanese (ja), English (en), French (fr)
- **Ebbinghaus intervals**: 1d → 2d → 4d → 7d → 10d → 15d
- **Excel generation**: Two-sheet format (review + answers) with sentence exercises
- **Version management**: Automatic file naming with major.minor versioning
- **COS storage**: Archives stored in Tencent Cloud COS via [objstore](https://github.com/zhangyf/objstore)
- **Automated workflows**: Each command is a complete flow (download → process → upload)

## Installation

```bash
go install github.com/zhangyf/jrp@latest
```

## Commands

```bash
# Import existing archive (initial migration from IMA)
jrp --lang ja import < archive.md

# Add new words
jrp --lang ja add-words --input words.json

# Generate today's review Excel
jrp --lang ja gen-plan --output /tmp/review.xlsx

# Record review results
jrp --lang ja record --input results.json

# Update a word's definition
jrp --lang ja update-def --input def.json

# Show statistics for last 7 days
jrp --lang ja stats --days 7

# Save a knowledge document
jrp --lang ja save-lesson --file lesson.md --name 第9课知识点.md
```

## Archive Format

```
日语学习进度档案_YYMMDD_vA.B.md
```

- Each day starts with v1.0
- Each update increments minor version (v1.0 → v1.1 → v1.2)
- Major version bumps on: format changes, 20+ word imports, or user request

## COS Storage Layout

```
language-review/
├── ja/
│   ├── archives/     # Current archives
│   ├── history/      # Historical snapshots
│   ├── plans/        # Review plan JSONs + Excel backups
│   └── knowledge/    # Lesson knowledge documents
├── en/
└── fr/
```

## Dependencies

- [objstore](https://github.com/zhangyf/objstore) - Unified COS/S3 client
- [excelize](https://github.com/xuri/excelize) - Excel file generation
