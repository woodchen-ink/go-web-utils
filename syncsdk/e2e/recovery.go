package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// 恢复码布局: 18 字节随机数 (144 位熵) + 2 字节校验, 共 20 字节,
// 渲染为 32 个 Crockford base32 符号, 8 组 4 个: "XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX"。
// 校验 = SHA-256("czl-sync-recovery-v1" || 0x00 || 随机数) 的前 2 字节。
const (
	recoveryEntropySize  = 18
	recoveryChecksumSize = 2
	recoveryDomain       = "czl-sync-recovery-v1"
)

// RecoveryCode 是开通时只展示一次的恢复码。配合服务端保存的私钥副本, 可在一无所有的设备上
// 恢复用户私钥。恢复码从不发给服务端。
type RecoveryCode struct {
	entropy [recoveryEntropySize]byte
}

// NewRecoveryCode 随机生成恢复码。
func NewRecoveryCode() (*RecoveryCode, error) {
	c := &RecoveryCode{}
	if _, err := rand.Read(c.entropy[:]); err != nil {
		return nil, err
	}
	return c, nil
}

// ParseRecoveryCode 解析用户输入的恢复码: 忽略大小写、空格与连字符, I/L/O 读作 1/1/0。
// 校验位不对 (几乎总是输错) 直接返回 ErrInvalidRecoveryCode, 不做任何解密尝试。
func ParseRecoveryCode(s string) (*RecoveryCode, error) {
	raw, ok := decodeCrockford(normalizeCrockford(s), recoveryEntropySize+recoveryChecksumSize)
	if !ok {
		return nil, ErrInvalidRecoveryCode
	}
	defer Zero(raw)
	c := &RecoveryCode{}
	copy(c.entropy[:], raw[:recoveryEntropySize])
	if !equal(c.checksum(), raw[recoveryEntropySize:]) {
		c.Destroy()
		return nil, ErrInvalidRecoveryCode
	}
	return c, nil
}

func (c *RecoveryCode) checksum() []byte {
	h := sha256.New()
	h.Write([]byte(recoveryDomain))
	h.Write([]byte{0})
	h.Write(c.entropy[:])
	return h.Sum(nil)[:recoveryChecksumSize]
}

// String 返回展示用的恢复码, 如 "0K3M-9QX2-..."。只展示一次, 不要写日志。
func (c *RecoveryCode) String() string {
	raw := append(c.entropy[:len(c.entropy):len(c.entropy)], c.checksum()...)
	defer Zero(raw)
	return group(encodeCrockford(raw), 4, "-")
}

// Destroy 清零恢复码。
func (c *RecoveryCode) Destroy() { Zero(c.entropy[:]) }

// KDFParams 是派生恢复密钥的 Argon2id 参数, 随私钥副本保存, 以后调高参数不影响旧副本。
type KDFParams struct {
	// Alg 固定为 "argon2id"。
	Alg string `json:"alg"`
	// Time 是迭代次数。
	Time uint32 `json:"t"`
	// MemoryKiB 是内存开销 (KiB)。
	MemoryKiB uint32 `json:"m"`
	// Threads 是并行度。
	Threads uint8 `json:"p"`
	// Salt 是每份副本独立的 16 字节随机盐。
	Salt Bytes `json:"salt"`
}

// DefaultKDFParams 返回新副本的参数: Argon2id t=3、m=64 MiB、p=4 (RFC 9106 第二推荐档的内存级别),
// 加新的 16 字节盐。恢复码本身已有 144 位熵, KDF 是纵深防御而非主要屏障。
func DefaultKDFParams() (KDFParams, error) {
	p := KDFParams{Alg: "argon2id", Time: 3, MemoryKiB: 64 * 1024, Threads: 4, Salt: make([]byte, 16)}
	_, err := rand.Read(p.Salt)
	return p, err
}

// 打开副本时接受的参数上限。参数来自服务端, 设上限防止恶意服务端让客户端分配无限内存。
const (
	maxKDFTime      = 16
	maxKDFMemoryKiB = 1024 * 1024
	maxKDFThreads   = 16
)

