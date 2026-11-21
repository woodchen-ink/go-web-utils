/*
Package synctest 提供 CZL Connect 同步接口 /api/sync/v1 的进程内假服务端 (httptest),
按设计文档的契约实现: 序号与游标、409 附当前文档、410 游标过期、只追加集合、
服务端写入 updatedBy / updatedAt、batch 逐条结果、附件幂等、密钥接口、新设备授权与 SSE。

它只用于测试 (SDK 自身与接入方的集成测试), 数据全在内存, 不做持久化与性能保证。
测试通过 AddUser / CreateOrg / AddMember / RemoveMember 等方法摆布状态,
用 SetOffline / FailNext 模拟断网与服务端故障。

Documentation: https://go-web-utils.czl.net/docs/syncsdk
*/
package synctest

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/go-web-utils/resputil"
	"github.com/woodchen-ink/go-web-utils/syncsdk"
	"github.com/woodchen-ink/go-web-utils/syncsdk/e2e"
)

// Prefix 是接口路径前缀; 客户端 BaseURL 用 Server.BaseURL()。
const Prefix = "/api/sync/v1"

// 假服务端使用的业务码 (HTTP 状态三位 + 两位序号)。空间级 403 用 40301~40309,
// 集合级与单条 403 用 40310 起, 与 SDK 的约定一致。
const (
	CodeInvalidRequest   = 40001
	CodeInvalidEnvelope  = 40002
	CodeIncompleteGrants = 40003
	CodeDigestMismatch   = 40004
	CodeUnauthorized     = 40101
	CodeNotMember        = 40301
	CodeReadOnly         = 40302
	CodeNotRegistered    = 40310
	CodeRoleTooLow       = 40311
	CodeAppendOnly       = 40312
	CodeNotFound         = 40401
	CodeVersionConflict  = 40901
	CodeStateConflict    = 40902
	CodeCursorExpired    = 41001
	CodeTooLarge         = 41301
	CodeUnavailable      = 50301
)

// Collection 是集合登记信息。
type Collection struct {
	// MinRole 是最低写入角色, 默认 member。
	MinRole string
	// AppendOnly 集合只能新建, 不能修改或删除。
	AppendOnly bool
	// Revisions 是保留的历史版本数, 0 不保留。
	Revisions int
}

// Options 配置假服务端。
type Options struct {
	// E2E 为 syncsdk.E2ERequired 时只接受信封格式 payload, 附件不校验明文摘要。默认 off。
	E2E string
	// MaxDocBytes 是单条 payload 上限, 默认 1 MB。
	MaxDocBytes int
	// MaxBlobBytes 是单个附件上限, 默认 32 MB。
	MaxBlobBytes int64
}

type docKey struct{ collection, id string }

type doc struct {
	syncsdk.Doc
	history []syncsdk.Doc
}

type presenceKey struct{ collection, id, user, device string }

type presenceVal struct {
	exp  time.Time
	name string
}

// subscriber 是一条 SSE 连接的调用方。
type subscriber struct{ user, device string }

type space struct {
	key, pathID, typ, name string
	members                map[string]string // user → role
	readonly               bool
	seq                    int64
	minCursor              int64
	docs                   map[docKey]*doc
	currentVersion         int
	wrapped                map[int]map[string]string // version → user → wrapped
	rotationRequired       bool
	blobs                  map[string][]byte
	presence               map[presenceKey]presenceVal
	subs                   map[chan string]subscriber
}

type device struct {
	user string
	req  syncsdk.DeviceRequest
}

type failure struct {
	status     int
	retryAfter int
	drop       bool
}

// Server 是假服务端。并发安全。
type Server struct {
	*httptest.Server
	opts Options

	mu          sync.Mutex
	now         func() time.Time
	tokens      map[string]string // token → user
	userKeys    map[string]*syncsdk.UserKeys
	spaces      map[string]*space
	collections map[string]Collection
	devices     map[string]*device
	presigned   map[string][]byte
	failures    []failure
	dropResp    int
	offline     bool
	counts      map[string]int
}

