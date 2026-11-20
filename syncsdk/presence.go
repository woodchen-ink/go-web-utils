package syncsdk

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"
)

// PresenceInterval 是「正在编辑」心跳间隔; 服务端 60 秒过期, 20 秒一次可容忍两次丢失。
const PresenceInterval = 20 * time.Second

// Editing 在打开一份文档编辑时调用: 立即发一次心跳, 之后每 PresenceInterval 一次,
// 直到调用返回的 stop 或 ctx 结束。心跳失败只影响他人看到的提示, 静默忽略。
// 他人的编辑状态经 EngineOptions.OnPresence 下发。
func (e *Engine) Editing(ctx context.Context, space, collection, id string) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(PresenceInterval)
		defer t.Stop()
		for {
			_ = e.opts.Client.TouchPresence(ctx, space, collection, id)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return cancel
}

// ActivityLog 是写入只追加集合的操作记录助手 (「谁在什么时候改了哪份单据」)。
// 每条记录是一份新文档, 内容照常端到端加密; 操作人与时间以服务端写入的
// Change.UpdatedBy / UpdatedAt 为准, 记录内容里不要、也不必自报身份。
// 记录经 Pull 与其他文档一起下发, 应用按 Collection 区分。
type ActivityLog struct {
	e          *Engine
	space      string
	collection string
}

// ActivityLog 返回某空间某只追加集合的记录助手。集合须登记在 EngineOptions.AppendOnly。
func (e *Engine) ActivityLog(space, collection string) *ActivityLog {
	return &ActivityLog{e: e, space: space, collection: collection}
}

// Append 追加一条记录 (入出站队列), 返回新记录 id。id 由 NewID 生成, 按时间有序。
func (l *ActivityLog) Append(ctx context.Context, entry any) (string, error) {
	if !l.e.appendOnly[l.collection] {
		return "", ErrAppendOnly
	}
	id := NewID()
	return id, l.e.Save(ctx, l.space, l.collection, id, 0, entry)
}

// NewID 生成文档 id: 48 位毫秒时间戳 + 80 位随机数, Crockford base32 小写共 26 位
// (ULID 布局), 字典序即创建时间序。注意 id 对服务端可见, 时间成分也就可见。
func NewID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16)
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err) // crypto/rand 在受支持平台上不会失败
	}
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	// 128 位 → 26 个 5 位符号, 首符号只用高 3 位 (与 ULID 一致)。
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var sb strings.Builder
	for i := 25; i >= 0; i-- {
		shift := uint(i * 5)
		var v uint64
		switch {
		case shift >= 64:
			v = hi >> (shift - 64)
		case shift+5 > 64:
			v = lo>>shift | hi<<(64-shift)
		default:
			v = lo >> shift
		}
		sb.WriteByte(alphabet[v&31])
	}
	return sb.String()
}
