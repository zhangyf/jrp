// storage.go —— Storage 全部方法与配套类型（存档 / 草稿 / 快照 / 配置 / 用户表 / 词元数据）
//
// 腿一 R2：把原本平铺在根目录的 7 个 api_*.go / storage*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；HTTP 响应、存档、Excel 字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找 handler 先看下面的来源清单。
//
// 来源与顶层声明：
// storage.go: cosSkillDir, Storage, SetDryRun, IsDryRun, NewStorage, loadCOSConfig, deriveEnvKey, normalizeSkillDir, encryptEnvFile, decryptEnvFile, ... (共 43 个顶层声明)
// storage_draft.go: Draft, DraftJudge, DraftItem, HasContent, draftKey, UploadDraft, DownloadDraft, DeleteDraft
// storage_judgeprompt.go: judgePromptKey, DownloadJudgePrompt, UploadJudgePrompt, DeleteJudgePrompt
// storage_planconfig.go: planConfigKey, DownloadPlanConfig, UploadPlanConfig, DeletePlanConfig
// storage_review.go: ReviewSnapshot, ReviewItem, reviewKey, UploadReview, mergeReview, DownloadReview, DeleteReview, validReviewMode
// storage_users.go: User, userTable, authBase, usersKey, ListUsers, SaveUsers, cachedUsers, usersUnknown, invalidateUserCache, findUserByUID, ... (共 17 个顶层声明)
// storage_words.go: wordMetaKey, DownloadWordMeta, UploadWordMeta
package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/stats"
	"github.com/zhangyf/objstore"
)

// ==========================================================================
// 原 storage.go
// ==========================================================================

// cosSkillDir returns the directory where the encrypted .env.enc is stored.
//
// Resolution order:
//  1. JRP_COS_SKILL_DIR env var (explicit override)
//  2. $HOME/.workbuddy/cos-credentials — if it actually holds a .env or .env.enc.
//     This is the durable home: it lives OUTSIDE every marketplace-managed skill
//     directory, so skill updates cannot wipe it (that happened 3×; the
//     2026-09-10 update destroyed the copy under skills/tencentcloud-cos).
//  3. $HOME/.workbuddy/skills/tencentcloud-cos — legacy location, kept as fallback
//     so a machine that has not been migrated yet still works.
//
// Step 2 is what makes the credential location platform-independent: neither the
// Windows work machine nor the macOS home machine needs to export anything, and
// non-interactive shells (which skip .zshrc/.bash_profile) still resolve it.
func cosSkillDir() string {
	if dir := os.Getenv("JRP_COS_SKILL_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".workbuddy", "skills", "tencentcloud-cos")
	}

	migrated := filepath.Join(home, ".workbuddy", "cos-credentials")
	for _, name := range []string{".env", ".env.enc"} {
		if _, err := os.Stat(filepath.Join(migrated, name)); err == nil {
			return migrated
		}
	}
	return filepath.Join(home, ".workbuddy", "skills", "tencentcloud-cos")
}

// Storage wraps an objstore.Store for archive operations.
type Storage struct {
	store objstore.Store
	lang  string

	// uid 绑定的用户。空串 = owner（站点所有者），其个人数据直接落在语言根下，
	// 也就是 2026-10 之前那份老数据的位置 —— 这样加账号不用迁移任何东西。
	// 非空 = 普通成员，个人数据落在 <语言根>/users/<uid>/ 下。
	uid string

	// dryRun 为 true 时所有 Upload* 变成空操作，读取完全不受影响。
	// 用于 serve 的演练模式：判分、序号匹配、版本号 bump 照常计算，
	// 只是不落盘 —— 验收时随便点都不会脏档案。
	dryRun bool
}

// SetDryRun 打开或关闭演练模式。
func (s *Storage) SetDryRun(v bool) { s.dryRun = v }

// IsDryRun 报告当前是否处于演练模式。
func (s *Storage) IsDryRun() bool { return s.dryRun }

// NewStorage creates a Storage instance with credentials loaded from
// environment variables or the encrypted .env.enc file.
func NewStorage(lang string) (*Storage, error) {
	cfg, err := loadCOSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load COS config: %w", err)
	}

	store, err := objstore.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create objstore: %w", err)
	}

	return &Storage{store: store, lang: lang}, nil
}

// loadCOSConfig loads COS credentials from env vars or .env.enc.
func loadCOSConfig() (objstore.Config, error) {
	// Try environment variables first
	secretID := os.Getenv("TENCENT_COS_SECRET_ID")
	secretKey := os.Getenv("TENCENT_COS_SECRET_KEY")
	region := os.Getenv("TENCENT_COS_REGION")
	bucket := os.Getenv("TENCENT_COS_BUCKET")

	if secretID != "" && secretKey != "" && region != "" && bucket != "" {
		return objstore.Config{
			Provider:  objstore.ProviderCOS,
			Bucket:    bucket,
			Region:    region,
			SecretID:  secretID,
			SecretKey: secretKey,
		}, nil
	}

	// Fall back to .env.enc
	skillDir := cosSkillDir()
	encPath := filepath.Join(skillDir, ".env.enc")
	encData, err := os.ReadFile(encPath)

	var plaintext string
	if err == nil {
		plaintext, err = decryptEnvFile(encData, skillDir)
		if err != nil {
			return objstore.Config{}, fmt.Errorf("failed to decrypt .env.enc: %w", err)
		}
	} else {
		// No .env.enc (or unreadable) — try a plaintext .env in the same dir.
		// This is what makes a half-finished migration still work: drop the
		// plaintext master copy in ~/.workbuddy/cos-credentials/.env and jrp
		// runs even before `encrypt-env` has been re-run there.
		raw, perr := os.ReadFile(filepath.Join(skillDir, ".env"))
		if perr != nil {
			return objstore.Config{}, fmt.Errorf("no env vars set, cannot read .env.enc (%v) and no plaintext .env: %w", err, perr)
		}
		plaintext = string(raw)
	}

	envVars := parseEnvFile(plaintext)

	if envVars["TENCENT_COS_SECRET_ID"] == "" || envVars["TENCENT_COS_SECRET_KEY"] == "" {
		return objstore.Config{}, fmt.Errorf("credentials not found in %s", skillDir)
	}

	return objstore.Config{
		Provider:  objstore.ProviderCOS,
		Bucket:    envVars["TENCENT_COS_BUCKET"],
		Region:    envVars["TENCENT_COS_REGION"],
		SecretID:  envVars["TENCENT_COS_SECRET_ID"],
		SecretKey: envVars["TENCENT_COS_SECRET_KEY"],
	}, nil
}