// New 启动假服务端, 测试结束时调用 Close。
func New(opts Options) *Server {
	if opts.E2E == "" {
		opts.E2E = syncsdk.E2EOff
	}
	if opts.MaxDocBytes <= 0 {
		opts.MaxDocBytes = 1 << 20
	}
	if opts.MaxBlobBytes <= 0 {
		opts.MaxBlobBytes = 32 << 20
	}
	s := &Server{
		opts: opts, now: time.Now,
		tokens: map[string]string{}, userKeys: map[string]*syncsdk.UserKeys{}, spaces: map[string]*space{},
		collections: map[string]Collection{}, devices: map[string]*device{}, presigned: map[string][]byte{},
		counts: map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// BaseURL 返回 SDK ClientOptions.BaseURL 应填的地址。
func (s *Server) BaseURL() string { return s.URL + Prefix }

// SetNow 替换服务端时钟 (测试设备请求过期)。
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func randomID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// AddUser 登记一个用户并返回其访问令牌; 同时建立其个人空间。
func (s *Server) AddUser(userID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := randomID("tok_")
	s.tokens[tok] = userID
	key := PersonalKey(userID)
	if _, ok := s.spaces[key]; !ok {
		s.spaces[key] = newSpace(key, syncsdk.PersonalSpace, syncsdk.SpaceTypePersonal, "", map[string]string{userID: syncsdk.RoleOwner})
	}
	return tok
}

// RevokeToken 让令牌失效 (之后返回 401)。
func (s *Server) RevokeToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// PersonalKey 返回用户个人空间在假服务端内部的标识, 供 ExpireCursors / RawDoc 等方法使用。
func PersonalKey(userID string) string { return "me:" + userID }

func newSpace(key, pathID, typ, name string, members map[string]string) *space {
	return &space{key: key, pathID: pathID, typ: typ, name: name, members: members,
		docs: map[docKey]*doc{}, wrapped: map[int]map[string]string{}, blobs: map[string][]byte{},
		presence: map[presenceKey]presenceVal{}, subs: map[chan string]subscriber{}}
}

// CreateOrg 建立组织空间, owner 为其所有者。
func (s *Server) CreateOrg(orgID, name, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spaces[orgID] = newSpace(orgID, orgID, syncsdk.SpaceTypeOrg, name, map[string]string{owner: syncsdk.RoleOwner})
}

// AddMember 把用户加入组织 (或改角色)。
func (s *Server) AddMember(orgID, userID, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spaces[orgID].members[userID] = role
}

// RemoveMember 移除成员: 删除其封装密钥, 若其持有过密钥则标记需要轮换。
func (s *Server) RemoveMember(orgID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.spaces[orgID]
	delete(sp.members, userID)
	for _, m := range sp.wrapped {
		if _, ok := m[userID]; ok {
			delete(m, userID)
			sp.rotationRequired = true
		}
	}
}

// SetReadOnly 设置空间只读 (订阅到期、组织 readonly)。
func (s *Server) SetReadOnly(spaceKey string, ro bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spaces[spaceKey].readonly = ro
}

// RegisterCollection 登记集合; 未登记的集合拒绝写入。
func (s *Server) RegisterCollection(name string, c Collection) {
	if c.MinRole == "" {
		c.MinRole = syncsdk.RoleMember
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collections[name] = c
}

// ExpireCursors 模拟墓碑清理: 清除已删除文档的墓碑, 早于当前序号的游标此后回 410。
func (s *Server) ExpireCursors(spaceKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.spaces[spaceKey]
	for k, d := range sp.docs {
		if d.Deleted {
			delete(sp.docs, k)
		}
	}
	sp.minCursor = sp.seq
}

// RawDoc 返回服务端存储的文档原样 (含密文), 模拟服务端或库被拖取。
func (s *Server) RawDoc(spaceKey, collection, id string) *syncsdk.Doc {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.spaces[spaceKey].docs[docKey{collection, id}]
	if !ok {
		return nil
	}
	c := d.Doc
	return &c
}

// SetOffline 为 true 时所有接口请求直接断开连接, 模拟断网。
func (s *Server) SetOffline(off bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline = off
}

// FailNext 让接下来 n 个接口请求返回 status (429 / 5xx 等), retryAfter 秒 > 0 时带 Retry-After。
func (s *Server) FailNext(n, status, retryAfter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < n; i++ {
		s.failures = append(s.failures, failure{status: status, retryAfter: retryAfter})
	}
}

// DropResponseNext 让接下来 n 个接口请求照常执行、但不返回应答 (断开连接),
// 模拟「写入已生效、应答丢失」后客户端重试。
func (s *Server) DropResponseNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropResp += n
}

// Count 返回某类请求的次数, kind 形如 "PUT blob"、"HEAD blob"、"POST batch"、"GET changes"。
func (s *Server) Count(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[kind]
}

// ---- HTTP ----

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_blob/") {
		s.servePresigned(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, Prefix+"/") {
		resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	s.mu.Lock()
	if s.offline {
		s.mu.Unlock()
		dropConn(w)
		return
	}
	if len(s.failures) > 0 {
		f := s.failures[0]
		s.failures = s.failures[1:]
		s.mu.Unlock()
		if f.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(f.retryAfter))
		}
		resputil.FailStatus(w, f.status, f.status*100+1, "injected_failure")
		return
	}
	user, ok := s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	drop := s.dropResp > 0
	if drop {
		s.dropResp--
	}
	s.mu.Unlock()
	if !ok {
		resputil.FailStatus(w, http.StatusUnauthorized, CodeUnauthorized, "invalid_token")
		return
	}
	segs := strings.Split(strings.TrimPrefix(r.URL.Path, Prefix+"/"), "/")
	if drop {
		s.route(httptest.NewRecorder(), r, user, segs) // 请求照常生效, 应答丢失
		dropConn(w)
		return
	}
	s.route(w, r, user, segs)
}

