package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangyf/objstore"
)

// memObjStore 测试用的内存对象存储，只实现 EnsureArchive 用到的那几条路径。
type memObjStore struct {
	mu   sync.Mutex
	objs map[string][]byte
	mod  map[string]time.Time
}

func newMemObjStore() *memObjStore {
	return &memObjStore{objs: map[string][]byte{}, mod: map[string]time.Time{}}
}

func (m *memObjStore) HeadObject(ctx context.Context, key string) (*objstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.objs[key]; ok {
		return &objstore.ObjectInfo{Key: key, Size: int64(len(d)), LastModified: m.mod[key]}, nil
	}
	return nil, errNotFound
}

func (m *memObjStore) ListObjects(ctx context.Context, opts objstore.ListOptions) ([]objstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []objstore.ObjectInfo{}
	for k, d := range m.objs {
		if strings.HasPrefix(k, opts.Prefix) {
			out = append(out, objstore.ObjectInfo{Key: k, Size: int64(len(d)), LastModified: m.mod[k]})
		}
	}
	return out, nil
}

func (m *memObjStore) GetAll(ctx context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.objs[key]; ok {
		return d, nil
	}
	return nil, errNotFound
}

func (m *memObjStore) PutObject(ctx context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objs[key] = cp
	m.mod[key] = time.Now()
	return nil
}

func (m *memObjStore) DeleteObject(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key)
	return nil
}

func (m *memObjStore) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	d, err := m.GetAll(ctx, key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(string(d))), nil
}

func (m *memObjStore) GetRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	return nil, nil
}
func (m *memObjStore) PutObjectStream(ctx context.Context, key string, r io.Reader, size int64) error {
	return nil
}
func (m *memObjStore) MultipartUpload(ctx context.Context, key string, totalSize, chunkSize int64,
	concurrency int, fetchPart func(partNumber int, offset, size int64) ([]byte, error)) error {
	return nil
}
func (m *memObjStore) PresignGetObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	return "", nil
}
func (m *memObjStore) PresignPutObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	return "", nil
}
func (m *memObjStore) BucketName() string          { return "mem" }
func (m *memObjStore) Provider() objstore.ProviderType { return objstore.ProviderCOS }

var errNotFound = fmt.Errorf("not found")

// 新账号引导：第一次进来没有档案也要能走通，而不是 500「no archives found」。
func TestBootstrapEmptyAccount(t *testing.T) {
	st := &Storage{lang: "ja", store: newMemObjStore()}
	ctx := context.Background()

	if st.hasArchive(ctx) {
		t.Fatal("新账号不该有档案")
	}

	// 写路径：EnsureArchive 造出空档案并落盘
	arc, fname, err := st.EnsureArchive(ctx, "ja")
	if err != nil {
		t.Fatalf("EnsureArchive: %v", err)
	}
	if arc == nil || len(arc.Groups) != 0 {
		t.Errorf("该拿到空档案，got %+v", arc)
	}
	if fname == "" {
		t.Error("该有档案文件名")
	}
	if !st.hasArchive(ctx) {
		t.Error("EnsureArchive 之后该有档案了")
	}

	// 第二次拿的必须是刚落盘那份（而不是又造一份）
	arc2, fname2, err := st.EnsureArchive(ctx, "ja")
	if err != nil {
		t.Fatalf("第二次 EnsureArchive: %v", err)
	}
	if fname2 != fname {
		t.Errorf("第二次该复用同一份档案，got %s vs %s", fname2, fname)
	}
	if arc2 == nil {
		t.Error("第二次该拿到档案")
	}

	// 读路径：没档案 → 空档案（不落盘）
	st2 := &Storage{lang: "ja", store: newMemObjStore()}
	arc3, err := st2.LoadArchiveForRead(ctx, "ja")
	if err != nil {
		t.Fatalf("LoadArchiveForRead: %v", err)
	}
	if arc3 == nil || len(arc3.Groups) != 0 {
		t.Error("没档案该回空档案")
	}
	if st2.hasArchive(ctx) {
		t.Error("读路径不许写 COS")
	}

	// 空档案也必须能录词（审核员录第一个词就是这条路）
	res, err := addWordsToArchive(ctx, st2, "", []WordToAdd{{Word: "みず(水)", Definition: "水"}}, false)
	if err != nil {
		t.Fatalf("空档案录词失败: %v", err)
	}
	if res.Added != 1 {
		t.Errorf("该录进 1 个词，实际 %d", res.Added)
	}
}