// deriveEnvKey derives the AES-256 key from machine identity, matching the
// Node.js side (cos_node.mjs): SHA-256(hostname + ":" + username + ":" + skillDir).
//
// username MUST match os.userInfo().username semantics, NOT user.Current().Username:
//   - Windows: os.userInfo().username is the bare username (e.g. "efrainzhang"),
//     while user.Current().Username prefixes "HOSTNAME\\". Use the USERNAME env
//     var to match. This mismatch was the root cause of "decryption failed".
//   - Unix: fall back to user.Current().Username (same as os.userInfo()).
func deriveEnvKey(skillDir string) []byte {
	hostname, _ := os.Hostname()
	username := os.Getenv("USERNAME")
	if username == "" {
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
	}
	seed := fmt.Sprintf("%s:%s:%s", hostname, username, normalizeSkillDir(skillDir))
	key := sha256.Sum256([]byte(seed))
	return key[:]
}

// normalizeSkillDir canonicalizes a skill directory before it is fed into the
// key derivation. This matters: the key is SHA-256(hostname:username:skillDir)
// over the *string*, so any spelling difference produces a different key and
// the file becomes undecryptable — with no way to tell "wrong path" from
// "corrupt file".
//
// Real-world breakages this fixes (2026-09-20):
//   - "C:/Users/x/.workbuddy/cos-credentials" vs "C:\Users\x\.workbuddy\cos-credentials"
//     → same directory, different key. Happens constantly because the dir comes
//     from an env var on one path (forward slashes, as typed in bash) and from
//     filepath.Join on the other (backslashes on Windows).
//   - A trailing separator, or "./" prefixes, likewise change the key.
//
// Canonical form: cleaned absolute-ish path with forward slashes only.
// Windows and macOS still derive different keys (different home paths), which is
// correct — the encrypted file is machine-local; move the plaintext .env instead.
//
// ⚠️ Changing this invalidates every existing .env.enc: re-run `encrypt-env`
// afterwards (the plaintext .env is the master copy, so this is lossless).
func normalizeSkillDir(skillDir string) string {
	if abs, err := filepath.Abs(skillDir); err == nil {
		skillDir = abs
	}
	return filepath.ToSlash(filepath.Clean(skillDir))
}

// encryptEnvFile encrypts plaintext into the same format as cos_node.mjs:
// iv(12) + authTag(16) + ciphertext. Cross-compatible with the Node decryptor.
func encryptEnvFile(plaintext string, skillDir string) ([]byte, error) {
	key := deriveEnvKey(skillDir)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}
	// gcm.Seal returns ciphertext || authTag; reorder to iv || authTag || ciphertext.
	sealed := gcm.Seal(nil, iv, []byte(plaintext), nil)
	tagLen := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagLen]
	authTag := sealed[len(sealed)-tagLen:]

	out := make([]byte, 0, len(iv)+tagLen+len(ciphertext))
	out = append(out, iv...)
	out = append(out, authTag...)
	out = append(out, ciphertext...)
	return out, nil
}

// decryptEnvFile decrypts the .env.enc file using the same algorithm as cos_node.mjs.
// Format: iv(12) + authTag(16) + ciphertext
func decryptEnvFile(encData []byte, skillDir string) (string, error) {
	if len(encData) < 28 {
		return "", fmt.Errorf("encrypted data too short")
	}

	key := deriveEnvKey(skillDir)

	// Extract IV, authTag, ciphertext
	iv := encData[:12]
	authTag := encData[12:28]
	ciphertext := encData[28:]

	// Decrypt
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, iv, append(ciphertext, authTag...), nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong machine/user?): %w", err)
	}

	return string(plaintext), nil
}

// parseEnvFile parses .env file content into a map.
func parseEnvFile(content string) map[string]string {
	vars := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Remove surrounding quotes
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = val[1 : len(val)-1]
		}
		vars[key] = val
	}
	return vars
}

// ============================================================================
// Archive operations
// ============================================================================

func (s *Storage) cosPrefix() string {
	return LangConfigs[s.lang].COSPrefix
}

// ForUser 返回绑定了 uid 的副本。store 是共享的 HTTP client + 凭证，
// 这里只是复制一个小 struct，所以每个请求调一次也没有开销。
//
// 之所以不改 NewStorage 的签名：全仓库 26 处调用里 25 处在 CLI 中，
// 保持签名不变 ⇒ CLI 拿到的 uid 天然是 ""（owner），一行都不用动。
func (s *Storage) ForUser(uid string) *Storage {
	return &Storage{store: s.store, lang: s.lang, uid: uid, dryRun: s.dryRun}
}

// UserID 返回绑定的用户（空串 = owner）。
func (s *Storage) UserID() string { return s.uid }

// IsOwner 报告这是不是站点所有者的存储。
func (s *Storage) IsOwner() bool { return s.uid == "" }

// personalBase 个人进度数据的根：档案、历史版本、plan、草稿、回看快照、
// 以及练习痕迹（sentence_history / sentence_wrong）。
//
// ⚠️ owner 的前缀必须为空 —— 这是「零迁移」的全部秘密：
// 老师今天在用的 language-review/ja/archives/ 一个字符都不用动。
func (s *Storage) personalBase() string {
	if s.uid == "" {
		return s.cosPrefix()
	}
	return s.cosPrefix() + "/users/" + s.uid
}

// sharedBase 教材与配置的根：句库、回炉变形句库、知识点文档、词性表、
// 出题配额、判句模板。全站一份，不随用户变 —— 这些是手工维护的教材，
// 按人复制一份等于以后改一句要改两遍。
//
// ⚠️ 注意 plans/ 目录下 personal 与 shared 混住（sentence_bank.json 就在
// plans/ 下，而 plan_<date>.json 也是），所以只能按函数区分，不能按目录切。
func (s *Storage) sharedBase() string { return s.cosPrefix() }

// DownloadLatestArchive downloads the most recent archive from COS.
func (s *Storage) DownloadLatestArchive(ctx context.Context) ([]byte, string, error) {
	prefix := s.personalBase() + "/archives/"
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, "", fmt.Errorf("failed to list archives: %w", err)
	}
	if len(objs) == 0 {
		return nil, "", fmt.Errorf("no archives found in %s", prefix)
	}

	// Sort by LastModified descending
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].LastModified.After(objs[j].LastModified)
	})

	latest := objs[0]
	// Extract just the filename from the full key
	filename := filepath.Base(latest.Key)

	data, err := s.store.GetAll(ctx, latest.Key)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download %s: %w", latest.Key, err)
	}

	return data, filename, nil
}