func dropConn(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	conn, _, err := hj.Hijack()
	if err == nil {
		conn.Close()
	}
}

func (s *Server) count(kind string) {
	s.mu.Lock()
	s.counts[kind]++
	s.mu.Unlock()
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, user string, p []string) {
	m := r.Method
	switch {
	case len(p) == 1 && p[0] == "spaces" && m == http.MethodGet:
		s.listSpaces(w, user)
	case len(p) == 2 && p[0] == "keys" && p[1] == "me":
		s.myKeys(w, r, user)
	case len(p) >= 2 && p[0] == "devices" && p[1] == "requests":
		s.devicesRoute(w, r, user, p[2:])
	case len(p) >= 3 && p[0] == "spaces":
		s.spaceRoute(w, r, user, p[1], p[2:])
	default:
		resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
	}
}

func roleRank(role string) int {
	switch role {
	case syncsdk.RoleOwner:
		return 4
	case syncsdk.RoleAdmin:
		return 3
	case syncsdk.RoleMember:
		return 2
	case syncsdk.RoleViewer:
		return 1
	}
	return 0
}

// resolve 把路径里的空间标识换成内部空间, 校验成员身份。调用方须持锁。
func (s *Server) resolve(user, pathID string) (*space, string, bool) {
	key := pathID
	if pathID == syncsdk.PersonalSpace {
		key = PersonalKey(user)
	}
	sp, ok := s.spaces[key]
	if !ok {
		return nil, "", false
	}
	role, ok := sp.members[user]
	return sp, role, ok
}

func (s *Server) listSpaces(w http.ResponseWriter, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []syncsdk.Space{}
	keys := make([]string, 0, len(s.spaces))
	for k := range s.spaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sp := s.spaces[k]
		role, ok := sp.members[user]
		if !ok || (sp.typ == syncsdk.SpaceTypePersonal && k != PersonalKey(user)) {
			continue
		}
		var bytes int64
		for _, d := range sp.docs {
			bytes += int64(len(d.Payload))
		}
		out = append(out, syncsdk.Space{ID: sp.pathID, Type: sp.typ, Name: sp.name, Role: role, ReadOnly: sp.readonly,
			E2E: s.opts.E2E, Seq: sp.seq, Usage: syncsdk.Usage{Bytes: bytes, Docs: int64(len(sp.docs))}})
	}
	resputil.OK(w, map[string]any{"spaces": out})
}

