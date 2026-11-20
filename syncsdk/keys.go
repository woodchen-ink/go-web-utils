package syncsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/woodchen-ink/go-web-utils/syncsdk/e2e"
)

// KeyStatus 是本机端到端加密密钥的状态。
type KeyStatus int

const (
	// KeyStatusNotEnrolled: 服务端没有本人公钥, 需 Enroll (首次开通)。
	KeyStatusNotEnrolled KeyStatus = iota
	// KeyStatusLocked: 服务端已开通, 本机没有私钥, 需恢复码或已有设备批准。
	KeyStatusLocked
	// KeyStatusUnlocked: 本机持有与服务端公钥一致的私钥。
	KeyStatusUnlocked
)

func (s KeyStatus) String() string {
	switch s {
	case KeyStatusNotEnrolled:
		return "not_enrolled"
	case KeyStatusLocked:
		return "locked"
	case KeyStatusUnlocked:
		return "unlocked"
	}
	return "unknown"
}

// PendingMember 是等待授予空间密钥的成员, 交给 KeyManagerOptions.ApproveGrant 决定。
type PendingMember struct {
	Space     string
	UserID    string
	PublicKey e2e.PublicKey
	// Fingerprint 是成员公钥指纹, 界面展示供人工核对。
	Fingerprint string
}

// KeyManagerOptions 配置 KeyManager。
type KeyManagerOptions struct {
	// Namespace 区分同一密钥库里的多个账号 / 应用, 建议用 "<应用>/<用户 ID>"。必填。
	Namespace string
	// ApproveGrant 决定是否为待授予成员封装密钥; nil 表示 owner / admin 同步时自动授予
	// (设计文档的默认行为)。可在这里展示指纹、记录授予, 或改为人工确认。
	ApproveGrant func(ctx context.Context, m PendingMember) bool
	// Now 默认 time.Now, 测试可替换。
	Now func() time.Time
	// PollInterval 是设备授权轮询间隔, 默认 2 秒。
	PollInterval time.Duration
}

// KeyManager 负责端到端加密的密钥生命周期: 开通、恢复码、设备授权、空间密钥的
// 建立 / 补授 / 轮换, 以及本机密钥库的存取。应用只提供 KeyStore 与界面回调。并发安全。
type KeyManager struct {
	client *Client
	store  KeyStore
	opts   KeyManagerOptions

	mu    sync.Mutex
	user  *e2e.UserKey
	rings map[string]*e2e.Keyring
	// spaceMu 串行化同一空间的密钥同步, 避免同一客户端并发轮换。
	spaceMu sync.Map
}

// NewKeyManager 创建 KeyManager。创建后调用 Status 或 Load 读取本机私钥。
func NewKeyManager(client *Client, store KeyStore, opts KeyManagerOptions) (*KeyManager, error) {
	if client == nil || store == nil || opts.Namespace == "" {
		return nil, errors.New("syncsdk: NewKeyManager needs client, store and Namespace")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	return &KeyManager{client: client, store: store, opts: opts, rings: map[string]*e2e.Keyring{}}, nil
}

func (m *KeyManager) userKeyName() string { return m.opts.Namespace + "/user-key" }
func (m *KeyManager) ringName(space string) string {
	return m.opts.Namespace + "/space/" + space
}

// Load 从本机密钥库读取用户私钥, 返回是否存在。不联网。
func (m *KeyManager) Load(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.user != nil {
		return true, nil
	}
	b, err := m.store.Get(ctx, m.userKeyName())
	if errors.Is(err, ErrKeyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer e2e.Zero(b)
	u, err := e2e.UserKeyFromBytes(b)
	if err != nil {
		return false, err
	}
	m.user = u
	return true, nil
}

// Status 对照服务端公钥判断本机状态。本机私钥与服务端公钥不一致 (例如在别处重新开通过)
// 时返回 KeyStatusLocked, 本机旧私钥不会被删除, 由应用决定是否 Forget。
func (m *KeyManager) Status(ctx context.Context) (KeyStatus, error) {
	if _, err := m.Load(ctx); err != nil {
		return 0, err
	}
	remote, err := m.client.MyKeys(ctx)
	if errors.Is(err, ErrNotFound) {
		return KeyStatusNotEnrolled, nil
	}
	if err != nil {
		return 0, err
	}
	pub, err := e2e.ParsePublicKey(remote.PublicKey)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	u := m.userKey()
	if u == nil || !u.Public().Equal(pub) {
		return KeyStatusLocked, nil
	}
	return KeyStatusUnlocked, nil
}

func (m *KeyManager) userKey() *e2e.UserKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.user
}

// requireUser 返回本机用户密钥, 没有返回 ErrLocked。
func (m *KeyManager) requireUser(ctx context.Context) (*e2e.UserKey, error) {
	if ok, err := m.Load(ctx); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrLocked
	}
	return m.userKey(), nil
}