// UploadArchive uploads an archive to COS.
func (s *Storage) UploadArchive(ctx context.Context, filename string, data []byte) error {
	if s.dryRun {
		return nil
	}
	key := s.personalBase() + "/archives/" + filename
	return s.store.PutObject(ctx, key, data)
}

// UploadHistory uploads a historical archive to COS.
func (s *Storage) UploadHistory(ctx context.Context, filename string, data []byte) error {
	if s.dryRun {
		return nil
	}
	key := s.personalBase() + "/history/" + filename
	return s.store.PutObject(ctx, key, data)
}

// planJSONKey builds the COS key for a plan JSON.
//   - kind == "hard"      → plans/hard_<date>.json
//   - kind == "sentences" → plans/sentences_<date>.json
//   - otherwise           → plans/plan_<date>.json
//
// The three must never collide: a hard-word export or a sentences-only plan on
// the same day as a daily review would otherwise overwrite the daily plan and
// break `record` (word numbers would resolve to nothing).
func (s *Storage) planJSONKey(kind, date string) string {
	switch kind {
	case "hard":
		return fmt.Sprintf("%s/plans/hard_%s.json", s.personalBase(), date)
	case "sentences":
		return fmt.Sprintf("%s/plans/sentences_%s.json", s.personalBase(), date)
	}
	return fmt.Sprintf("%s/plans/plan_%s.json", s.personalBase(), date)
}

// UploadPlan uploads a review plan JSON to COS.
// deleteObjectIgnore404 删 COS 对象，不存在当成功。
// 清 plan / 清草稿 / 清模板都是这个语义：删一个本来就没有的东西不算失败。
func (s *Storage) deleteObjectIgnore404(ctx context.Context, key string) error {
	err := s.store.DeleteObject(ctx, key)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "404") ||
		strings.Contains(err.Error(), "NoSuchKey") {
		return nil
	}
	return err
}

func (s *Storage) UploadPlan(ctx context.Context, plan *ReviewPlan) error {
	if s.dryRun {
		return nil
	}
	key := s.planJSONKey("", plan.Date)
	data := []byte(toJSON(plan))
	return s.store.PutObject(ctx, key, data)
}

// DownloadPlan downloads a review plan JSON from COS.
func (s *Storage) DownloadPlan(ctx context.Context, planDate string) (*ReviewPlan, error) {
	key := s.planJSONKey("", planDate)
	data, err := s.store.GetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download plan: %w", err)
	}
	var plan ReviewPlan
	if err := jsonUnmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan: %w", err)
	}
	return &plan, nil
}

// UploadHardPlan uploads a hard-word plan JSON to COS.
func (s *Storage) UploadHardPlan(ctx context.Context, plan *ReviewPlan) error {
	if s.dryRun {
		return nil
	}
	key := s.planJSONKey("hard", plan.Date)
	data := []byte(toJSON(plan))
	return s.store.PutObject(ctx, key, data)
}

// DownloadHardPlan downloads a hard-word plan JSON from COS.
func (s *Storage) DownloadHardPlan(ctx context.Context, planDate string) (*ReviewPlan, error) {
	key := s.planJSONKey("hard", planDate)
	data, err := s.store.GetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download hard plan: %w", err)
	}
	var plan ReviewPlan
	if err := jsonUnmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("failed to parse hard plan: %w", err)
	}
	return &plan, nil
}

// UploadSentencePlan uploads a sentences-only plan JSON to COS (kind
// "sentences"). Kept in its own key so it never clobbers the daily plan.
func (s *Storage) UploadSentencePlan(ctx context.Context, plan *ReviewPlan) error {
	if s.dryRun {
		return nil
	}
	key := s.planJSONKey("sentences", plan.Date)
	data := []byte(toJSON(plan))
	return s.store.PutObject(ctx, key, data)
}

// DeleteSentencePlan 删掉某天已出过的造句 plan。
//
// 造句的换题时机是「提交之后」，不是「刷新之后」：plan 一旦出过就锁在 COS 上，
// 同一天反复刷新、换设备打开都是同一批；只有回写成功才把它删掉，
// 让下一次 GET /api/plan?mode=sentences 重新出题。
//
// 对象不存在时 COS 返回 404，这里一律当成功 —— 删不掉最坏只是沿用旧的一批。
func (s *Storage) DeleteSentencePlan(ctx context.Context, planDate string) error {
	if s.dryRun {
		return nil
	}
	_ = s.store.DeleteObject(ctx, s.planJSONKey("sentences", planDate))
	return nil
}

// DownloadSentencePlan downloads a sentences-only plan JSON from COS.
func (s *Storage) DownloadSentencePlan(ctx context.Context, planDate string) (*ReviewPlan, error) {
	key := s.planJSONKey("sentences", planDate)
	data, err := s.store.GetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download sentence plan: %w", err)
	}
	var plan ReviewPlan
	if err := jsonUnmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("failed to parse sentence plan: %w", err)
	}
	return &plan, nil
}

// UploadExcel uploads an Excel file to COS.
func (s *Storage) UploadExcel(ctx context.Context, date string, major, minor int, localPath string) error {
	if s.dryRun {
		return nil
	}
	key := fmt.Sprintf("%s/plans/review_%s_v%d.%d.xlsx", s.personalBase(), date, major, minor)
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read excel file: %w", err)
	}
	return s.store.PutObject(ctx, key, data)
}

// UploadSentenceExcel uploads a sentences-only Excel file to COS.
func (s *Storage) UploadSentenceExcel(ctx context.Context, date string, major, minor int, localPath string) error {
	if s.dryRun {
		return nil
	}
	key := fmt.Sprintf("%s/plans/sentences_%s_v%d.%d.xlsx", s.personalBase(), date, major, minor)
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read excel file: %w", err)
	}
	return s.store.PutObject(ctx, key, data)
}

// UploadHardExcel uploads a hard-word Excel file to COS.
func (s *Storage) UploadHardExcel(ctx context.Context, date string, major, minor int, localPath string) error {
	if s.dryRun {
		return nil
	}
	key := fmt.Sprintf("%s/plans/hard_words_%s_v%d.%d.xlsx", s.personalBase(), date, major, minor)
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read excel file: %w", err)
	}
	return s.store.PutObject(ctx, key, data)
}

// UploadKnowledge uploads a knowledge document to COS.
func (s *Storage) UploadKnowledge(ctx context.Context, filename string, data []byte) error {
	if s.dryRun {
		return nil
	}
	key := s.sharedBase() + "/knowledge/" + filename
	return s.store.PutObject(ctx, key, data)
}