func (s *Server) spaceRoute(w http.ResponseWriter, r *http.Request, user, pathID string, p []string) {
	m := r.Method
	switch {
	case len(p) == 1 && p[0] == "changes" && m == http.MethodGet:
		s.count("GET changes")
		s.changes(w, r, user, pathID)
	case len(p) == 3 && p[0] == "docs" && (m == http.MethodPut || m == http.MethodDelete):
		s.singleWrite(w, r, user, pathID, p[1], p[2])
	case len(p) == 4 && p[0] == "docs" && p[3] == "revisions" && m == http.MethodGet:
		s.revisions(w, user, pathID, p[1], p[2])
	case len(p) == 1 && p[0] == "batch" && m == http.MethodPost:
		s.count("POST batch")
		s.batch(w, r, user, pathID)
	case len(p) == 2 && p[0] == "blobs":
		s.count(m + " blob")
		s.blob(w, r, user, pathID, p[1])
	case len(p) == 1 && p[0] == "keys" && m == http.MethodGet:
		s.spaceKeys(w, user, pathID)
	case len(p) == 2 && p[0] == "keys" && m == http.MethodPut:
		s.putSpaceKeys(w, r, user, pathID, p[1])
	case len(p) == 1 && p[0] == "events" && m == http.MethodGet:
		s.events(w, r, user, pathID)
	case len(p) == 3 && p[0] == "presence" && m == http.MethodPut:
		s.touchPresence(w, r, user, pathID, p[1], p[2])
	default:
		resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
	}
}

// ---- changes ----