// PublicKey 返回本人公钥; 本机未解锁返回 ErrLocked。
func (m *KeyManager) PublicKey(ctx context.Context) (e2e.PublicKey, error) {
	u, err := m.requireUser(ctx)
	if err != nil {
		return e2e.PublicKey{}, err
	}
	return u.Public(), nil
}

func (m *KeyManager) saveUser(ctx context.Context, u *e2e.UserKey) error {
	b := u.Bytes()
	defer e2e.Zero(b)
	if err := m.store.Put(ctx, m.userKeyName(), b); err != nil {
		return err
	}
	m.mu.Lock()
	m.user = u
	m.mu.Unlock()
	return nil
}

// Enroll 首次开通: 生成用户密钥对与恢复码, 上传公钥与私钥加密副本, 私钥存入本机密钥库。
// 返回的恢复码只此一次, 应用必须展示给用户并要求确认已保存 (服务端与客服都无法代为恢复)。
// 服务端已有公钥 (另一台设备抢先开通) 返回 ErrConflict, 此时应改走解锁流程。
func (m *KeyManager) Enroll(ctx context.Context) (*e2e.RecoveryCode, error) {
	u, err := e2e.GenerateUserKey()
	if err != nil {
		return nil, err
	}
	code, err := e2e.NewRecoveryCode()
	if err != nil {
		return nil, err
	}
	escrow, err := e2e.SealEscrow(u, code)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(escrow)
	if _, err := m.client.PutMyKeys(ctx, UserKeys{PublicKey: u.Public().String(), Escrow: raw, RecoveryVersion: 1}); err != nil {
		code.Destroy()
		return nil, err
	}
	if err := m.saveUser(ctx, u); err != nil {
		return nil, err
	}
	return code, nil
}