// ListKnowledge lists all knowledge documents in COS, sorted by key ascending.
func (s *Storage) ListKnowledge(ctx context.Context) ([]objstore.ObjectInfo, error) {
	prefix := s.sharedBase() + "/knowledge/"
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].Key < objs[j].Key
	})
	return objs, nil
}

// DownloadKnowledge downloads a specific knowledge document by filename.
func (s *Storage) DownloadKnowledge(ctx context.Context, name string) ([]byte, error) {
	key := s.sharedBase() + "/knowledge/" + name
	return s.store.GetAll(ctx, key)
}

// ListHistoryArchives lists all historical archives in COS.
func (s *Storage) ListHistoryArchives(ctx context.Context) ([]objstore.ObjectInfo, error) {
	prefix := s.personalBase() + "/history/"
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].LastModified.Before(objs[j].LastModified)
	})
	return objs, nil
}

// DownloadHistoryArchive downloads a specific historical archive.
func (s *Storage) DownloadHistoryArchive(ctx context.Context, key string) ([]byte, error) {
	return s.store.GetAll(ctx, key)
}

// DownloadArchiveByDate downloads the latest archive for a specific date.
func (s *Storage) DownloadArchiveByDate(ctx context.Context, dateStr string) ([]byte, string, error) {
	prefix := s.personalBase() + "/archives/"
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, "", err
	}

	// Find the latest version for the given date
	var latest objstore.ObjectInfo
	found := false
	for _, obj := range objs {
		filename := filepath.Base(obj.Key)
		if strings.Contains(filename, dateStr) {
			if !found || obj.LastModified.After(latest.LastModified) {
				latest = obj
				found = true
			}
		}
	}
	if !found {
		return nil, "", fmt.Errorf("no archive found for date %s", dateStr)
	}

	data, err := s.store.GetAll(ctx, latest.Key)
	if err != nil {
		return nil, "", err
	}
	return data, filepath.Base(latest.Key), nil
}

// ListAllArchives lists all archives sorted by date descending.
func (s *Storage) ListAllArchives(ctx context.Context) ([]objstore.ObjectInfo, error) {
	prefix := s.personalBase() + "/archives/"
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].LastModified.After(objs[j].LastModified)
	})
	return objs, nil
}

// ListArchiveRefs 列出所有档案引用（含解析出的日期），供统计用例使用。
// 之前 computeStats 直接用 ListAllArchives 拿 []objstore.ObjectInfo，
// 把底层对象存储类型渗进了用例签名；收口到本方法后，统计用例只认
// stats.ArchiveRef（Key / Filename / Date），不必认识 objstore。
func (s *Storage) ListArchiveRefs(ctx context.Context) ([]stats.ArchiveRef, error) {
	objs, err := s.ListAllArchives(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]stats.ArchiveRef, 0, len(objs))
	for _, obj := range objs {
		fname := obj.Key
		if idx := strings.LastIndex(fname, "/"); idx >= 0 {
			fname = fname[idx+1:]
		}
		fdate, _, _, err := archive.ParseFilename(fname)
		if err != nil {
			continue
		}
		refs = append(refs, stats.ArchiveRef{Key: obj.Key, Filename: fname, Date: fdate})
	}
	return refs, nil
}

// ReadArchiveBlob 按完整 COS key 读取一份档案原文。
// 之前 computeStats 直接调 s.store.GetAll，把底层对象存储泄漏进了业务用例；
// 收敛到这个公开方法后，统计用例只需依赖 *Storage 的方法面，
// 后面拆进用例层时可用内存 fake 替身，不必认识 objstore.Store。
func (s *Storage) ReadArchiveBlob(ctx context.Context, key string) ([]byte, error) {
	return s.store.GetAll(ctx, key)
}

// FindLatestArchiveBeforeDate finds the most recent archive on or before the given date.
func (s *Storage) FindLatestArchiveBeforeDate(ctx context.Context, before time.Time) (string, time.Time, error) {
	objs, err := s.ListAllArchives(ctx)
	if err != nil {
		return "", time.Time{}, err
	}

	// Parse dates from filenames and find the latest one before 'before'
	dateStr := before.Format("060102")
	var latestKey string
	var latestDate time.Time

	for _, obj := range objs {
		filename := filepath.Base(obj.Key)
		fnameDate, _, _, err := archive.ParseFilename(filename)
		if err != nil {
			continue
		}
		if fnameDate.Before(before) || fnameDate.Format("060102") == dateStr {
			if latestKey == "" || fnameDate.After(latestDate) {
				latestKey = obj.Key
				latestDate = fnameDate
			}
		}
	}

	if latestKey == "" {
		return "", time.Time{}, fmt.Errorf("no archive found before %s", before.Format("2006-01-02"))
	}

	return latestKey, latestDate, nil
}

// extractDateFromFilename extracts YYMMDD from archive filename.
func extractDateFromFilename(filename string) string {
	re := regexp.MustCompile(`_(\d{6})_v`)
	matches := re.FindStringSubmatch(filename)
	if matches != nil {
		return matches[1]
	}
	return ""
}

// ==========================================================================
// 原 storage_draft.go
// ==========================================================================

// Draft 是一次「写了一半还没回写」的练习快照。
//
// 存在 COS 而不是浏览器 localStorage —— 老师会在手机、家里的 Mac、办公室的 Windows
// 上打开同一个站，写到一半换设备要能接着写。
//
// 只存老师亲手动过的东西：输入的答案、点的「不会」、勾的「算对」。
// **不存判分结果（correct）** —— 它由 (input, word, grade_mode) 完全决定，恢复时重算即可。
// 这样切判分档位后恢复草稿，不会因为存档时的档位不同而带上过期的对错。
type Draft struct {
	Date      string      `json:"date"`
	Mode      string      `json:"mode"`       // words | hard | sentences | offline
	GradeMode string      `json:"grade_mode"` // 存档时的判分档位，仅用于展示
	SavedAt   string      `json:"saved_at"`
	Items     []DraftItem `json:"items"`
}

// DraftJudge 一句的模型判定结论。
//
// V 是判定规则版本：prompt 改了（判得松/紧变了），旧规则算出的结论就不该
// 再被复用 —— 恢复草稿时只认 V 等于当前版本的结论，旧的重新问模型。
// 2026-09-29 起为 2（v1 没有版本号，恢复时一律作废）。
type DraftJudge struct {
	V       int    `json:"v,omitempty"`
	Correct bool   `json:"correct"`
	Reason  string `json:"reason,omitempty"`
}