func (s *Server) changes(w http.ResponseWriter, r *http.Request, user, pathID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, _, ok := s.resolve(user, pathID)
	if !ok {
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > syncsdk.MaxChangesLimit {
		limit = syncsdk.MaxChangesLimit
	}
	if since > 0 && since < sp.minCursor {
		resputil.FailStatus(w, http.StatusGone, CodeCursorExpired, "cursor_expired")
		return
	}
	var docs []syncsdk.Doc
	for _, d := range sp.docs {
		if d.Rev > since {
			docs = append(docs, d.Doc)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Rev < docs[j].Rev })
	page := syncsdk.ChangesPage{Docs: docs, Cursor: sp.seq}
	if len(docs) > limit {
		page.Docs, page.HasMore, page.Cursor = docs[:limit], true, docs[limit-1].Rev
	}
	if page.Docs == nil {
		page.Docs = []syncsdk.Doc{}
	}
	device, _ := syncsdk.DeviceFromHeader(r.Header)
	page.Presence = s.presenceFor(sp, user, device)
	resputil.OK(w, page)
}

// presenceFor 下发给 (user, device) 的 presence: 排除其自己的心跳 (规则见 syncsdk.IsOwnPresence)。
func (s *Server) presenceFor(sp *space, user, device string) []syncsdk.Presence {
	now := s.now()
	out := []syncsdk.Presence{}
	for k, v := range sp.presence {
		if v.exp.Before(now) {
			delete(sp.presence, k)
			continue
		}
		p := syncsdk.Presence{Collection: k.collection, ID: k.id, UserID: k.user, DeviceID: k.device, DeviceName: v.name, ExpiresAt: v.exp}
		if !syncsdk.IsOwnPresence(p, user, device) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}

// ---- writes ----

// spaceWritable 做空间级写入校验。调用方须持锁。
func (s *Server) spaceWritable(user, pathID string) (*space, string, int, int, string) {
	sp, role, ok := s.resolve(user, pathID)
	if !ok {
		return nil, "", http.StatusForbidden, CodeNotMember, "not_member"
	}
	if sp.readonly {
		return nil, "", http.StatusForbidden, CodeReadOnly, "space_readonly"
	}
	return sp, role, 0, 0, ""
}

// write 执行一个写操作, 返回逐条结果。调用方须持锁。
func (s *Server) write(sp *space, role, user string, op syncsdk.BatchOp) syncsdk.BatchResult {
	fail := func(status, code int, msg string) syncsdk.BatchResult {
		return syncsdk.BatchResult{Status: status, Code: code, Msg: msg}
	}
	col, ok := s.collections[op.Collection]
	if !ok {
		return fail(http.StatusForbidden, CodeNotRegistered, "collection_not_registered")
	}
	if roleRank(role) < roleRank(col.MinRole) {
		return fail(http.StatusForbidden, CodeRoleTooLow, "role_too_low")
	}
	if op.ID == "" || (op.Op != syncsdk.OpPut && op.Op != syncsdk.OpDelete) {
		return fail(http.StatusBadRequest, CodeInvalidRequest, "invalid_op")
	}
	cur, exists := sp.docs[docKey{op.Collection, op.ID}]
	if col.AppendOnly && (op.Op == syncsdk.OpDelete || op.BaseRev != 0) {
		return fail(http.StatusForbidden, CodeAppendOnly, "collection_append_only")
	}
	var payload json.RawMessage
	if op.Op == syncsdk.OpPut {
		if len(op.Payload) > s.opts.MaxDocBytes {
			return fail(http.StatusRequestEntityTooLarge, CodeTooLarge, "payload_too_large")
		}
		var err error
		if payload, err = s.normalizePayload(op.Payload); err != nil {
			return fail(http.StatusBadRequest, CodeInvalidEnvelope, "invalid_payload")
		}
	}
	var curRev int64
	if exists {
		curRev = cur.Rev
	}
	if op.BaseRev != curRev {
		res := fail(http.StatusConflict, CodeVersionConflict, "version_conflict")
		if exists {
			d := cur.Doc
			res.Doc = &d
		}
		return res
	}
	sp.seq++
	next := syncsdk.Doc{Collection: op.Collection, ID: op.ID, Rev: sp.seq, Deleted: op.Op == syncsdk.OpDelete,
		Payload: payload, UpdatedBy: user, UpdatedAt: s.now().UTC().Truncate(time.Millisecond)}
	nd := &doc{Doc: next}
	if exists && col.Revisions > 0 {
		nd.history = append([]syncsdk.Doc{cur.Doc}, cur.history...)
		if len(nd.history) > col.Revisions {
			nd.history = nd.history[:col.Revisions]
		}
	}
	sp.docs[docKey{op.Collection, op.ID}] = nd
	s.broadcast(sp, fmt.Sprintf("event: changed\ndata: {\"seq\":%d}\n\n", sp.seq), "")
	meta := next
	meta.Payload = nil
	return syncsdk.BatchResult{Status: http.StatusOK, Code: 200, Doc: &meta}
}

// normalizePayload 校验 payload 并像 PostgreSQL jsonb 一样重排键、去空白,
// 让客户端无法依赖字节级一致。e2e required 时还校验信封格式。
func (s *Server) normalizePayload(raw json.RawMessage) (json.RawMessage, error) {
	if s.opts.E2E == syncsdk.E2ERequired {
		if _, err := e2e.ParseEnvelope(raw); err != nil {
			return nil, err
		}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func writeResult(w http.ResponseWriter, res syncsdk.BatchResult) {
	if res.Status == http.StatusOK {
		resputil.OK(w, res.Doc)
		return
	}
	var data any
	if res.Doc != nil {
		data = res.Doc
	}
	resputil.Write(w, res.Status, resputil.Response{Code: res.Code, Data: data, Msg: res.Msg})
}

func (s *Server) singleWrite(w http.ResponseWriter, r *http.Request, user, pathID, collection, id string) {
	op := syncsdk.BatchOp{Op: syncsdk.OpPut, Collection: collection, ID: id}
	if r.Method == http.MethodDelete {
		op.Op = syncsdk.OpDelete
		op.BaseRev, _ = strconv.ParseInt(r.URL.Query().Get("baseRev"), 10, 64)
	} else {
		var body syncsdk.PutDocRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, int64(s.opts.MaxDocBytes)*2)).Decode(&body); err != nil {
			resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "invalid_body")
			return
		}
		op.BaseRev, op.Payload = body.BaseRev, body.Payload
	}
	s.count(r.Method + " doc")
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, role, status, code, msg := s.spaceWritable(user, pathID)
	if sp == nil {
		resputil.FailStatus(w, status, code, msg)
		return
	}
	writeResult(w, s.write(sp, role, user, op))
}

func (s *Server) batch(w http.ResponseWriter, r *http.Request, user, pathID string) {
	var body syncsdk.BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Ops) == 0 || len(body.Ops) > syncsdk.MaxBatchOps {
		resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "invalid_batch")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, role, status, code, msg := s.spaceWritable(user, pathID)
	if sp == nil {
		resputil.FailStatus(w, status, code, msg)
		return
	}
	results := make([]syncsdk.BatchResult, len(body.Ops))
	for i, op := range body.Ops {
		results[i] = s.write(sp, role, user, op)
	}
	resputil.OK(w, syncsdk.BatchResponse{Results: results})
}