// UnlockWithRecoveryCode 在本机没有私钥时, 用恢复码解开服务端副本并存入本机密钥库。
// 恢复码有误 (校验位不对) 返回 e2e.ErrInvalidRecoveryCode, 解密失败返回 e2e.ErrDecrypt。
func (m *KeyManager) UnlockWithRecoveryCode(ctx context.Context, recoveryCode string) error {
	code, err := e2e.ParseRecoveryCode(recoveryCode)
	if err != nil {
		return err
	}
	defer code.Destroy()
	remote, err := m.client.MyKeys(ctx)
	if errors.Is(err, ErrNotFound) {
		return ErrNotEnrolled
	}
	if err != nil {
		return err
	}
	pub, err := e2e.ParsePublicKey(remote.PublicKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	var escrow e2e.Escrow
	if err := json.Unmarshal(remote.Escrow, &escrow); err != nil {
		return fmt.Errorf("%w: escrow: %v", ErrProtocol, err)
	}
	u, err := e2e.OpenEscrow(&escrow, code, pub)
	if err != nil {
		return err
	}
	return m.saveUser(ctx, u)
}

// ResetRecoveryCode 在已解锁的设备上生成新恢复码并替换服务端私钥副本, 旧恢复码随之作废。
// 返回的新恢复码同样只展示一次。
func (m *KeyManager) ResetRecoveryCode(ctx context.Context) (*e2e.RecoveryCode, error) {
	u, err := m.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	remote, err := m.client.MyKeys(ctx)
	if err != nil {
		return nil, err
	}
	pub, err := e2e.ParsePublicKey(remote.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if !pub.Equal(u.Public()) {
		return nil, ErrLocked
	}
	code, err := e2e.NewRecoveryCode()
	if err != nil {
		return nil, err
	}
	escrow, err := e2e.SealEscrow(u, code)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(escrow)
	_, err = m.client.PutMyKeys(ctx, UserKeys{PublicKey: pub.String(), Escrow: raw, RecoveryVersion: remote.RecoveryVersion + 1})
	if err != nil {
		code.Destroy()
		return nil, err
	}
	return code, nil
}

// Forget 删除本机保存的用户私钥与空间密钥缓存 (退出登录、切换账号时调用)。
// spaces 为本机缓存过的空间, 应用可从 Spaces() 结果传入。
func (m *KeyManager) Forget(ctx context.Context, spaces ...string) error {
	m.mu.Lock()
	m.user = nil
	for s, r := range m.rings {
		r.Destroy()
		delete(m.rings, s)
	}
	m.mu.Unlock()
	if err := m.store.Delete(ctx, m.userKeyName()); err != nil {
		return err
	}
	for _, s := range spaces {
		if err := m.store.Delete(ctx, m.ringName(s)); err != nil {
			return err
		}
	}
	return nil
}

// cachedRing 返回内存或本机密钥库里的空间密钥, 都没有返回空 keyring。
func (m *KeyManager) cachedRing(ctx context.Context, space string) (*e2e.Keyring, error) {
	m.mu.Lock()
	if r, ok := m.rings[space]; ok {
		m.mu.Unlock()
		return r, nil
	}
	m.mu.Unlock()
	ring := e2e.EmptyKeyring(space)
	b, err := m.store.Get(ctx, m.ringName(space))
	switch {
	case errors.Is(err, ErrKeyNotFound):
	case err != nil:
		return nil, err
	default:
		err := ring.UnmarshalBinary(b)
		e2e.Zero(b)
		if err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rings[space]; ok {
		return r, nil
	}
	m.rings[space] = ring
	return ring, nil
}

func (m *KeyManager) persistRing(ctx context.Context, ring *e2e.Keyring) error {
	if ring.Empty() {
		return nil
	}
	b, err := ring.MarshalBinary()
	if err != nil {
		return err
	}
	defer e2e.Zero(b)
	return m.store.Put(ctx, m.ringName(ring.Space()), b)
}

func (m *KeyManager) swapRing(ctx context.Context, ring *e2e.Keyring) error {
	if err := m.persistRing(ctx, ring); err != nil {
		return err
	}
	// 被替换的旧 keyring 可能仍被进行中的加解密引用, 不清零, 交给 GC。
	m.mu.Lock()
	m.rings[ring.Space()] = ring
	m.mu.Unlock()
	return nil
}

// Keyring 返回空间密钥; 本机没有缓存时联网同步一次 (SyncSpace)。
// 本人尚未被授予任何版本时返回 ErrKeyNotGranted。
func (m *KeyManager) Keyring(ctx context.Context, space string) (*e2e.Keyring, error) {
	ring, err := m.cachedRing(ctx, space)
	if err != nil {
		return nil, err
	}
	if !ring.Empty() {
		return ring, nil
	}
	if _, err := m.SyncSpace(ctx, space); err != nil {
		return nil, err
	}
	ring, err = m.cachedRing(ctx, space)
	if err != nil {
		return nil, err
	}
	if ring.Empty() {
		return nil, ErrKeyNotGranted
	}
	return ring, nil
}

// SpaceSyncReport 汇报一次 SyncSpace 做了什么。
type SpaceSyncReport struct {
	// Created 为 true 表示本客户端建立了空间密钥 version 1。
	Created bool
	// Granted 是本次补授了密钥的成员。
	Granted []PendingMember
	// Rotated 是本次轮换出的新版本, 0 表示未轮换。
	Rotated int
	// CurrentVersion 是同步后本机的当前版本。
	CurrentVersion int
}

func canManageKeys(role string) bool { return role == RoleOwner || role == RoleAdmin }

func (m *KeyManager) lockSpace(space string) func() {
	v, _ := m.spaceMu.LoadOrStore(space, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// SyncSpace 拉取并解开本人在空间的全部封装密钥, 合入本机缓存; 本人是 owner / admin 时还会:
// 空间尚无密钥则建立 version 1, 服务端标记需要轮换 (有成员被移除) 则轮换,
// 有待授予成员则为其封装全部版本。引擎在每轮同步开始时调用。
func (m *KeyManager) SyncSpace(ctx context.Context, space string) (*SpaceSyncReport, error) {
	user, err := m.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	unlock := m.lockSpace(space)
	defer unlock()

	report := &SpaceSyncReport{}
	for attempt := 0; attempt < 3; attempt++ {
		sk, err := m.client.SpaceKeys(ctx, space)
		if err != nil {
			return nil, err
		}
		ring, err := m.mergeRemote(ctx, space, sk, user)
		if err != nil {
			return nil, err
		}
		if !canManageKeys(sk.Role) {
			report.CurrentVersion = ring.Current()
			return report, nil
		}
		retry, err := m.manage(ctx, space, sk, ring, user, report)
		if errors.Is(err, ErrConflict) && retry {
			continue // 另一台管理端抢先建立或轮换, 重新拉取
		}
		if err != nil {
			return nil, err
		}
		if !retry {
			cur, _ := m.cachedRing(ctx, space)
			report.CurrentVersion = cur.Current()
			return report, nil
		}
	}
	return nil, fmt.Errorf("%w: space keys kept changing", ErrConflict)
}

// mergeRemote 解开服务端下发的封装密钥并与本机缓存合并。
// 当前版本只取本机见过的最大值, 服务端无法把空间回退到旧密钥。
func (m *KeyManager) mergeRemote(ctx context.Context, space string, sk *SpaceKeys, user *e2e.UserKey) (*e2e.Keyring, error) {
	cached, err := m.cachedRing(ctx, space)
	if err != nil {
		return nil, err
	}
	ring := cached.Clone()
	changed := false
	for _, wk := range sk.Keys {
		if ring.Has(wk.Version) {
			continue
		}
		raw, err := e2e.DecodeBase64(wk.Wrapped)
		if err != nil {
			return nil, fmt.Errorf("%w: wrapped key", ErrProtocol)
		}
		if err := ring.AddWrapped(wk.Version, raw, user); err != nil {
			return nil, fmt.Errorf("%w: space %s key v%d: %v", ErrProtocol, space, wk.Version, err)
		}
		changed = true
	}
	if !changed {
		return cached, nil
	}
	if err := m.swapRing(ctx, ring); err != nil {
		return nil, err
	}
	return ring, nil
}

// manage 执行管理端职责, retry 表示状态已变更、应重新拉取一轮。
func (m *KeyManager) manage(ctx context.Context, space string, sk *SpaceKeys, ring *e2e.Keyring, user *e2e.UserKey, report *SpaceSyncReport) (retry bool, err error) {
	members, err := parseMembers(sk.Members, user)
	if err != nil {
		return false, err
	}
	switch {
	case sk.CurrentVersion == 0 && ring.Empty():
		fresh, err := e2e.NewKeyring(space)
		if err != nil {
			return false, err
		}
		if err := m.putVersion(ctx, space, fresh, 1, members); err != nil {
			return errors.Is(err, ErrConflict), err
		}
		report.Created = true
		return true, m.swapRing(ctx, fresh)
	case ring.Empty() || ring.Current() < sk.CurrentVersion:
		// 本人还没有当前版本: 等其他管理端补授, 不能基于旧版本轮换或补授。
		return false, nil
	case sk.RotationRequired:
		next := ring.Clone()
		v, err := next.Rotate()
		if err != nil {
			return false, err
		}
		if err := m.putVersion(ctx, space, next, v, members); err != nil {
			return errors.Is(err, ErrConflict), err
		}
		report.Rotated = v
		return true, m.swapRing(ctx, next)
	}
	return m.grantPending(ctx, space, sk.Pending, ring, report)
}

// grantPending 为通过 ApproveGrant 的待授予成员封装本机持有的全部版本,
// 每个版本一次 PUT, 旧版本在前、当前版本最后 (服务端以拿到当前版本视为授予完成)。
func (m *KeyManager) grantPending(ctx context.Context, space string, pending []KeyMember, ring *e2e.Keyring, report *SpaceSyncReport) (bool, error) {
	var approved []PendingMember
	for _, km := range pending {
		pk, err := e2e.ParsePublicKey(km.PublicKey)
		if err != nil {
			continue // 公钥非法的成员跳过, 不影响其他人
		}
		pm := PendingMember{Space: space, UserID: km.UserID, PublicKey: pk, Fingerprint: pk.Fingerprint()}
		if m.opts.ApproveGrant != nil && !m.opts.ApproveGrant(ctx, pm) {
			continue
		}
		approved = append(approved, pm)
	}
	if len(approved) == 0 {
		return false, nil
	}
	for _, v := range ring.Versions() {
		grants := make([]KeyGrant, 0, len(approved))
		for _, pm := range approved {
			w, err := ring.Wrap(v, pm.PublicKey)
			if err != nil {
				return false, err
			}
			grants = append(grants, KeyGrant{UserID: pm.UserID, Wrapped: e2e.EncodeBase64(w)})
		}
		if err := m.client.PutSpaceKeys(ctx, space, v, false, grants); err != nil {
			return errors.Is(err, ErrConflict), err
		}
	}
	report.Granted = append(report.Granted, approved...)
	return false, nil
}

type memberKey struct {
	userID string
	pub    e2e.PublicKey
}

// parseMembers 解析成员公钥, 并确保本人在列 (服务端遗漏时补上)。
func parseMembers(in []KeyMember, self *e2e.UserKey) ([]memberKey, error) {
	var out []memberKey
	hasSelf := false
	for _, km := range in {
		pk, err := e2e.ParsePublicKey(km.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("%w: member %s public key", ErrProtocol, km.UserID)
		}
		if pk.Equal(self.Public()) {
			hasSelf = true
		}
		out = append(out, memberKey{km.UserID, pk})
	}
	if !hasSelf {
		out = append(out, memberKey{"", self.Public()})
	}
	return out, nil
}

func (m *KeyManager) putVersion(ctx context.Context, space string, ring *e2e.Keyring, v int, members []memberKey) error {
	grants := make([]KeyGrant, 0, len(members))
	for _, mk := range members {
		w, err := ring.Wrap(v, mk.pub)
		if err != nil {
			return err
		}
		grants = append(grants, KeyGrant{UserID: mk.userID, Wrapped: e2e.EncodeBase64(w)})
	}
	return m.client.PutSpaceKeys(ctx, space, v, true, grants)
}

// RotateSpace 立即轮换空间密钥 (owner / admin), 新版本封装给服务端列出的全部现有成员。
// 通常不需要手动调用: 成员被移除后服务端置 rotationRequired, 下一次 SyncSpace 自动轮换。
func (m *KeyManager) RotateSpace(ctx context.Context, space string) (int, error) {
	user, err := m.requireUser(ctx)
	if err != nil {
		return 0, err
	}
	unlock := m.lockSpace(space)
	defer unlock()
	sk, err := m.client.SpaceKeys(ctx, space)
	if err != nil {
		return 0, err
	}
	if !canManageKeys(sk.Role) {
		return 0, ErrForbidden
	}
	ring, err := m.mergeRemote(ctx, space, sk, user)
	if err != nil {
		return 0, err
	}
	if ring.Empty() || ring.Current() < sk.CurrentVersion {
		return 0, ErrKeyNotGranted
	}
	members, err := parseMembers(sk.Members, user)
	if err != nil {
		return 0, err
	}
	next := ring.Clone()
	v, err := next.Rotate()
	if err != nil {
		return 0, err
	}
	if err := m.putVersion(ctx, space, next, v, members); err != nil {
		return 0, err
	}
	return v, m.swapRing(ctx, next)
}