type DraftItem struct {
	Number  int    `json:"number"`
	Answer  string `json:"answer"`
	Unknown bool   `json:"unknown"`
	Manual  bool   `json:"manual"`
	// Judge 模型判定结果（造句用）。没问过模型就是 null。
	//
	// 存它的理由是「判定不能漂移」：同一句刷新一次换个结论，比没模型更糟。
	// 恢复时只有写的句子跟草稿里的一字不差（归一化后）才认，改过字重新判。
	Judge *DraftJudge `json:"judge,omitempty"`
	// Prompt 题目原文：造句存原句，单词存词形。恢复时用来核对。
	//
	// 造句的 number 只是当天的 1..N 位置号，不是句子的身份。万一同一天
	// 换过一批题（比如手工重跑了 gen-plan），位置对得上、句子却不是同一句，
	// 没有它就会把上一批写的句子糊到下一批上 —— 老师看到的「你写的」
	// 和「正确答案」会驴唇不对马嘴，还容易被当成判分 bug。
	Prompt string `json:"prompt,omitempty"`
}

// HasContent 判断这份草稿有没有实际内容。全空的草稿没必要往 COS 里写，
// 否则老师刚打开页面什么都没填也会留一个垃圾对象。
func (d *Draft) HasContent() bool {
	if d == nil {
		return false
	}
	for _, it := range d.Items {
		if it.Unknown || it.Manual || len(it.Answer) > 0 {
			return true
		}
	}
	return false
}

// draftKey 草稿的 COS 键：drafts/draft_<date>_<mode>.json
//
// 按「日期 + 模式」分键，所以同一天的今日练习和钉子户互不覆盖，
// 隔天的旧草稿也永远不会串到今天。
func (s *Storage) draftKey(date, mode string) string {
	return fmt.Sprintf("%s/drafts/draft_%s_%s.json", s.personalBase(), date, mode)
}

// UploadDraft 写草稿。演练模式下不落盘 —— 跟其余 Upload* 保持一致，
// 这样 --dry-run 验收时能确认「一个字节都没写进 COS」。
func (s *Storage) UploadDraft(ctx context.Context, d *Draft) error {
	if s.dryRun {
		return nil
	}
	if d.SavedAt == "" {
		d.SavedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	return s.store.PutObject(ctx, s.draftKey(d.Date, d.Mode), []byte(toJSON(d)))
}

// DownloadDraft 读草稿。
//
// **不存在、解析失败、日期对不上，一律返回 (nil, nil)** —— 草稿是锦上添花的功能，
// 读不到就当没有，绝不能因此让整个练习页打不开。
func (s *Storage) DownloadDraft(ctx context.Context, date, mode string) (*Draft, error) {
	data, err := s.store.GetAll(ctx, s.draftKey(date, mode))
	if err != nil {
		return nil, nil
	}
	var d Draft
	if err := jsonUnmarshal(data, &d); err != nil {
		return nil, nil
	}
	if d.Date != "" && d.Date != date {
		return nil, nil // 陈年草稿，日期对不上就不恢复
	}
	return &d, nil
}

// DeleteDraft 清草稿（回写成功后调）。
//
// 对象本来就不存在时 COS 会返回 404，这里也当成功 —— 清不掉不影响任何功能，
// 下次 load 也只是恢复不到东西。
func (s *Storage) DeleteDraft(ctx context.Context, date, mode string) error {
	if s.dryRun {
		return nil
	}
	_ = s.store.DeleteObject(ctx, s.draftKey(date, mode))
	return nil
}

// ==========================================================================
// 原 storage_judgeprompt.go
// ==========================================================================

// 造句判定用的 prompt 模板，存在 COS 上 —— 改 prompt 不用改代码、不用重新部署。
//
// 这是老师 2026-10-12 定的：整段模板外置，随时改随时生效。代价是没了编译期
// 保护，所以配了四道护栏（全在 api_judge.go 里）：
//   1. 模板必须含 {answer} 和 {input}，缺一个就判坏，回退内置模板。
//   2. 填完占位符后如果通篇没提 JSON 输出，自动在末尾补上输出格式段
//      —— 否则模型开始自由发挥，parseJudgeReply 抠不出结论，判分全线失灵。
//   3. COS 上没有 / 读失败 / 内容为空 → 用内置模板，练习绝不卡住。
//   4. 结果按 60 秒缓存，别每次判定都打 COS。
//
// 模板改动会让旧结论失效：版本取内容 sha256 前 12 位，草稿里的结论记着它，
// 版本对不上就作废重判（见 api_judge.go 的 promptVersion）。

const judgePromptCosKey = "configs/judge_prompt.txt"

func (s *Storage) judgePromptKey() string {
	return s.sharedBase() + "/" + judgePromptCosKey
}

// DownloadJudgePrompt 读模板。不存在或读失败返回空串 —— 调用方据此回退内置模板。
func (s *Storage) DownloadJudgePrompt(ctx context.Context) (string, error) {
	data, err := s.store.GetAll(ctx, s.judgePromptKey())
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), nil
}

// UploadJudgePrompt 写模板。演练模式下不落盘，跟其余 Upload* 保持一致。
func (s *Storage) UploadJudgePrompt(ctx context.Context, text string) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.judgePromptKey(), []byte(text))
}

// DeleteJudgePrompt 删模板（--reset），删掉即回退到内置模板。
// 对象本来就不存在时 COS 返回 404，这里也当成功。
func (s *Storage) DeleteJudgePrompt(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.judgePromptKey())
}

// ---- 字幕精校 prompt 外置（照 judge prompt 同一套，key 换成 configs/refine_prompt.txt）----
//
// 精校模板跟判句模板的区别：判句模板有 {answer}/{input} 占位符，精校模板没有
// （字幕是拼在模板后面的）。所以这里只有「读/写/删」三件套，校验逻辑在
// listening.ValidateRefinePrompt（只要求非空 + 含「编号」）。

const refinePromptCosKey = "configs/refine_prompt.txt"

func (s *Storage) refinePromptKey() string {
	return s.sharedBase() + "/" + refinePromptCosKey
}

// DownloadRefinePrompt 读精校模板。不存在或读失败返回空串，调用方据此回退内置。
func (s *Storage) DownloadRefinePrompt(ctx context.Context) (string, error) {
	data, err := s.store.GetAll(ctx, s.refinePromptKey())
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), nil
}

// UploadRefinePrompt 写精校模板。演练模式下不落盘。
func (s *Storage) UploadRefinePrompt(ctx context.Context, text string) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.refinePromptKey(), []byte(text))
}

// DeleteRefinePrompt 删精校模板（--reset），删掉即回退内置。404 也当成功。
func (s *Storage) DeleteRefinePrompt(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.refinePromptKey())
}