func (s *Server) revisions(w http.ResponseWriter, user, pathID, collection, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, _, ok := s.resolve(user, pathID)
	if !ok {
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	d, ok := sp.docs[docKey{collection, id}]
	if !ok {
		resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	resputil.OK(w, map[string]any{"revisions": append([]syncsdk.Doc{}, d.history...)})
}

// ---- blobs ----

func (s *Server) blob(w http.ResponseWriter, r *http.Request, user, pathID, blobID string) {
	s.mu.Lock()
	sp, _, ok := s.resolve(user, pathID)
	if !ok {
		s.mu.Unlock()
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	data, exists := sp.blobs[blobID]
	readonly := sp.readonly
	s.mu.Unlock()
	switch r.Method {
	case http.MethodHead:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if !exists {
			resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
			return
		}
		tok := randomID("")
		s.mu.Lock()
		s.presigned[tok] = data
		s.mu.Unlock()
		http.Redirect(w, r, "/_blob/"+tok, http.StatusFound)
	case http.MethodPut:
		if readonly {
			resputil.FailStatus(w, http.StatusForbidden, CodeReadOnly, "space_readonly")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, s.opts.MaxBlobBytes+1))
		if err != nil {
			resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "read_failed")
			return
		}
		if int64(len(body)) > s.opts.MaxBlobBytes {
			resputil.FailStatus(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "blob_too_large")
			return
		}
		if s.opts.E2E != syncsdk.E2ERequired {
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != blobID {
				resputil.FailStatus(w, http.StatusBadRequest, CodeDigestMismatch, "digest_mismatch")
				return
			}
		}
		s.mu.Lock()
		if _, ok := sp.blobs[blobID]; !ok { // 幂等: 已存在不覆盖
			sp.blobs[blobID] = body
		}
		s.mu.Unlock()
		resputil.OK(w, map[string]any{"blobId": blobID, "size": len(body)})
	default:
		resputil.FailStatus(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "method_not_allowed")
	}
}

func (s *Server) servePresigned(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	data, ok := s.presigned[strings.TrimPrefix(r.URL.Path, "/_blob/")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, bytes.NewReader(data))
}

// ---- keys ----

func (s *Server) myKeys(w http.ResponseWriter, r *http.Request, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.userKeys[user]
	if r.Method == http.MethodGet {
		if cur == nil {
			resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_enrolled")
			return
		}
		resputil.OK(w, cur)
		return
	}
	if r.Method != http.MethodPut {
		resputil.FailStatus(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "method_not_allowed")
		return
	}
	var in syncsdk.UserKeys
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Escrow) == 0 {
		resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "invalid_body")
		return
	}
	if _, err := e2e.ParsePublicKey(in.PublicKey); err != nil {
		resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "invalid_public_key")
		return
	}
	want := 1
	if cur != nil {
		want = cur.RecoveryVersion + 1
		if cur.PublicKey != in.PublicKey {
			resputil.Write(w, http.StatusConflict, resputil.Response{Code: CodeStateConflict, Data: cur, Msg: "public_key_exists"})
			return
		}
	}
	if in.RecoveryVersion != want {
		resputil.Write(w, http.StatusConflict, resputil.Response{Code: CodeStateConflict, Data: cur, Msg: "recovery_version_conflict"})
		return
	}
	in.UpdatedAt = s.now().UTC()
	s.userKeys[user] = &in
	resputil.OK(w, in)
}

