package syncsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/go-web-utils/syncsdk/e2e"
)

// Change 是拉取到的一份文档变更 (已解密), 交给 Applier.Apply 落到本地库。
type Change struct {
	Space      string
	Collection string
	ID         string
	Rev        int64
	Deleted    bool
	// Value 是文档明文 JSON; 墓碑或解密失败时为 nil。
	Value json.RawMessage
	// UpdatedBy / UpdatedAt 由服务端按令牌写入, 是展示「谁改的」唯一可信来源。
	UpdatedBy string
	UpdatedAt time.Time
	// KeyVersion 是加密所用空间密钥版本, 未加密为 0。
	KeyVersion int
	// Pending 为 true 表示本地出站队列里还有该文档未推送的修改: 应用不应覆盖本地内容,
	// 下次推送会以 409 走冲突回调。
	Pending bool
	// Err 非空表示该文档无法解密 (被篡改、缺少对应版本的密钥); 游标照常前进,
	// 应用可记录并提示, 不应删除本地副本。
	Err error
}

// Commit 是本机写入被服务端接受后的结果, 应用据此更新本地行的 rev 并清 dirty
// (出站队列里没有更新的修改时)。
type Commit struct {
	Space      string
	Collection string
	ID         string
	Rev        int64
	Deleted    bool
	UpdatedBy  string
	UpdatedAt  time.Time
}

// Applier 由应用实现, 把同步结果落到本地库。方法在引擎的同步协程里串行调用。
type Applier interface {
	// Apply 应用一页拉取到的变更 (按 Rev 升序)。建议只在 change.Rev 大于本地 rev 时覆盖,
	// 本机刚推送的写入也会再拉回一次。返回错误时游标不前进, 下次重拉。
	Apply(ctx context.Context, space string, changes []Change) error
	// Committed 通知本机写入已被接受。
	Committed(ctx context.Context, c Commit) error
	// ResetSpace 在游标过期 (410) 时调用: 应用应丢弃该空间所有已同步且非 dirty 的本地副本
	// (或标记待核对), 引擎随后从 0 全量重拉。dirty 的本地修改仍在出站队列, 不受影响。
	ResetSpace(ctx context.Context, space string) error
}

// ResolutionKind 是冲突的处理方式。
type ResolutionKind int

const (
	// KeepRemote 放弃本地修改, 采用服务端版本 (引擎随即 Apply 服务端版本)。
	KeepRemote ResolutionKind = iota
	// Overwrite 以本地内容覆盖服务端版本 (基于服务端当前 rev 重推)。
	Overwrite
	// SaveAsCopy 采用服务端版本, 本地内容另存为新文档 Resolution.CopyID 推送。
	// 应用须在返回前自行把副本写进本地库 (如标题加「（冲突副本）」)。
	SaveAsCopy
)

// Resolution 是冲突回调的决定。
type Resolution struct {
	Kind ResolutionKind
	// CopyID 是 SaveAsCopy 时副本的新文档 id, 必填 (可用 NewID 生成)。
	CopyID string
	// CopyValue 是副本内容, 为空则用本地内容原样。
	CopyValue json.RawMessage
}

// Conflict 是推送时遇到的版本冲突: 本地修改基于的版本已被他人更新。
type Conflict struct {
	// Local 是本地待推送的修改 (明文)。
	Local OutboxItem
	// Remote 是服务端当前版本 (已解密; 已删除时 Deleted 为 true)。
	Remote Change
}

// ConflictResolver 决定冲突如何处理; 返回错误时该条保留在队列里, 下次再问。
type ConflictResolver func(ctx context.Context, c Conflict) (Resolution, error)