// ---- 字幕精校旋钮外置（configs/refine_config.json）----
//
// 2026-11-06 老师提的：批次上限/超时/模型不该硬编码。跟 plan_config.json 同一套，
// 读不到 / 解析坏 / 越界都由 listening.ChooseRefineConfig 回退内置默认。

const refineConfigCosKey = "configs/refine_config.json"

func (s *Storage) refineConfigKey() string {
	return s.sharedBase() + "/" + refineConfigCosKey
}

// DownloadRefineConfig 读配置原始 JSON。不存在或读失败返回空，调用方回退内置。
func (s *Storage) DownloadRefineConfig(ctx context.Context) ([]byte, error) {
	data, err := s.store.GetAll(ctx, s.refineConfigKey())
	if err != nil {
		return nil, nil
	}
	return data, nil
}

// UploadRefineConfig 写配置。演练模式下不落盘。
func (s *Storage) UploadRefineConfig(ctx context.Context, data []byte) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.refineConfigKey(), data)
}

// DeleteRefineConfig 删配置（--reset），删掉即回退内置。404 也当成功。
func (s *Storage) DeleteRefineConfig(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.refineConfigKey())
}

// ==========================================================================
// 原 storage_planconfig.go
// ==========================================================================

// 出题配额配置，存在 COS 上 —— 改配额不用改代码、不用重新部署。
//
// 2026-10-13 老师提的：改个「每天几句回炉句」都要重新编译上线 + 推 GitHub，太重。
// judge prompt 已经趟过一遍外置的路子（configs/judge_prompt.txt），这里照抄同一套：
// 读不到 / 解析坏 / 值越界一律回退内置默认值，练习绝不会因为一份坏配置卡住。
//
// 缓存 60 秒（见 plan_config.go 的 resolvePlanConfig），改完配置最多 60 秒生效，
// 不用重启服务。

const planConfigCosKey = "configs/plan_config.json"

// PlanConfig 类型与解析/校验/回退规则已迁 internal/planconfig，根 types.go 只留别名。
// 本文件只做 COS 读写，规则不在本文件。

func (s *Storage) planConfigKey() string { return s.sharedBase() + "/" + planConfigCosKey }

// DownloadPlanConfig 读配置。对象不存在或读失败返回零值（=没配），不返回 error ——
// 「没配」是正常状态，不是故障。
func (s *Storage) DownloadPlanConfig(ctx context.Context) (PlanConfig, error) {
	data, err := s.store.GetAll(ctx, s.planConfigKey())
	if err != nil {
		return PlanConfig{}, nil
	}
	return planconfig.ParsePlanConfig(data)
}

// UploadPlanConfig 写配置。演练模式下不落盘，跟其余 Upload* 保持一致。
func (s *Storage) UploadPlanConfig(ctx context.Context, c PlanConfig) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.planConfigKey(), []byte(toJSON(c)))
}

// DeletePlanConfig 删配置（--reset），删掉即回退内置默认。
// 对象本来就不存在时 COS 返回 404，这里也当成功。
func (s *Storage) DeletePlanConfig(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.planConfigKey())
}

// ==========================================================================
// 原 storage_review.go
// ==========================================================================

// ReviewSnapshot 是「某天已经练完、已回写的那批词」的只读快照。
//
// 为什么需要它：档案里每个词只留累计值（ReviewCount / ErrorCount / LastReview），
// **不记录当次是答对还是答错**。所以回写一完成，老师当天写了什么、哪题错了
// 就再也取不回来了 —— 页面也因为到期日被推到未来而显示「今天没有到期的词」。
//
// 跟 Draft 的区别：
//   - Draft  = 没写完的，写完回写成功就删掉
//   - Review = 已写完的，留着给老师回看，当天一直在
type ReviewSnapshot struct {
	Date    string       `json:"date"`
	Mode    string       `json:"mode"` // words | hard
	SavedAt string       `json:"saved_at"`
	Items   []ReviewItem `json:"items"`
}

type ReviewItem struct {
	Number     int    `json:"number"`
	Word       string `json:"word"`
	Definition string `json:"definition"`
	Group      string `json:"group,omitempty"`
	Status     string `json:"status,omitempty"` // 当时的掌握状态，如 🔴待巩固
	Answer     string `json:"answer"`           // 老师写的答案，错题回看主要看这个
	Correct    bool   `json:"correct"`
	Unknown    bool   `json:"unknown"` // 主动点了「不会」
	Manual     bool   `json:"manual"`  // 老师勾了「算对」
	Blank      bool   `json:"blank"`   // 空着没写、也没标「不会」—— 没进档案，但要能看出来
	// 词性 / 备注 / 圆级不存快照：随时可能补录，存了就是过期数据。
	// /api/review 返回时按词形从 word_meta.json join（enrichReviewMeta）。
	Pos  string `json:"pos,omitempty"`
	Sub  string `json:"sub,omitempty"`
	Note string `json:"note,omitempty"`
	// Circle 三圆手册分级（三圆 / 二圆 / 一圆），回看时也要能看到（2026-11-03）。
	Circle string `json:"circle,omitempty"`
}

// reviewKey 快照的 COS 键：reviews/review_<date>_<mode>.json
func (s *Storage) reviewKey(date, mode string) string {
	return fmt.Sprintf("%s/reviews/review_%s_%s.json", s.personalBase(), date, mode)
}

// UploadReview 合并写入快照。
//
// 合并而不是覆盖：老师可能先练今日练习、再练钉子户，也可能同一批词分两次回写
// （比如写完一半先回写，剩下的写完再回写一次）。按 Number 去重，后写的覆盖先写的。
func (s *Storage) UploadReview(ctx context.Context, snap *ReviewSnapshot) error {
	if s.dryRun || snap == nil || len(snap.Items) == 0 {
		return nil
	}
	if snap.SavedAt == "" {
		snap.SavedAt = time.Now().Format("2006-01-02 15:04:05")
	}

	merged := mergeReview(snap.Items, func() []ReviewItem {
		old, err := s.DownloadReview(ctx, snap.Date, snap.Mode)
		if err != nil || old == nil {
			return nil
		}
		return old.Items
	}())

	snap.Items = merged
	return s.store.PutObject(ctx, s.reviewKey(snap.Date, snap.Mode), []byte(toJSON(snap)))
}

