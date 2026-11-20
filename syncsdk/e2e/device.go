package e2e

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// 新设备授权协议。新设备 N 没有私钥, 同一用户的已有设备 A 有。全部消息经服务端转交,
// 服务端不能在不被察觉的情况下替换密钥。
//
//  1. N 生成临时 X25519 密钥对与 32 字节 nonce Nn, 公布 tempPub 与
//     commitment = SHA-256("czl-sync-device-commit-v1" || LP(tempPub) || LP(Nn)), Nn 暂不公开
//  2. A 取到请求后公布 32 字节随机 nonce Na
//  3. N 看到 Na 后揭示 Nn, A 用承诺校验
//  4. 双方计算 SAS(requestId, tempPub, userPub, Nn, Na) 显示 6 位码, 用户在 A 上确认一致
//  5. A 把用户私钥封装给 tempPub (sealed box, 绑定请求 id), 服务端转交; N 解开并校验与 userPub 一致
//
// 承诺迫使 Nn 在 Na 公布前固定、Na 在 Nn 揭示前固定。替换 tempPub 的中间人只有一次 10^-6 的
// 碰运气机会, 而不能离线反复生成密钥直到两端码碰撞 (只用双方公钥算码时这只需约 10^6 次尝试)。
const (
	deviceCommitDomain = "czl-sync-device-commit-v1"
	deviceSASDomain    = "czl-sync-device-sas-v1"
	deviceSealMagic    = "CZLDK1"
	// DeviceNonceSize 是协议两个 nonce 的字节数。
	DeviceNonceSize = 32
)

// DeviceRequester 是新设备一侧: 临时密钥对与已承诺的 nonce。
type DeviceRequester struct {
	temp  *ecdh.PrivateKey
	pub   PublicKey
	nonce [DeviceNonceSize]byte
}

// NewDeviceRequester 为新的授权请求生成临时密钥对与 nonce。
func NewDeviceRequester() (*DeviceRequester, error) {
	temp, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	d := &DeviceRequester{temp: temp}
	copy(d.pub[:], temp.PublicKey().Bytes())
	if _, err := rand.Read(d.nonce[:]); err != nil {
		return nil, err
	}
	return d, nil
}

// TempPublic 返回要公布的临时公钥。
func (d *DeviceRequester) TempPublic() PublicKey { return d.pub }

// Commitment 返回随请求公布的承诺。
func (d *DeviceRequester) Commitment() []byte { return DeviceCommitment(d.pub, d.nonce[:]) }

// Nonce 返回已承诺的 nonce。只能在收到批准方 nonce 之后揭示, 每个请求只揭示一次。
func (d *DeviceRequester) Nonce() []byte { return append([]byte(nil), d.nonce[:]...) }

// Code 在得知批准方 nonce 后返回 6 位校验码。
func (d *DeviceRequester) Code(requestID string, userPub PublicKey, approverNonce []byte) (string, error) {
	return DeviceSAS(requestID, d.pub, userPub, d.nonce[:], approverNonce)
}

// OpenUserKey 解开为 requestID 转交来的用户私钥, 并校验与用户已发布的公钥 userPub 一致。
func (d *DeviceRequester) OpenUserKey(requestID string, sealed []byte, userPub PublicKey) (*UserKey, error) {
	var priv [KeySize]byte
	b := d.temp.Bytes()
	copy(priv[:], b)
	Zero(b)
	defer Zero(priv[:])
	pub := [KeySize]byte(d.pub)
	pt, ok := box.OpenAnonymous(nil, sealed, &pub, &priv)
	if !ok {
		return nil, ErrDecrypt
	}
	defer Zero(pt)
	if len(pt) < len(deviceSealMagic) || string(pt[:len(deviceSealMagic)]) != deviceSealMagic {
		return nil, fmt.Errorf("%w: device payload", ErrMalformed)
	}
	id, rest, ok := readLP(pt[len(deviceSealMagic):])
	if !ok || len(rest) != KeySize {
		return nil, fmt.Errorf("%w: device payload", ErrMalformed)
	}
	if string(id) != requestID {
		return nil, ErrKeyMismatch
	}
	user, err := UserKeyFromBytes(rest)
	if err != nil {
		return nil, err
	}
	if !user.Public().Equal(userPub) {
		return nil, ErrKeyMismatch
	}
	return user, nil
}

// Destroy 清零 nonce。ecdh 临时私钥无法原地清除, 用完丢弃对 requester 的引用即可。
func (d *DeviceRequester) Destroy() { Zero(d.nonce[:]) }

// DeviceCommitment 计算对 (tempPub, nonce) 的承诺。
func DeviceCommitment(tempPub PublicKey, nonce []byte) []byte {
	sum := sha256.Sum256(appendLP([]byte(deviceCommitDomain), tempPub[:], nonce))
	return sum[:]
}

// VerifyDeviceCommitment 恒定时间校验揭示的 nonce 与承诺是否相符。
func VerifyDeviceCommitment(tempPub PublicKey, commitment, nonce []byte) bool {
	return len(nonce) == DeviceNonceSize && equal(DeviceCommitment(tempPub, nonce), commitment)
}

// NewDeviceNonce 返回随机协议 nonce (批准方的 Na)。
func NewDeviceNonce() ([]byte, error) {
	n := make([]byte, DeviceNonceSize)
	_, err := rand.Read(n)
	return n, err
}

// DeviceSAS 计算两端显示的 6 位短认证码:
//
//	h = SHA-256("czl-sync-device-sas-v1" || LP(requestId) || LP(tempPub) ||
//	            LP(userPub) || LP(Nn) || LP(Na))
//	code = big-endian uint64(h[0:8]) mod 1_000_000, zero-padded to 6 digits
func DeviceSAS(requestID string, tempPub, userPub PublicKey, deviceNonce, approverNonce []byte) (string, error) {
	if len(deviceNonce) != DeviceNonceSize || len(approverNonce) != DeviceNonceSize {
		return "", fmt.Errorf("%w: device nonce", ErrMalformed)
	}
	h := sha256.Sum256(appendLP([]byte(deviceSASDomain), []byte(requestID), tempPub[:], userPub[:], deviceNonce, approverNonce))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint64(h[:8])%1_000_000), nil
}

// SealUserKeyForDevice 把用户私钥封装给新设备临时公钥, 并绑定 requestID:
//
//	sealedbox(tempPub, "CZLDK1" || LP(requestId) || privateKey[32])
//
// 只能在用户确认两端校验码一致后调用。
func SealUserKeyForDevice(requestID string, user *UserKey, tempPub PublicKey) ([]byte, error) {
	priv := user.Bytes()
	defer Zero(priv)
	pt := appendLP([]byte(deviceSealMagic), []byte(requestID))
	pt = append(pt, priv...)
	defer Zero(pt)
	pub := [KeySize]byte(tempPub)
	return box.SealAnonymous(nil, pt, &pub, rand.Reader)
}