// EngineOptions 配置 Engine。
type EngineOptions struct {
	// Client 是同步接口客户端, 必填。
	Client *Client
	// Keys 为端到端加密应用必填; nil 表示应用未开启端到端加密 (payload 明文, 由服务端加密存储)。
	Keys *KeyManager
	// Outbox、Cursors、Applier、Resolver 必填。
	Outbox   Outbox
	Cursors  CursorStore
	Applier  Applier
	Resolver ConflictResolver
	// AppendOnly 是应用登记为只追加的集合 (如操作记录)。引擎拒绝对其修改与删除,
	// 推送遇到冲突时不走冲突回调, 重加密时跳过。
	AppendOnly []string
	// OnRejected 在服务端永久拒绝某条写入 (403 无权写该集合、413 超限等) 时调用,
	// 返回 true 从队列删除, false 保留到下次同步。nil 时保留, 避免静默丢数据。
	OnRejected func(ctx context.Context, item OutboxItem, err error) (drop bool)
	// OnPresence 在拉取或事件流带回他人编辑状态时调用。Client 设了 DeviceID 时也包含
	// 本人其他设备的心跳 (Presence.UserID 是自己、DeviceID 不同), 可据此提示「在另一台设备上使用中」。
	OnPresence func(space string, presence []Presence)
	// BlobCacheDir 是附件下载缓存目录; 使用 BlobPath 时必填。
	BlobCacheDir string
}

// Engine 是同步引擎: 出站队列推送 (加密、冲突处理)、增量拉取 (解密、游标、410 全量重拉)、
// 事件订阅、附件、presence 与只追加集合。并发安全; 同一空间的推送与拉取串行执行。
type Engine struct {
	opts       EngineOptions
	appendOnly map[string]bool

	locks   sync.Map // space → *sync.Mutex
	notifyM sync.Mutex
	notify  map[string]chan struct{}
}

// NewEngine 创建 Engine。
func NewEngine(opts EngineOptions) (*Engine, error) {
	if opts.Client == nil || opts.Outbox == nil || opts.Cursors == nil || opts.Applier == nil || opts.Resolver == nil {
		return nil, errors.New("syncsdk: EngineOptions needs Client, Outbox, Cursors, Applier and Resolver")
	}
	e := &Engine{opts: opts, appendOnly: map[string]bool{}, notify: map[string]chan struct{}{}}
	for _, c := range opts.AppendOnly {
		e.appendOnly[c] = true
	}
	return e, nil
}

// Client 返回引擎使用的接口客户端。
func (e *Engine) Client() *Client { return e.opts.Client }