// mergeReview 按 Word 合并，newer 覆盖 old，并保持 old 的相对顺序、
// 把 old 里没有的新词追加在后面。
//
// 为什么按 Word 而不是按 Number：同一天会出好几批 plan（回写成功 → plan 清 →
// 新一批），**每批的题号都从 1 重新编**。按号码合并的话，后一批的 #1 会把前一批
// 的 #1 盖掉 —— 前几批练过的词就这么没了。2026-10-05 当天档案里 83 个词的
// LastReview 是当天，快照里却只剩 44 个词：老师看到的「共 75 / 对 62」是虚的。
// 按词形合并，一天练过哪些词就都能留下来。
//
// 另一个例外：newer 是空条目（blank，没练也没标「不会」）而 old 已经有作答内容时，
// 保留 old。老师中途刷新页面后客户端状态清零，再提交会把整批都发上来，
// 里面早前批次已答的词全变成 blank —— 直接覆盖会把当天已回写的错词抹掉
// （同日事故：当天 10 个错词只剩 7 个）。空条目没有信息量，不该覆盖。
func mergeReview(newer, old []ReviewItem) []ReviewItem {
	if len(old) == 0 {
		return newer
	}
	byWord := make(map[string]ReviewItem, len(newer))
	order := make([]string, 0, len(newer))
	for _, it := range newer {
		if _, seen := byWord[it.Word]; !seen {
			order = append(order, it.Word)
		}
		byWord[it.Word] = it
	}

	out := make([]ReviewItem, 0, len(old)+len(newer))
	for _, it := range old {
		n, ok := byWord[it.Word]
		if !ok {
			out = append(out, it)
			continue
		}
		delete(byWord, it.Word)
		if n.Blank && !it.Blank {
			out = append(out, it) // 空条目不覆盖已答条目
			continue
		}
		out = append(out, n)
	}
	// 剩下的是这一批新出现的词，按它们在本批里的顺序追加
	for _, w := range order {
		if it, ok := byWord[w]; ok {
			out = append(out, it)
		}
	}

	// 按词去重：old 里可能带着历史遗留的重复条目（号码撞车时同一词被写了两遍），
	// 一条词只留一条，保留后来那条（信息更新），位置按首次出现算。
	uniq := make([]ReviewItem, 0, len(out))
	at := make(map[string]int, len(out))
	for _, it := range out {
		if j, ok := at[it.Word]; ok {
			// 后来那条优先，但空条目不能盖掉已答的（同一条规矩，换了个地方）
			if !it.Blank || uniq[j].Blank {
				uniq[j] = it
			}
			continue
		}
		at[it.Word] = len(uniq)
		uniq = append(uniq, it)
	}

	// 号码重排成 1..N 连续：跨批次的号码必然撞车（两批都有 #1），留着会让
	// 前端按号码索引错位（列表模式的「再练这 N 个错词」就是按号码匹配的）。
	for i := range uniq {
		uniq[i].Number = i + 1
	}
	return uniq
}

// DownloadReview 读快照。不存在 / 解析失败 / 日期对不上 → (nil, nil)，
// 跟草稿一样：回看是锦上添花，读不到就当没练过，不能拖垮整页。
func (s *Storage) DownloadReview(ctx context.Context, date, mode string) (*ReviewSnapshot, error) {
	data, err := s.store.GetAll(ctx, s.reviewKey(date, mode))
	if err != nil {
		return nil, nil
	}
	var snap ReviewSnapshot
	if err := jsonUnmarshal(data, &snap); err != nil {
		return nil, nil
	}
	if snap.Date != "" && snap.Date != date {
		return nil, nil
	}
	return &snap, nil
}

// DeleteReview 清快照。对象不存在（404）也当成功。
func (s *Storage) DeleteReview(ctx context.Context, date, mode string) error {
	if s.dryRun {
		return nil
	}
	_ = s.store.DeleteObject(ctx, s.reviewKey(date, mode))
	return nil
}

// validReviewMode 白名单。模式直接进 COS 键，不放宽会被任意字符串打散。
func validReviewMode(m string) bool {
	switch strings.ToLower(m) {
	case draftModeWords, draftModeHard:
		return true
	}
	return false
}

// ==========================================================================
// 原 storage_users.go
// ==========================================================================

// 用户表。放在语言前缀之外（language-review/_auth/）：一个账号应该能学日语、
// 英语、法语，不该被语言切开。
//
// 存成单个 JSON 而不是每人一个对象：照抄 word_meta.json / sentence_bank.json
// 的范式，用户数就几个人、读写频率极低（只在登录时读、只在新建用户时写）。
// 真到几十人再切成 _auth/users/<uid>.json，改一个文件的事。

const usersCosKey = "_auth/users.json"

