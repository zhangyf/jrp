package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
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

// COS skill directory where .env.enc is stored
const cosSkillDir = "/Users/zhangyufeng/.workbuddy/skills/tencentcloud-cos"

// Storage wraps an objstore.Store for archive operations.
type Storage struct {
	store objstore.Store
	lang  string
}

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
	encPath := filepath.Join(cosSkillDir, ".env.enc")
	encData, err := os.ReadFile(encPath)
	if err != nil {
		return objstore.Config{}, fmt.Errorf("no env vars set and cannot read .env.enc: %w", err)
	}

	plaintext, err := decryptEnvFile(encData, cosSkillDir)
	if err != nil {
		return objstore.Config{}, fmt.Errorf("failed to decrypt .env.enc: %w", err)
	}

	envVars := parseEnvFile(plaintext)

	if envVars["TENCENT_COS_SECRET_ID"] == "" || envVars["TENCENT_COS_SECRET_KEY"] == "" {
		return objstore.Config{}, fmt.Errorf("credentials not found in .env.enc")
	}

	return objstore.Config{
		Provider:  objstore.ProviderCOS,
		Bucket:    envVars["TENCENT_COS_BUCKET"],
		Region:    envVars["TENCENT_COS_REGION"],
		SecretID:  envVars["TENCENT_COS_SECRET_ID"],
		SecretKey: envVars["TENCENT_COS_SECRET_KEY"],
	}, nil
}

// decryptEnvFile decrypts the .env.enc file using the same algorithm as cos_node.mjs.
// Key derivation: SHA-256(hostname + ":" + username + ":" + skillDir)
// Format: iv(12) + authTag(16) + ciphertext
func decryptEnvFile(encData []byte, skillDir string) (string, error) {
	if len(encData) < 28 {
		return "", fmt.Errorf("encrypted data too short")
	}

	// Derive key
	hostname, _ := os.Hostname()
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("cannot get current user: %w", err)
	}
	username := currentUser.Username

	seed := fmt.Sprintf("%s:%s:%s", hostname, username, skillDir)
	key := sha256.Sum256([]byte(seed))

	// Extract IV, authTag, ciphertext
	iv := encData[:12]
	authTag := encData[12:28]
	ciphertext := encData[28:]

	// Decrypt
	block, err := aes.NewCipher(key[:])
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
	key := s.cosPrefix() + "/archives/" + filename
	return s.store.PutObject(ctx, key, data)
}

// UploadHistory uploads a historical archive to COS.
func (s *Storage) UploadHistory(ctx context.Context, filename string, data []byte) error {
	key := s.cosPrefix() + "/history/" + filename
	return s.store.PutObject(ctx, key, data)
}

// UploadPlan uploads a review plan JSON to COS.
func (s *Storage) UploadPlan(ctx context.Context, plan *ReviewPlan) error {
	key := fmt.Sprintf("%s/plans/plan_%s.json", s.cosPrefix(), plan.Date)
	data := []byte(toJSON(plan))
	return s.store.PutObject(ctx, key, data)
}

// DownloadPlan downloads a review plan JSON from COS.
func (s *Storage) DownloadPlan(ctx context.Context, planDate string) (*ReviewPlan, error) {
	key := fmt.Sprintf("%s/plans/plan_%s.json", s.cosPrefix(), planDate)
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

// UploadExcel uploads an Excel file to COS.
func (s *Storage) UploadExcel(ctx context.Context, date string, major, minor int, localPath string) error {
	key := fmt.Sprintf("%s/plans/review_%s_v%d.%d.xlsx", s.cosPrefix(), date, major, minor)
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read excel file: %w", err)
	}
	return s.store.PutObject(ctx, key, data)
}

// UploadKnowledge uploads a knowledge document to COS.
func (s *Storage) UploadKnowledge(ctx context.Context, filename string, data []byte) error {
	key := s.cosPrefix() + "/knowledge/" + filename
	return s.store.PutObject(ctx, key, data)
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
