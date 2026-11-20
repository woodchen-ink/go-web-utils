package e2e

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"

	"golang.org/x/crypto/nacl/box"
)

// 封装空间密钥的明文 (放进 sealed box 之前):
//
//	"CZLSK1" || uint32 version || LP(space) || spaceKey[32] || blobIDKey[32]
//
// sealed box 没有关联数据, 所以把空间标识与版本放进明文, 解封时校验,
// 防止服务端把一个空间的密钥冒充另一个空间的, 或篡改版本号。
const wrapMagic = "CZLSK1"

// Keyring 持有一个空间的密钥: 带版本的 AES-256-GCM 空间密钥, 与不轮换的附件标识 HMAC 密钥。并发安全。
//
// 当前版本只进不退: keyring 永远用见过的最新版本加密, 服务端无法把空间回退到被移除成员仍持有的旧密钥。
type Keyring struct {
	space string

	mu      sync.RWMutex
	current int
	keys    map[int][]byte
	blobKey []byte
}

// NewKeyring 为新空间生成密钥: version 1 与附件标识密钥。
func NewKeyring(space string) (*Keyring, error) {
	k := &Keyring{space: space, keys: map[int][]byte{}}
	key, err := randomKey()
	if err != nil {
		return nil, err
	}
	blob, err := randomKey()
	if err != nil {
		return nil, err
	}
	k.keys[1] = key
	k.blobKey = blob
	k.current = 1
	return k, nil
}

// EmptyKeyring 返回该空间的空 keyring, 之后用 AddWrapped 或 UnmarshalBinary 填充。
func EmptyKeyring(space string) *Keyring {
	return &Keyring{space: space, keys: map[int][]byte{}}
}

func randomKey() ([]byte, error) {
	k := make([]byte, KeySize)
	_, err := rand.Read(k)
	return k, err
}

// Space 返回所属空间标识。
func (k *Keyring) Space() string { return k.space }

// Current 返回新加密使用的版本, 空 keyring 为 0。
func (k *Keyring) Current() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.current
}

// Has 判断是否持有版本 v。
func (k *Keyring) Has(v int) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	_, ok := k.keys[v]
	return ok
}

// Versions 升序返回持有的版本。
func (k *Keyring) Versions() []int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	vs := make([]int, 0, len(k.keys))
	for v := range k.keys {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}

// Empty 判断是否一个密钥都没有。
func (k *Keyring) Empty() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.keys) == 0
}