// User 一个登录身份。
// Role：
//   - owner   站点所有者（老师）。COS 上个人数据直接落在语言根下，老数据的位置。
//   - member  普通成员（比如儿子），个人数据在 users/<uid>/ 下。
//   - pending 已扫码但未批准：**有 7 个自然日的完整体验期**（跟 member 一样的权限，
//     只是数据在 users/<uid>/ 下）。到期还没被批准就一律 403，下次登录照样挡
//     —— 见 pendingExpired。
//   - disabled 被踢掉的。
type User struct {
	UID       string `json:"uid"`
	OpenID    string `json:"openid"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	Created   string `json:"created"`
	LastLogin string `json:"last_login"`
}

const (
	roleOwner    = "owner"
	roleMember   = "member"
	rolePending  = "pending"
	roleDisabled = "disabled"
)

type userTable struct {
	Version int    `json:"version"`
	Users   []User `json:"users"`
}

// authBase 用户表所在的根目录（语言无关）。
func (s *Storage) authBase() string {
	p := s.cosPrefix()
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i] + "/_auth"
	}
	return "_auth"
}

func (s *Storage) usersKey() string { return s.authBase() + "/users.json" }

// ListUsers 读用户表。COS 读不到（还没人登过）返回空表，不算错误。
func (s *Storage) ListUsers(ctx context.Context) ([]User, error) {
	// store 为 nil 只在测试里出现（dryRun 也要能读真实用户表，只是不写）。
	if s.store == nil {
		return nil, nil
	}
	data, err := s.store.GetAll(ctx, s.usersKey())
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	var t userTable
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return t.Users, nil
}

// SaveUsers 整份写回。写之前重新读一遍 COS（不用缓存）以降低并发丢写：
// 调用方负责持锁，这里的重读是为了避免把别人刚加的用户盖掉。
func (s *Storage) SaveUsers(ctx context.Context, users []User) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.usersKey(), []byte(toJSON(userTable{Version: 1, Users: users})))
}

// ---------------------------------------------------------------------------
// 60 秒缓存：照抄 plan_config.go / api_judge.go 的写法。
// 登录链路每次请求都要判角色，不能每次都打 COS。
// ---------------------------------------------------------------------------

const usersTTL = 60 * time.Second

var userCache = struct {
	sync.Mutex
	at     time.Time
	loaded bool
	users  []User
	// lastErr 记着最近一次读有没有出错。
	//
	// ⚠️ 2026-10-28 安全评审：以前读失败就「当没人登过」，于是 authRequired()
	// 返回 false，匿名请求被当成 owner —— COS 抖一下陌生人就能往老师档案里写。
	// 现在读失败要能被外面看见，好在鉴权那道 fail-closed（见 authRequired）。
	lastErr bool
}{}

func cachedUsers(ctx context.Context, st *Storage) []User {
	userCache.Lock()
	defer userCache.Unlock()
	if userCache.loaded && time.Since(userCache.at) < usersTTL {
		return userCache.users
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		// 读失败就用上一份（没有就当没人登过），绝不因为 COS 抖动把所有人踢下线。
		userCache.lastErr = true
		return userCache.users
	}
	userCache.users = users
	userCache.loaded = true
	userCache.lastErr = false
	userCache.at = time.Now()
	return users
}

// usersUnknown 报告「用户表一次都没读成功过」—— 这时不能假定没人，
// 鉴权必须 fail-closed（匿名一律 401），否则匿名会被当成 owner。
func usersUnknown() bool {
	userCache.Lock()
	defer userCache.Unlock()
	return !userCache.loaded
}

func invalidateUserCache() {
	userCache.Lock()
	userCache.loaded = false
	userCache.at = time.Time{}
	userCache.Unlock()
}

func findUserByUID(users []User, uid string) (User, bool) {
	for _, u := range users {
		if u.UID == uid {
			return u, true
		}
	}
	return User{}, false
}

func findUserByOpenID(users []User, openid string) (User, bool) {
	for _, u := range users {
		if u.OpenID == openid {
			return u, true
		}
	}
	return User{}, false
}

// upsertUser 按 openid 找人，找到就更新登录时间，没找到就新建。
// 第一个登录的人自动成为 owner（阶段 2 时 nginx Basic Auth 还开着，
// 外网根本到不了登录页，所以这个「第一个」必然是老师本人）。
func upsertUser(ctx context.Context, st *Storage, openid string) (User, error) {
	usersMu.Lock()
	defer usersMu.Unlock()

	users, _ := st.ListUsers(ctx) // 写前重读，不用缓存
	if u, ok := findUserByOpenID(users, openid); ok {
		u.LastLogin = time.Now().Format("2006-01-02 15:04:05")
		for i := range users {
			if users[i].UID == u.UID {
				users[i] = u
			}
		}
		if err := st.SaveUsers(ctx, users); err != nil {
			return u, err
		}
		invalidateUserCache()
		return u, nil
	}

	role := roleForNewUser(users)
	u := User{
		UID:       newUID(),
		OpenID:    openid,
		Role:      role,
		Name:      "",
		Created:   time.Now().Format("2006-01-02 15:04:05"),
		LastLogin: time.Now().Format("2006-01-02 15:04:05"),
	}
	// ⚠️ owner 的 uid 恒为空。personalBase() 靠 uid=="" 走老路径（零迁移），
	// 给 owner 也发 u_xxx 的话，老师一登录就读 users/u_xxx/archives/（空目录），
	// 表现就是「档案全没了」—— 数据其实还在老位置，只是读错了地方。
	if role == roleOwner {
		u.UID = ""
	}
	users = append(users, u)
	if err := st.SaveUsers(ctx, users); err != nil {
		return User{}, err
	}
	invalidateUserCache()
	return u, nil
}

// ---------- pending 的 7 天体验期 ----------
//
// 老师 2026-10-28 定的：新用户扫码进来是 pending，但**不能一上来就挡**——
// 给 7 个自然日的完整体验期（该练练、该录录），到期还没被批准再拦。
// 这样管理员（老师）有缓冲时间，陌生人也不能无限期占用。

const pendingGraceDays = 7

// parseUserTime 用户表里的时间串。老数据格式不统一，两种都认。
func parseUserTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// pendingDaysLeft 还剩几个自然日（按日期算，不看时分秒）。<=0 就是过期。
// ⚠️ 老数据没有 created → 按满格给，宁可多给几天也别误伤已登录的人。
func pendingDaysLeft(u User) int {
	t, ok := parseUserTime(u.Created)
	if !ok {
		return pendingGraceDays
	}
	d0 := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	now := time.Now()
	d1 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return pendingGraceDays - int(d1.Sub(d0).Hours()/24)
}

func pendingExpired(u User) bool { return pendingDaysLeft(u) <= 0 }

// roleForNewUser 决定新用户进来时给什么角色：一个都没有 → owner，其余 → pending
// （pending 要 owner 用 `jrp user approve` 放行，防止谁扫一下就能读数据）。
func roleForNewUser(users []User) string {
	if len(users) == 0 {
		return roleOwner
	}
	return rolePending
}

// usersMu 包住用户表的读-改-写。COS 的 PutObject 没有 If-Match，做不了乐观锁，
// 只能靠进程内这把锁 + 写前重读（用户数个位数，新用户注册极低频，够用）。
var usersMu sync.Mutex

// newUID 8 字节 hex。短到能塞进 cookie 和日志，又足够不可能撞。
func newUID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return "u_" + hex.EncodeToString(b)
}

// ==========================================================================
// 原 storage_words.go
// ==========================================================================

// WordMeta / WordPOS 类型已迁 internal/words（含完整字段注释），
// 根包靠 types.go 的别名保持这两个名字不变；本文件只留 word_meta.json
// 的 COS 读写。读不到就返回空表，绝不因此把词汇总表打不开。
func (s *Storage) wordMetaKey() string {
	return s.sharedBase() + "/word_meta.json"
}

// DownloadWordMeta 读词性表。
//
// 不存在或解析失败 → 返回空表而不是报错：词性只是展示增强，
// 读不到就让页面上这列空着，绝不能因此把整个词汇总表打不开。
func (s *Storage) DownloadWordMeta(ctx context.Context) (*WordMeta, error) {
	data, err := s.store.GetAll(ctx, s.wordMetaKey())
	if err != nil {
		return &WordMeta{Items: map[string]WordPOS{}}, nil
	}
	var m WordMeta
	if err := jsonUnmarshal(data, &m); err != nil {
		return &WordMeta{Items: map[string]WordPOS{}}, nil
	}
	if m.Items == nil {
		m.Items = map[string]WordPOS{}
	}
	return &m, nil
}

// UploadWordMeta 写词性表。演练模式下不落盘，与其他 Upload* 保持一致。
func (s *Storage) UploadWordMeta(ctx context.Context, m *WordMeta) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.wordMetaKey(), []byte(toJSON(m)))
}
