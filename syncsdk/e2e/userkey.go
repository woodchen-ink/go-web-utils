package e2e

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// KeySize 是本包所有对称密钥与 X25519 密钥的字节数。
const KeySize = 32

// 本包返回的错误, 用 errors.Is 判断。
var (
	// ErrDecrypt 表示认证解密失败: 密钥不对、关联数据不对 (密文被挪到别的文档 / 空间 / 附件)
	// 或密文被篡改。几种情形刻意不加区分。
	ErrDecrypt = errors.New("e2e: decryption failed")
	// ErrUnknownKeyVersion 表示密文所用的空间密钥版本不在 keyring 里。
	ErrUnknownKeyVersion = errors.New("e2e: unknown space key version")
	// ErrMalformed 表示信封、私钥副本、封装密钥或附件流不符合线上格式。
	ErrMalformed = errors.New("e2e: malformed input")
	// ErrKeyMismatch 表示解出的密钥材料与预期归属不符 (公钥、空间、版本或请求不对)。
	ErrKeyMismatch = errors.New("e2e: key material does not match")
	// ErrInvalidRecoveryCode 表示恢复码无法解析或校验位不对 (通常是输错)。
	ErrInvalidRecoveryCode = errors.New("e2e: invalid recovery code")
)

// PublicKey 是 X25519 公钥。
type PublicKey [KeySize]byte

// ParsePublicKey 从 base64url 线上形式解析公钥, 并校验是合法的 X25519 点编码。
func ParsePublicKey(s string) (PublicKey, error) {
	var pk PublicKey
	b, err := b64.DecodeString(s)
	if err != nil || len(b) != KeySize {
		return pk, fmt.Errorf("%w: public key", ErrMalformed)
	}
	if _, err := ecdh.X25519().NewPublicKey(b); err != nil {
		return pk, fmt.Errorf("%w: public key", ErrMalformed)
	}
	copy(pk[:], b)
	return pk, nil
}

// String 返回 base64url 线上形式。
func (p PublicKey) String() string { return b64.EncodeToString(p[:]) }

// IsZero 判断是否为零值。
func (p PublicKey) IsZero() bool { return p == PublicKey{} }

// Equal 恒定时间比较两个公钥。
func (p PublicKey) Equal(q PublicKey) bool { return equal(p[:], q[:]) }

// fingerprintDomain 把指纹哈希与协议里其他哈希隔开。
const fingerprintDomain = "czl-sync-fingerprint-v1"

// Fingerprint 返回便于人工核对的公钥指纹: SHA-256(domain || 0x00 || key) 的前 160 位,
// Crockford base32 编码, 8 组 4 个符号以空格分隔, 如 "8M0F 2YZC ..."。
// 双方在带外核对指纹, 确认成员公钥没有被服务端替换。
func (p PublicKey) Fingerprint() string {
	h := sha256.New()
	h.Write([]byte(fingerprintDomain))
	h.Write([]byte{0})
	h.Write(p[:])
	return group(encodeCrockford(h.Sum(nil)[:20]), 4, " ")
}

// UserKey 是用户长期 X25519 密钥对。私钥只以两种形式离开用户设备:
// 用恢复密钥加密的副本 (Escrow), 以及封装给新批准设备的 sealed box。
type UserKey struct {
	priv *ecdh.PrivateKey
	pub  PublicKey
}

// GenerateUserKey 随机生成用户密钥对。
func GenerateUserKey() (*UserKey, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return newUserKey(priv), nil
}

// UserKeyFromBytes 从 Bytes 返回的 32 字节私钥恢复密钥对, 不保留入参。
func UserKeyFromBytes(b []byte) (*UserKey, error) {
	priv, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("%w: private key", ErrMalformed)
	}
	return newUserKey(priv), nil
}

func newUserKey(priv *ecdh.PrivateKey) *UserKey {
	k := &UserKey{priv: priv}
	copy(k.pub[:], priv.PublicKey().Bytes())
	return k
}

// Public 返回公钥。
func (k *UserKey) Public() PublicKey { return k.pub }

// Bytes 返回 32 字节私钥的新副本, 用于存入系统密钥库; 用完应 Zero。
func (k *UserKey) Bytes() []byte { return k.priv.Bytes() }

// secret 以数组形式返回私钥供 nacl/box 使用, 调用方须清零。
func (k *UserKey) secret() *[KeySize]byte {
	var s [KeySize]byte
	b := k.priv.Bytes()
	copy(s[:], b)
	Zero(b)
	return &s
}
