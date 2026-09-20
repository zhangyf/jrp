package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zhangyf/objstore"
)

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

// DownloadLatestArchive downloads the most recent archive from COS.
func (s *Storage) DownloadLatestArchive(ctx context.Context) ([]byte, string, error) {
	prefix := s.cosPrefix() + "/archives/"
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
	key := s.cosPrefix() + "/archives/" + filename
	return s.store.PutObject(ctx, key, data)
}

// UploadHistory uploads a historical archive to COS.
func (s *Storage) UploadHistory(ctx context.Context, filename string, data []byte) error {
	if s.dryRun {
		return nil
	}
	key := s.cosPrefix() + "/history/" + filename
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
		return fmt.Sprintf("%s/plans/hard_%s.json", s.cosPrefix(), date)
	case "sentences":
		return fmt.Sprintf("%s/plans/sentences_%s.json", s.cosPrefix(), date)
	}
	return fmt.Sprintf("%s/plans/plan_%s.json", s.cosPrefix(), date)
}

// UploadPlan uploads a review plan JSON to COS.
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
	key := fmt.Sprintf("%s/plans/review_%s_v%d.%d.xlsx", s.cosPrefix(), date, major, minor)
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
	key := fmt.Sprintf("%s/plans/sentences_%s_v%d.%d.xlsx", s.cosPrefix(), date, major, minor)
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
	key := fmt.Sprintf("%s/plans/hard_words_%s_v%d.%d.xlsx", s.cosPrefix(), date, major, minor)
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
	key := s.cosPrefix() + "/knowledge/" + filename
	return s.store.PutObject(ctx, key, data)
}

// ListKnowledge lists all knowledge documents in COS, sorted by key ascending.
func (s *Storage) ListKnowledge(ctx context.Context) ([]objstore.ObjectInfo, error) {
	prefix := s.cosPrefix() + "/knowledge/"
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
	key := s.cosPrefix() + "/knowledge/" + name
	return s.store.GetAll(ctx, key)
}

// ListHistoryArchives lists all historical archives in COS.
func (s *Storage) ListHistoryArchives(ctx context.Context) ([]objstore.ObjectInfo, error) {
	prefix := s.cosPrefix() + "/history/"
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
	prefix := s.cosPrefix() + "/archives/"
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
	prefix := s.cosPrefix() + "/archives/"
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: prefix, Delimiter: ""})
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].LastModified.After(objs[j].LastModified)
	})
	return objs, nil
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
		fnameDate, _, _, err := ParseFilename(filename)
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