// Rotate 生成比已持有最新版本高一版的随机空间密钥并设为当前, 附件标识密钥不变, 返回新版本号。
func (k *Keyring) Rotate() (int, error) {
	key, err := randomKey()
	if err != nil {
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.blobKey == nil {
		return 0, fmt.Errorf("%w: rotating an empty keyring", ErrKeyMismatch)
	}
	v := k.current + 1
	k.keys[v] = key
	k.current = v
	return v, nil
}

// Wrap 用匿名 sealed box (X25519 + XSalsa20-Poly1305) 把版本 v (连同附件标识密钥) 封装给成员公钥,
// 结果即服务端按 (空间, 版本, 成员) 保存的内容。
func (k *Keyring) Wrap(v int, member PublicKey) ([]byte, error) {
	k.mu.RLock()
	key, ok := k.keys[v]
	if !ok {
		k.mu.RUnlock()
		return nil, ErrUnknownKeyVersion
	}
	pt := make([]byte, 0, len(wrapMagic)+4+4+len(k.space)+2*KeySize)
	pt = append(pt, wrapMagic...)
	pt = binary.BigEndian.AppendUint32(pt, uint32(v))
	pt = appendLP(pt, []byte(k.space))
	pt = append(pt, key...)
	pt = append(pt, k.blobKey...)
	k.mu.RUnlock()
	defer Zero(pt)
	pub := [KeySize]byte(member)
	return box.SealAnonymous(nil, pt, &pub, rand.Reader)
}

// AddWrapped 解开封装给 user 的密钥并作为版本 v 加入。封装内容指向别的空间或版本、
// 附件标识密钥与已持有的不同、或同版本已持有不同密钥时返回 ErrKeyMismatch。
func (k *Keyring) AddWrapped(v int, wrapped []byte, user *UserKey) error {
	priv := user.secret()
	defer Zero(priv[:])
	pub := [KeySize]byte(user.Public())
	pt, ok := box.OpenAnonymous(nil, wrapped, &pub, priv)
	if !ok {
		return ErrDecrypt
	}
	defer Zero(pt)
	if len(pt) < len(wrapMagic)+4 || string(pt[:len(wrapMagic)]) != wrapMagic {
		return fmt.Errorf("%w: wrapped key", ErrMalformed)
	}
	rest := pt[len(wrapMagic):]
	gotV := int(binary.BigEndian.Uint32(rest))
	space, rest, ok := readLP(rest[4:])
	if !ok || len(rest) != 2*KeySize {
		return fmt.Errorf("%w: wrapped key", ErrMalformed)
	}
	if gotV != v || string(space) != k.space {
		return ErrKeyMismatch
	}
	return k.add(v, rest[:KeySize], rest[KeySize:])
}

func (k *Keyring) add(v int, key, blobKey []byte) error {
	if v <= 0 {
		return fmt.Errorf("%w: key version", ErrMalformed)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.blobKey != nil && !equal(k.blobKey, blobKey) {
		return ErrKeyMismatch
	}
	if old, ok := k.keys[v]; ok {
		if !equal(old, key) {
			return ErrKeyMismatch
		}
		return nil
	}
	if k.blobKey == nil {
		k.blobKey = append([]byte(nil), blobKey...)
	}
	k.keys[v] = append([]byte(nil), key...)
	if v > k.current {
		k.current = v
	}
	return nil
}

// key 返回版本 v 的密钥, 返回值不得修改或长期持有。
func (k *Keyring) key(v int) ([]byte, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[v]
	return key, ok
}

// currentKey 返回当前版本及其密钥。
func (k *Keyring) currentKey() (int, []byte, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[k.current]
	return k.current, key, ok
}

// 本机缓存格式 (MarshalBinary), 只用于系统密钥库:
//
//	"CZLKR1" || LP(space) || blobIDKey[32] || uint32 count ||
//	count × (uint32 version || key[32])
const ringMagic = "CZLKR1"

// MarshalBinary 序列化全部密钥供本机密钥库保存。输出是秘密: 只存系统密钥库, 用完清零。
func (k *Keyring) MarshalBinary() ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.blobKey == nil {
		return nil, fmt.Errorf("%w: empty keyring", ErrMalformed)
	}
	vs := make([]int, 0, len(k.keys))
	for v := range k.keys {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	out := appendLP([]byte(ringMagic), []byte(k.space))
	out = append(out, k.blobKey...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(vs)))
	for _, v := range vs {
		out = binary.BigEndian.AppendUint32(out, uint32(v))
		out = append(out, k.keys[v]...)
	}
	return out, nil
}

// UnmarshalBinary 把 MarshalBinary 的输出合并进 k, 其中的空间标识必须与 k 一致。
func (k *Keyring) UnmarshalBinary(data []byte) error {
	if len(data) < len(ringMagic) || string(data[:len(ringMagic)]) != ringMagic {
		return fmt.Errorf("%w: keyring", ErrMalformed)
	}
	space, rest, ok := readLP(data[len(ringMagic):])
	if !ok || len(rest) < KeySize+4 {
		return fmt.Errorf("%w: keyring", ErrMalformed)
	}
	if string(space) != k.space {
		return ErrKeyMismatch
	}
	blobKey := rest[:KeySize]
	n := binary.BigEndian.Uint32(rest[KeySize:])
	rest = rest[KeySize+4:]
	if uint64(len(rest)) != uint64(n)*(4+KeySize) {
		return fmt.Errorf("%w: keyring", ErrMalformed)
	}
	for i := uint32(0); i < n; i++ {
		v := int(binary.BigEndian.Uint32(rest))
		if err := k.add(v, rest[4:4+KeySize], blobKey); err != nil {
			return err
		}
		rest = rest[4+KeySize:]
	}
	return nil
}

// Clone 返回独立副本。轮换时先在副本上 Rotate, 服务端接受新版本后再替换,
// 被拒绝的轮换不会留下孤立版本。
func (k *Keyring) Clone() *Keyring {
	k.mu.RLock()
	defer k.mu.RUnlock()
	c := &Keyring{space: k.space, current: k.current, keys: make(map[int][]byte, len(k.keys))}
	for v, key := range k.keys {
		c.keys[v] = append([]byte(nil), key...)
	}
	if k.blobKey != nil {
		c.blobKey = append([]byte(nil), k.blobKey...)
	}
	return c
}

// Destroy 清零 keyring 持有的全部密钥。
func (k *Keyring) Destroy() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for v, key := range k.keys {
		Zero(key)
		delete(k.keys, v)
	}
	Zero(k.blobKey)
	k.blobKey = nil
	k.current = 0
}
