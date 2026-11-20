package e2e

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
)

// b64 是线上唯一的二进制编码: 无填充 base64url (RFC 4648 §5)。
var b64 = base64.RawURLEncoding

// Bytes 是 JSON 序列化为无填充 base64url 字符串的字节切片, 本包线上格式的二进制字段都用它。
type Bytes []byte

// MarshalJSON 编码为无填充 base64url 字符串。
func (b Bytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(b64.EncodeToString(b))
}

// UnmarshalJSON 解码无填充 base64url 字符串; 带填充或标准字母表的写法被拒绝, 保证每个值只有一种编码。
func (b *Bytes) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := b64.DecodeString(s)
	if err != nil {
		return errors.New("e2e: invalid base64url value")
	}
	*b = v
	return nil
}

// EncodeBase64 编码为无填充 base64url (与服务端交换的二进制值都用这种编码)。
func EncodeBase64(b []byte) string { return b64.EncodeToString(b) }

// DecodeBase64 解码无填充 base64url。
func DecodeBase64(s string) ([]byte, error) { return b64.DecodeString(s) }

// appendLP 逐段追加「大端 uint32 长度 + 内容」(下文记作 LP)。长度前缀让拼接无歧义:
// ("ab","c") 与 ("a","bc") 编码不同。
func appendLP(dst []byte, parts ...[]byte) []byte {
	for _, p := range parts {
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(p)))
		dst = append(dst, p...)
	}
	return dst
}

// readLP 读出 appendLP 写入的一段。
func readLP(src []byte) (field, rest []byte, ok bool) {
	if len(src) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(src)
	if uint64(len(src)-4) < uint64(n) {
		return nil, nil, false
	}
	return src[4 : 4+n], src[4+n:], true
}

// Zero 把 b 清零。Go 的 GC 可能已把数据复制到别处, 所以这只能减少而不能杜绝残留。
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// equal 恒定时间比较两个字节切片。
func equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// crockford 是 Crockford base32 字母表: 数字与去掉 I、L、O、U 的大写字母, 念出来或手抄都不易错。
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// encodeCrockford 把 b 编码为无填充 Crockford base32。
func encodeCrockford(b []byte) string {
	var sb strings.Builder
	var acc uint64
	bits := 0
	for _, c := range b {
		acc = acc<<8 | uint64(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(crockford[(acc>>uint(bits))&31])
		}
	}
	if bits > 0 {
		sb.WriteByte(crockford[(acc<<uint(5-bits))&31])
	}
	return sb.String()
}

// decodeCrockford 把已规整 (大写、无分隔符) 且恰为 ceil(n*8/5) 个符号的输入解成 n 字节。
func decodeCrockford(s string, n int) ([]byte, bool) {
	if len(s) != (n*8+4)/5 {
		return nil, false
	}
	out := make([]byte, 0, n)
	var acc uint64
	bits := 0
	for i := 0; i < len(s); i++ {
		v := strings.IndexByte(crockford, s[i])
		if v < 0 {
			return nil, false
		}
		acc = acc<<5 | uint64(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>uint(bits)))
		}
	}
	return out, len(out) == n
}

// normalizeCrockford 转大写、去掉空白与连字符, 并把易混的 I/L 读作 1、O 读作 0。
func normalizeCrockford(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-', '\t', '\n', '\r':
			continue
		case 'I', 'L':
			sb.WriteByte('1')
		case 'O':
			sb.WriteByte('0')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// group 把 s 按 n 个字符分组, 用 sep 连接。
func group(s string, n int, sep string) string {
	var parts []string
	for len(s) > n {
		parts = append(parts, s[:n])
		s = s[n:]
	}
	parts = append(parts, s)
	return strings.Join(parts, sep)
}