func (p KDFParams) validate() error {
	if p.Alg != "argon2id" || p.Time == 0 || p.Time > maxKDFTime ||
		p.MemoryKiB < 8*uint32(max(p.Threads, 1)) || p.MemoryKiB > maxKDFMemoryKiB ||
		p.Threads == 0 || p.Threads > maxKDFThreads || len(p.Salt) < 16 || len(p.Salt) > 64 {
		return fmt.Errorf("%w: kdf parameters", ErrMalformed)
	}
	return nil
}

// derive 返回 32 字节恢复密钥, 调用方须清零。
func (p KDFParams) derive(c *RecoveryCode) []byte {
	return argon2.IDKey(c.entropy[:], p.Salt, p.Time, p.MemoryKiB, p.Threads, KeySize)
}

// EscrowVersion 是当前私钥副本格式版本。
const EscrowVersion = 1

// Escrow 是用恢复密钥加密的用户私钥副本, 服务端原样保存并下发给新设备。JSON 线上形式:
//
//	{"v":1,
//	 "kdf":{"alg":"argon2id","t":3,"m":65536,"p":4,"salt":"<b64url 16B>"},
//	 "nonce":"<b64url 12B>",
//	 "ct":"<b64url AES-256-GCM(recoveryKey, nonce, privateKey, aad)>"}
//
// aad = "czl-sync-escrow-v1" || LP(uint8 v) || LP(用户公钥), LP 为大端 uint32 长度前缀。
// 绑定公钥后, 副本不能被配到别人的公钥上。
type Escrow struct {
	V     int       `json:"v"`
	KDF   KDFParams `json:"kdf"`
	Nonce Bytes     `json:"nonce"`
	CT    Bytes     `json:"ct"`
}

func escrowAAD(v int, pub PublicKey) []byte {
	return appendLP([]byte("czl-sync-escrow-v1"), []byte{byte(v)}, pub[:])
}

// SealEscrow 用恢复码派生的密钥加密用户私钥。重置恢复码即用新码封一份新副本替换服务端旧副本。
func SealEscrow(user *UserKey, code *RecoveryCode) (*Escrow, error) {
	params, err := DefaultKDFParams()
	if err != nil {
		return nil, err
	}
	key := params.derive(code)
	defer Zero(key)
	priv := user.Bytes()
	defer Zero(priv)
	nonce, ct, err := sealGCM(key, priv, escrowAAD(EscrowVersion, user.Public()))
	if err != nil {
		return nil, err
	}
	return &Escrow{V: EscrowVersion, KDF: params, Nonce: nonce, CT: ct}, nil
}

// OpenEscrow 用恢复码解开副本, 并校验恢复出的私钥与用户已发布的公钥 expected 一致。
func OpenEscrow(e *Escrow, code *RecoveryCode, expected PublicKey) (*UserKey, error) {
	if e == nil || e.V != EscrowVersion || len(e.Nonce) != 12 {
		return nil, fmt.Errorf("%w: escrow", ErrMalformed)
	}
	if err := e.KDF.validate(); err != nil {
		return nil, err
	}
	key := e.KDF.derive(code)
	defer Zero(key)
	priv, err := openGCM(key, e.Nonce, e.CT, escrowAAD(e.V, expected))
	if err != nil {
		return nil, err
	}
	defer Zero(priv)
	user, err := UserKeyFromBytes(priv)
	if err != nil {
		return nil, err
	}
	if !user.Public().Equal(expected) {
		return nil, ErrKeyMismatch
	}
	return user, nil
}

// sealGCM 用 AES-256-GCM 与新的 96 位随机 nonce 加密。
func sealGCM(key, plaintext, aad []byte) (nonce, ct []byte, err error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, aead.Seal(nil, nonce, plaintext, aad), nil
}

func openGCM(key, nonce, ct, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: nonce", ErrMalformed)
	}
	pt, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