func (s *Server) spaceKeys(w http.ResponseWriter, user, pathID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, role, ok := s.resolve(user, pathID)
	if !ok {
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	out := syncsdk.SpaceKeys{Role: role, CurrentVersion: sp.currentVersion, RotationRequired: sp.rotationRequired,
		Keys: []syncsdk.WrappedKey{}, Pending: []syncsdk.KeyMember{}, Members: []syncsdk.KeyMember{}}
	versions := make([]int, 0, len(sp.wrapped))
	for v := range sp.wrapped {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	for _, v := range versions {
		if wk, ok := sp.wrapped[v][user]; ok {
			out.Keys = append(out.Keys, syncsdk.WrappedKey{Version: v, Wrapped: wk})
		}
	}
	members := make([]string, 0, len(sp.members))
	for u := range sp.members {
		members = append(members, u)
	}
	sort.Strings(members)
	for _, u := range members {
		uk := s.userKeys[u]
		if uk == nil {
			continue
		}
		km := syncsdk.KeyMember{UserID: u, PublicKey: uk.PublicKey}
		out.Members = append(out.Members, km)
		if _, ok := sp.wrapped[sp.currentVersion][u]; sp.currentVersion > 0 && !ok {
			out.Pending = append(out.Pending, km)
		}
	}
	resputil.OK(w, out)
}

func (s *Server) putSpaceKeys(w http.ResponseWriter, r *http.Request, user, pathID, version string) {
	v, err := strconv.Atoi(version)
	var in syncsdk.PutSpaceKeysRequest
	if err != nil || v <= 0 || json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Grants) == 0 {
		resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "invalid_body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, role, ok := s.resolve(user, pathID)
	if !ok {
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	if roleRank(role) < roleRank(syncsdk.RoleAdmin) {
		resputil.FailStatus(w, http.StatusForbidden, CodeRoleTooLow, "role_too_low")
		return
	}
	grants := map[string]string{}
	for _, g := range in.Grants {
		uid := g.UserID
		if uid == "" {
			uid = user
		}
		if _, member := sp.members[uid]; !member || s.userKeys[uid] == nil || g.Wrapped == "" {
			resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "grant_for_non_member")
			return
		}
		grants[uid] = g.Wrapped
	}
	if in.Rotate {
		if v != sp.currentVersion+1 {
			resputil.FailStatus(w, http.StatusConflict, CodeStateConflict, "version_conflict")
			return
		}
		for u := range sp.members {
			if _, ok := grants[u]; !ok && s.userKeys[u] != nil {
				resputil.FailStatus(w, http.StatusBadRequest, CodeIncompleteGrants, "grants_must_cover_all_members")
				return
			}
		}
		sp.wrapped[v] = grants
		sp.currentVersion = v
		sp.rotationRequired = false
		resputil.OK(w, map[string]any{"version": v})
		return
	}
	if v > sp.currentVersion {
		resputil.FailStatus(w, http.StatusConflict, CodeStateConflict, "version_not_found")
		return
	}
	for u, wk := range grants {
		if _, ok := sp.wrapped[v][u]; !ok { // 已有的不覆盖, 重复提交幂等
			sp.wrapped[v][u] = wk
		}
	}
	resputil.OK(w, map[string]any{"version": v})
}

// ---- devices ----

func (s *Server) devicesRoute(w http.ResponseWriter, r *http.Request, user string, p []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, d := range s.devices {
		if now.After(d.req.ExpiresAt) {
			delete(s.devices, id)
		}
	}
	if len(p) == 0 {
		switch r.Method {
		case http.MethodPost:
			var in syncsdk.CreateDeviceRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.TempPublicKey == "" || in.Commitment == "" {
				resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "invalid_body")
				return
			}
			req := syncsdk.DeviceRequest{ID: randomID("dev_"), DeviceName: in.DeviceName, TempPublicKey: in.TempPublicKey,
				Commitment: in.Commitment, Status: syncsdk.DeviceStatusPending,
				CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(10 * time.Minute)}
			s.devices[req.ID] = &device{user: user, req: req}
			resputil.OK(w, req)
		case http.MethodGet:
			out := []syncsdk.DeviceRequest{}
			for _, d := range s.devices {
				if d.user == user {
					out = append(out, d.req)
				}
			}
			sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
			resputil.OK(w, map[string]any{"requests": out})
		default:
			resputil.FailStatus(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "method_not_allowed")
		}
		return
	}
	d, ok := s.devices[p[0]]
	if !ok || d.user != user {
		resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	if len(p) == 1 {
		switch r.Method {
		case http.MethodGet:
			resputil.OK(w, d.req)
		case http.MethodDelete:
			delete(s.devices, p[0])
			resputil.OK(w, nil)
		default:
			resputil.FailStatus(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "method_not_allowed")
		}
		return
	}
	var in map[string]string
	_ = json.NewDecoder(r.Body).Decode(&in)
	setOnce := func(field *string, value, requiredStatus, nextStatus string) bool {
		if value == "" {
			resputil.FailStatus(w, http.StatusBadRequest, CodeInvalidRequest, "missing_value")
			return false
		}
		if *field != "" {
			if *field != value {
				resputil.FailStatus(w, http.StatusConflict, CodeStateConflict, "already_set")
				return false
			}
			return true
		}
		if d.req.Status != requiredStatus {
			resputil.FailStatus(w, http.StatusConflict, CodeStateConflict, "wrong_state")
			return false
		}
		*field = value
		d.req.Status = nextStatus
		return true
	}
	ok = true
	switch p[1] {
	case "challenge":
		ok = setOnce(&d.req.ApproverNonce, in["approverNonce"], syncsdk.DeviceStatusPending, syncsdk.DeviceStatusChallenged)
	case "reveal":
		ok = setOnce(&d.req.DeviceNonce, in["deviceNonce"], syncsdk.DeviceStatusChallenged, syncsdk.DeviceStatusRevealed)
	case "approve":
		ok = setOnce(&d.req.SealedKey, in["sealedKey"], syncsdk.DeviceStatusRevealed, syncsdk.DeviceStatusApproved)
	case "claim":
		if d.req.Status != syncsdk.DeviceStatusApproved {
			resputil.FailStatus(w, http.StatusConflict, CodeStateConflict, "not_approved")
			return
		}
		delete(s.devices, p[0])
	default:
		resputil.FailStatus(w, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	if ok {
		resputil.OK(w, d.req)
	}
}

// ---- events & presence ----

func (s *Server) broadcast(sp *space, msg, except string) {
	for ch, sub := range sp.subs {
		if sub.user == except {
			continue
		}
		select {
		case ch <- msg:
		default: // 慢订阅者丢事件, 客户端靠兜底轮询追上
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, user, pathID string) {
	s.mu.Lock()
	sp, _, ok := s.resolve(user, pathID)
	if !ok {
		s.mu.Unlock()
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	ch := make(chan string, 16)
	device, _ := syncsdk.DeviceFromHeader(r.Header)
	sp.subs[ch] = subscriber{user: user, device: device}
	seq := sp.seq
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(sp.subs, ch)
		s.mu.Unlock()
	}()
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && last < seq {
		fmt.Fprintf(w, "event: changed\ndata: {\"seq\":%d}\n\n", seq)
	}
	if flusher != nil {
		flusher.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			s.mu.Lock()
			off := s.offline
			s.mu.Unlock()
			if off {
				return
			}
			if _, err := io.WriteString(w, msg); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// DropEventStreams 断开该空间全部 SSE 连接 (模拟网络抖动)。
func (s *Server) DropEventStreams(spaceKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.spaces[spaceKey]
	for ch := range sp.subs {
		delete(sp.subs, ch)
		close(ch)
	}
}

func (s *Server) touchPresence(w http.ResponseWriter, r *http.Request, user, pathID, collection, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, _, ok := s.resolve(user, pathID)
	if !ok {
		resputil.FailStatus(w, http.StatusForbidden, CodeNotMember, "not_member")
		return
	}
	device, name := syncsdk.DeviceFromHeader(r.Header)
	exp := s.now().Add(60 * time.Second)
	sp.presence[presenceKey{collection, id, user, device}] = presenceVal{exp: exp, name: name}
	self := syncsdk.Presence{UserID: user, DeviceID: device}
	for ch, sub := range sp.subs {
		if syncsdk.IsOwnPresence(self, sub.user, sub.device) {
			continue
		}
		b, _ := json.Marshal(map[string]any{"presence": s.presenceFor(sp, sub.user, sub.device)})
		select {
		case ch <- "event: presence\ndata: " + string(b) + "\n\n":
		default:
		}
	}
	resputil.OK(w, nil)
}