func (e *Engine) lock(space string) func() {
	v, _ := e.locks.LoadOrStore(space, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (e *Engine) notifyChan(space string) chan struct{} {
	e.notifyM.Lock()
	defer e.notifyM.Unlock()
	ch, ok := e.notify[space]
	if !ok {
		ch = make(chan struct{}, 1)
		e.notify[space] = ch
	}
	return ch
}

func (e *Engine) poke(space string) {
	select {
	case e.notifyChan(space) <- struct{}{}:
	default:
	}
}

func validName(kind, s string) error {
	if s == "" || len(s) > 200 || strings.ContainsAny(s, "/\\?#") {
		return fmt.Errorf("syncsdk: invalid %s %q", kind, s)
	}
	return nil
}

// Save 把一份文档的修改写入出站队列 (断网照常成功), 订阅中的引擎会尽快推送。
// baseRev 是本地副本当前的服务端版本, 新建为 0; value 为 json.RawMessage、[]byte 或可 JSON 序列化的值。
// 只追加集合只允许 baseRev 为 0 的新建。
func (e *Engine) Save(ctx context.Context, space, collection, id string, baseRev int64, value any) error {
	if err := errors.Join(validName("space", space), validName("collection", collection), validName("id", id)); err != nil {
		return err
	}
	if e.appendOnly[collection] && baseRev != 0 {
		return ErrAppendOnly
	}
	raw, err := toJSON(value)
	if err != nil {
		return err
	}
	if _, err := e.opts.Outbox.Enqueue(ctx, OutboxItem{Space: space, Collection: collection, ID: id, BaseRev: baseRev, Value: raw}); err != nil {
		return err
	}
	e.poke(space)
	return nil
}

// Delete 把删除写入出站队列, 推送时写墓碑。只追加集合不允许删除。
func (e *Engine) Delete(ctx context.Context, space, collection, id string, baseRev int64) error {
	if err := errors.Join(validName("space", space), validName("collection", collection), validName("id", id)); err != nil {
		return err
	}
	if e.appendOnly[collection] {
		return ErrAppendOnly
	}
	if _, err := e.opts.Outbox.Enqueue(ctx, OutboxItem{Space: space, Collection: collection, ID: id, BaseRev: baseRev, Deleted: true}); err != nil {
		return err
	}
	e.poke(space)
	return nil
}

func toJSON(v any) (json.RawMessage, error) {
	var raw []byte
	switch x := v.(type) {
	case json.RawMessage:
		raw = x
	case []byte:
		raw = x
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	if !json.Valid(raw) {
		return nil, errors.New("syncsdk: document value is not valid JSON")
	}
	return append(json.RawMessage(nil), raw...), nil
}

// ring 返回空间密钥; 未开启端到端加密返回 nil。
func (e *Engine) ring(ctx context.Context, space string) (*e2e.Keyring, error) {
	if e.opts.Keys == nil {
		return nil, nil
	}
	return e.opts.Keys.Keyring(ctx, space)
}

// SyncResult 汇报一次 Sync。
type SyncResult struct {
	Keys *SpaceSyncReport
	Push PushResult
	Pull PullResult
}

// Sync 执行一轮完整同步: 同步空间密钥 (端到端加密时) → 推送出站队列 → 拉取增量。
func (e *Engine) Sync(ctx context.Context, space string) (SyncResult, error) {
	unlock := e.lock(space)
	defer unlock()
	var res SyncResult
	if e.opts.Keys != nil {
		rep, err := e.opts.Keys.SyncSpace(ctx, space)
		if err != nil {
			return res, fmt.Errorf("sync keys: %w", err)
		}
		res.Keys = rep
	}
	var err error
	if res.Push, err = e.push(ctx, space); err != nil {
		return res, fmt.Errorf("push: %w", err)
	}
	if res.Pull, err = e.pull(ctx, space); err != nil {
		return res, fmt.Errorf("pull: %w", err)
	}
	return res, nil
}

// PushResult 汇报一次推送。
type PushResult struct {
	Pushed    int // 被接受的写入
	Conflicts int // 走了冲突回调的写入
	Rejected  int // 被服务端永久拒绝的写入
}

// Push 推送该空间出站队列里的全部修改。
func (e *Engine) Push(ctx context.Context, space string) (PushResult, error) {
	unlock := e.lock(space)
	defer unlock()
	return e.push(ctx, space)
}

const maxPushRounds = 10

func (e *Engine) push(ctx context.Context, space string) (PushResult, error) {
	var res PushResult
	var ring *e2e.Keyring
	skip := map[int64]bool{} // 本轮已决定暂留的条目
	for round := 0; round < maxPushRounds; round++ {
		pending, err := e.opts.Outbox.Pending(ctx, space, MaxBatchOps+len(skip))
		if err != nil {
			return res, err
		}
		var items []OutboxItem
		for _, it := range pending {
			if !skip[it.Seq] && len(items) < MaxBatchOps {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			return res, nil
		}
		if ring == nil && e.opts.Keys != nil {
			if ring, err = e.ring(ctx, space); err != nil {
				return res, err
			}
		}
		ops := make([]BatchOp, len(items))
		for i, it := range items {
			if ops[i], err = encodeOp(ring, it); err != nil {
				return res, err
			}
		}
		results, err := e.write(ctx, space, ops)
		if err != nil {
			return res, err
		}
		var transient error
		for i, r := range results {
			keep, err := e.handleResult(ctx, space, ring, items[i], ops[i], r, &res)
			if err != nil {
				if !IsTransient(err) {
					return res, err
				}
				transient = err
			}
			if keep {
				skip[items[i].Seq] = true
			}
		}
		if transient != nil {
			return res, transient
		}
	}
	return res, nil
}

func encodeOp(ring *e2e.Keyring, it OutboxItem) (BatchOp, error) {
	op := BatchOp{Op: OpPut, Collection: it.Collection, ID: it.ID, BaseRev: it.BaseRev}
	if it.Deleted {
		op.Op = OpDelete
		return op, nil
	}
	if ring == nil {
		op.Payload = it.Value
		return op, nil
	}
	env, err := ring.Seal(it.Collection, it.ID, it.Value)
	if err != nil {
		return op, err
	}
	op.Payload, err = json.Marshal(env)
	return op, err
}

// write 经 batch 提交写操作。空间级拒绝 (非成员、只读、订阅到期) 作用于整个请求,
// 以错误返回, 修改留在队列; 集合级与单条的拒绝在逐条结果里。
func (e *Engine) write(ctx context.Context, space string, ops []BatchOp) ([]BatchResult, error) {
	return e.opts.Client.Batch(ctx, space, ops)
}

// handleResult 处理一条写入结果, keep 表示条目暂留队列且本轮不再重试。
func (e *Engine) handleResult(ctx context.Context, space string, ring *e2e.Keyring, it OutboxItem, op BatchOp, r BatchResult, res *PushResult) (keep bool, err error) {
	switch {
	case r.Status >= 200 && r.Status < 300:
		if r.Doc == nil {
			return true, fmt.Errorf("%w: write result without doc", ErrProtocol)
		}
		res.Pushed++
		return false, e.commit(ctx, space, it, r.Doc)
	case r.Status == http.StatusConflict:
		return e.conflict(ctx, space, ring, it, op, r, res)
	case r.Status == http.StatusTooManyRequests || r.Status >= 500:
		return true, &APIError{Status: r.Status, Code: r.Code, Msg: r.Msg}
	default:
		res.Rejected++
		return e.reject(ctx, it, &APIError{Status: r.Status, Code: r.Code, Msg: r.Msg})
	}
}

func (e *Engine) commit(ctx context.Context, space string, it OutboxItem, d *Doc) error {
	if err := e.opts.Outbox.Ack(ctx, it, d.Rev); err != nil {
		return err
	}
	return e.opts.Applier.Committed(ctx, Commit{Space: space, Collection: it.Collection, ID: it.ID,
		Rev: d.Rev, Deleted: d.Deleted, UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt})
}

func (e *Engine) reject(ctx context.Context, it OutboxItem, err error) (bool, error) {
	if e.opts.OnRejected != nil && e.opts.OnRejected(ctx, it, err) {
		return false, e.opts.Outbox.Drop(ctx, it)
	}
	return true, nil
}

func (e *Engine) conflict(ctx context.Context, space string, ring *e2e.Keyring, it OutboxItem, op BatchOp, r BatchResult, res *PushResult) (bool, error) {
	cur := r.Doc
	if cur == nil {
		return true, fmt.Errorf("%w: conflict without current document", ErrProtocol)
	}
	// 上次请求其实已成功、只是应答丢失 (重试后看到自己的写入), 或双方都删了: 视为成功。
	if (it.Deleted && cur.Deleted) || (!it.Deleted && !cur.Deleted && samePayload(ring != nil, op.Payload, cur.Payload)) {
		res.Pushed++
		return false, e.commit(ctx, space, it, cur)
	}
	if e.appendOnly[it.Collection] {
		res.Rejected++
		return e.reject(ctx, it, ErrAppendOnly)
	}
	refreshed := false
	remote := e.decode(ctx, space, ring, *cur, &refreshed)
	res.Conflicts++
	dec, err := e.opts.Resolver(ctx, Conflict{Local: it, Remote: remote})
	if err != nil {
		return true, nil
	}
	switch dec.Kind {
	case Overwrite:
		return false, e.opts.Outbox.Rebase(ctx, it, cur.Rev)
	case KeepRemote, SaveAsCopy:
		if dec.Kind == SaveAsCopy && !it.Deleted {
			if err := validName("id", dec.CopyID); err != nil || dec.CopyID == it.ID {
				return true, errors.New("syncsdk: SaveAsCopy needs a new CopyID")
			}
			val := dec.CopyValue
			if len(val) == 0 {
				val = it.Value
			}
			if _, err := e.opts.Outbox.Enqueue(ctx, OutboxItem{Space: space, Collection: it.Collection, ID: dec.CopyID, Value: val}); err != nil {
				return true, err
			}
		}
		if err := e.opts.Outbox.Drop(ctx, it); err != nil {
			return true, err
		}
		remote.Pending = false
		return false, e.opts.Applier.Apply(ctx, space, []Change{remote})
	}
	return true, fmt.Errorf("syncsdk: unknown resolution %d", dec.Kind)
}

// samePayload 判断服务端当前内容是否就是本机刚发出的那次写入。
// 加密时比较信封 (nonce 随机, 相同即同一次加密); 明文时按 JSON 语义比较 (服务端可能重排键)。
func samePayload(encrypted bool, sent, current json.RawMessage) bool {
	if encrypted {
		a, err1 := e2e.ParseEnvelope(sent)
		b, err2 := e2e.ParseEnvelope(current)
		return err1 == nil && err2 == nil && a.Equal(b)
	}
	var a, b any
	return json.Unmarshal(sent, &a) == nil && json.Unmarshal(current, &b) == nil && reflect.DeepEqual(a, b)
}

// decode 把服务端文档转换成明文 Change。refreshed 非 nil 时, 遇到未知密钥版本最多联网刷新一次密钥
// (同一次拉取共用该标记), 避免一批无法解密的文档逐条触发请求。
func (e *Engine) decode(ctx context.Context, space string, ring *e2e.Keyring, d Doc, refreshed *bool) Change {
	c := Change{Space: space, Collection: d.Collection, ID: d.ID, Rev: d.Rev, Deleted: d.Deleted,
		UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt}
	if d.Deleted {
		return c
	}
	if ring == nil {
		c.Value = append(json.RawMessage(nil), d.Payload...)
		return c
	}
	env, err := e2e.ParseEnvelope(d.Payload)
	if err != nil {
		c.Err = err
		return c
	}
	c.KeyVersion = env.KV
	if !ring.Has(env.KV) && e.opts.Keys != nil && refreshed != nil && !*refreshed {
		// 他人刚轮换过: 刷新一次密钥再试。
		*refreshed = true
		if _, err := e.opts.Keys.SyncSpace(ctx, space); err == nil {
			if fresh, err := e.opts.Keys.Keyring(ctx, space); err == nil {
				ring = fresh
			}
		}
	}
	pt, err := ring.Open(d.Collection, d.ID, env)
	if err != nil {
		c.Err = err
		return c
	}
	c.Value = pt
	return c
}

// PullResult 汇报一次拉取。
type PullResult struct {
	Applied int
	// Reset 为 true 表示游标过期, 已调用 ResetSpace 并全量重拉。
	Reset  bool
	Cursor int64
}

// Pull 拉取该空间自游标以来的全部变更并交给 Applier。游标过期 (410) 时调用
// Applier.ResetSpace 后从 0 全量重拉。本人尚未被授予密钥时返回 ErrKeyNotGranted。
func (e *Engine) Pull(ctx context.Context, space string) (PullResult, error) {
	unlock := e.lock(space)
	defer unlock()
	return e.pull(ctx, space)
}

func (e *Engine) pull(ctx context.Context, space string) (PullResult, error) {
	var res PullResult
	ring, err := e.ring(ctx, space)
	if err != nil {
		return res, err
	}
	cursor, err := e.opts.Cursors.Get(ctx, space)
	if err != nil {
		return res, err
	}
	for {
		page, err := e.opts.Client.Changes(ctx, space, cursor, MaxChangesLimit)
		if errors.Is(err, ErrCursorExpired) && !res.Reset && cursor != 0 {
			if err := e.opts.Applier.ResetSpace(ctx, space); err != nil {
				return res, err
			}
			if err := e.opts.Cursors.Set(ctx, space, 0); err != nil {
				return res, err
			}
			cursor, res.Reset = 0, true
			continue
		}
		if err != nil {
			return res, err
		}
		changes := make([]Change, 0, len(page.Docs))
		refreshed := false
		for _, d := range page.Docs {
			c := e.decode(ctx, space, ring, d, &refreshed)
			if e.opts.Keys != nil {
				// decode 可能刷新过密钥, 后续文档用最新的 keyring。
				if fresh, err := e.opts.Keys.Keyring(ctx, space); err == nil {
					ring = fresh
				}
			}
			if p, err := e.opts.Outbox.Lookup(ctx, space, d.Collection, d.ID); err != nil {
				return res, err
			} else if p != nil {
				c.Pending = true
			}
			changes = append(changes, c)
		}
		if len(changes) > 0 {
			if err := e.opts.Applier.Apply(ctx, space, changes); err != nil {
				return res, err
			}
		}
		if page.Cursor < cursor {
			return res, fmt.Errorf("%w: cursor moved backwards", ErrProtocol)
		}
		if err := e.opts.Cursors.Set(ctx, space, page.Cursor); err != nil {
			return res, err
		}
		res.Applied += len(changes)
		cursor = page.Cursor
		res.Cursor = cursor
		if e.opts.OnPresence != nil {
			e.opts.OnPresence(space, page.Presence)
		}
		if !page.HasMore {
			return res, nil
		}
		if len(page.Docs) == 0 {
			return res, fmt.Errorf("%w: empty page with hasMore", ErrProtocol)
		}
	}
}

// Reencrypt 把空间里仍用旧版本密钥加密的文档用当前版本重新加密 (owner 在成员移除后可手动触发)。
// 遍历全部文档, 跳过只追加集合与墓碑; 期间被他人修改 (409) 的文档跳过 (新写入已用新版本)。
// 重加密后文档 rev 变化、内容不变, 引擎会经 Committed 通知应用更新本地 rev。返回重加密的份数。
func (e *Engine) Reencrypt(ctx context.Context, space string) (int, error) {
	if e.opts.Keys == nil {
		return 0, errors.New("syncsdk: Reencrypt requires end-to-end encryption")
	}
	unlock := e.lock(space)
	defer unlock()
	if _, err := e.opts.Keys.SyncSpace(ctx, space); err != nil {
		return 0, err
	}
	ring, err := e.ring(ctx, space)
	if err != nil {
		return 0, err
	}
	n := 0
	var cursor int64
	for {
		page, err := e.opts.Client.Changes(ctx, space, cursor, MaxChangesLimit)
		if err != nil {
			return n, err
		}
		for _, d := range page.Docs {
			if d.Deleted || e.appendOnly[d.Collection] {
				continue
			}
			env, err := e2e.ParseEnvelope(d.Payload)
			if err != nil || env.KV >= ring.Current() {
				continue
			}
			pt, err := ring.Open(d.Collection, d.ID, env)
			if err != nil {
				continue
			}
			fresh, err := ring.Seal(d.Collection, d.ID, pt)
			e2e.Zero(pt)
			if err != nil {
				return n, err
			}
			payload, _ := json.Marshal(fresh)
			doc, err := e.opts.Client.PutDoc(ctx, space, d.Collection, d.ID, d.Rev, payload)
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return n, err
			}
			n++
			if p, _ := e.opts.Outbox.Lookup(ctx, space, d.Collection, d.ID); p != nil {
				if err := e.opts.Outbox.Rebase(ctx, OutboxItem{Seq: -1, Space: space, Collection: d.Collection, ID: d.ID, BaseRev: d.Rev}, doc.Rev); err != nil {
					return n, err
				}
			}
			if err := e.opts.Applier.Committed(ctx, Commit{Space: space, Collection: d.Collection, ID: d.ID,
				Rev: doc.Rev, UpdatedBy: doc.UpdatedBy, UpdatedAt: doc.UpdatedAt}); err != nil {
				return n, err
			}
		}
		if !page.HasMore || len(page.Docs) == 0 {
			return n, nil
		}
		cursor = page.Cursor
	}
}
