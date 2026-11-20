package syncsdk

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// 本文件定义应用需实现的本地持久化接口, 并提供内存实现 (测试与示例用)。
// 桌面端通常用 SQLite 实现 Outbox / CursorStore, 用 DPAPI / 钥匙串实现 KeyStore。

// ErrKeyNotFound 由 KeyStore.Get 在名称不存在时返回。
var ErrKeyNotFound = errors.New("syncsdk: key not found in key store")

// KeyStore 是本机系统密钥库 (Windows DPAPI、macOS 钥匙串) 的抽象, 按名称存取秘密字节。
// SDK 存入的名称形如 "<命名空间>/user-key"、"<命名空间>/space/<空间>"。
// 实现方须加密落盘且不进备份; Get 返回的切片 SDK 用完会清零。
type KeyStore interface {
	Get(ctx context.Context, name string) ([]byte, error)
	Put(ctx context.Context, name string, secret []byte) error
	Delete(ctx context.Context, name string) error
}

// CursorStore 按空间保存拉取游标 (服务端序号)。从未拉取过返回 0。
type CursorStore interface {
	Get(ctx context.Context, space string) (int64, error)
	Set(ctx context.Context, space string, cursor int64) error
}

// OutboxItem 是出站队列里的一条待推送写入, 内容为明文, 推送时才加密。
type OutboxItem struct {
	// Seq 由 Outbox 分配, 全局单调递增, 决定推送顺序; 同一文档再次入队会换新 Seq。
	Seq        int64
	Space      string
	Collection string
	ID         string
	// BaseRev 是这次修改所基于的服务端版本, 新建为 0。
	BaseRev int64
	// Deleted 为 true 表示删除 (写墓碑), 此时 Value 为空。
	Deleted bool
	// Value 是文档明文 JSON。
	Value     json.RawMessage
	CreatedAt time.Time
}

// Outbox 是持久化、有序、按文档去重的出站队列: 断网时照常入队, 联网后按 Seq 推送。
// 实现方必须保证每个方法原子执行 (SQLite 用一个事务)。
type Outbox interface {
	// Enqueue 写入一条修改并分配新 Seq; 同一 (Space, Collection, ID) 已有待推送项时
	// 用新内容整条替换 (只保留最新), 返回入队后的条目。
	Enqueue(ctx context.Context, item OutboxItem) (OutboxItem, error)
	// Pending 按 Seq 升序返回该空间最多 limit 条待推送项。
	Pending(ctx context.Context, space string, limit int) ([]OutboxItem, error)
	// Lookup 返回该文档的待推送项, 没有返回 nil。
	Lookup(ctx context.Context, space, collection, id string) (*OutboxItem, error)
	// Ack 在 item 推送成功 (服务端版本 newRev) 后调用:
	// 队列中该文档的条目 Seq 仍等于 item.Seq 时删除它; 已被更新的修改取代时保留新条目,
	// 若新条目的 BaseRev 等于 item.BaseRev 则改为 newRev (新修改是在推送途中做的)。
	Ack(ctx context.Context, item OutboxItem, newRev int64) error
	// Rebase 把条目的 BaseRev 改为 newBaseRev 以便覆盖重推: Seq 相同则直接改;
	// 已被取代且新条目 BaseRev 等于 item.BaseRev 时改新条目。
	Rebase(ctx context.Context, item OutboxItem, newBaseRev int64) error
	// Drop 在条目 Seq 仍等于 item.Seq 时删除它 (放弃本地修改或服务端永久拒绝)。
	Drop(ctx context.Context, item OutboxItem) error
}

type docKey struct{ space, collection, id string }

// MemoryOutbox 是 Outbox 的内存实现, 进程退出即丢失, 仅用于测试与示例。
type MemoryOutbox struct {
	mu    sync.Mutex
	seq   int64
	items map[docKey]OutboxItem
}

// NewMemoryOutbox 创建空的内存出站队列。
func NewMemoryOutbox() *MemoryOutbox { return &MemoryOutbox{items: map[docKey]OutboxItem{}} }

func keyOf(i OutboxItem) docKey { return docKey{i.Space, i.Collection, i.ID} }

// Enqueue 实现 Outbox。
func (o *MemoryOutbox) Enqueue(_ context.Context, item OutboxItem) (OutboxItem, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	item.Seq = o.seq
	item.Value = append(json.RawMessage(nil), item.Value...)
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	o.items[keyOf(item)] = item
	return item, nil
}

// Pending 实现 Outbox。
func (o *MemoryOutbox) Pending(_ context.Context, space string, limit int) ([]OutboxItem, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []OutboxItem
	for k, it := range o.items {
		if k.space == space {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Lookup 实现 Outbox。
func (o *MemoryOutbox) Lookup(_ context.Context, space, collection, id string) (*OutboxItem, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if it, ok := o.items[docKey{space, collection, id}]; ok {
		return &it, nil
	}
	return nil, nil
}

// Ack 实现 Outbox。
func (o *MemoryOutbox) Ack(_ context.Context, item OutboxItem, newRev int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	cur, ok := o.items[keyOf(item)]
	switch {
	case !ok:
	case cur.Seq == item.Seq:
		delete(o.items, keyOf(item))
	case cur.BaseRev == item.BaseRev:
		cur.BaseRev = newRev
		o.items[keyOf(item)] = cur
	}
	return nil
}

// Rebase 实现 Outbox。
func (o *MemoryOutbox) Rebase(_ context.Context, item OutboxItem, newBaseRev int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	cur, ok := o.items[keyOf(item)]
	if ok && (cur.Seq == item.Seq || cur.BaseRev == item.BaseRev) {
		cur.BaseRev = newBaseRev
		o.items[keyOf(item)] = cur
	}
	return nil
}

// Drop 实现 Outbox。
func (o *MemoryOutbox) Drop(_ context.Context, item OutboxItem) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if cur, ok := o.items[keyOf(item)]; ok && cur.Seq == item.Seq {
		delete(o.items, keyOf(item))
	}
	return nil
}

// Len 返回队列总条数 (测试用)。
func (o *MemoryOutbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

// MemoryCursorStore 是 CursorStore 的内存实现。
type MemoryCursorStore struct {
	mu sync.Mutex
	m  map[string]int64
}

// NewMemoryCursorStore 创建空的内存游标存储。
func NewMemoryCursorStore() *MemoryCursorStore { return &MemoryCursorStore{m: map[string]int64{}} }

// Get 实现 CursorStore。
func (s *MemoryCursorStore) Get(_ context.Context, space string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[space], nil
}

// Set 实现 CursorStore。
func (s *MemoryCursorStore) Set(_ context.Context, space string, cursor int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[space] = cursor
	return nil
}

// MemoryKeyStore 是 KeyStore 的内存实现, 秘密明文驻留内存, 仅用于测试。
type MemoryKeyStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewMemoryKeyStore 创建空的内存密钥库。
func NewMemoryKeyStore() *MemoryKeyStore { return &MemoryKeyStore{m: map[string][]byte{}} }

// Get 实现 KeyStore, 返回副本。
func (s *MemoryKeyStore) Get(_ context.Context, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), v...), nil
}

// Put 实现 KeyStore, 保存副本。
func (s *MemoryKeyStore) Put(_ context.Context, name string, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = append([]byte(nil), secret...)
	return nil
}

// Delete 实现 KeyStore; 名称不存在不算错误。
func (s *MemoryKeyStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, name)
	return nil
}

// Names 返回已存的名称 (测试用)。
func (s *MemoryKeyStore) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
