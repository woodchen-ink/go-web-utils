package e2e

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// EnvelopeVersion 是当前文档信封格式版本。
const EnvelopeVersion = 1

// Envelope 是加密后的文档 payload, JSON 线上形式 (即文档 "payload" 字段的值):
//
//	{"v":1,"kv":3,"nonce":"<b64url 12B>","ct":"<b64url ciphertext||tag>"}
//
// ct = AES-256-GCM(spaceKey[kv], nonce, plaintext, DocAAD(space, collection, id, v, kv)).
// 每次加密使用新的随机 nonce。
type Envelope struct {
	V     int   `json:"v"`
	KV    int   `json:"kv"`
	Nonce Bytes `json:"nonce"`
	CT    Bytes `json:"ct"`
}

// DocAAD 返回把文档密文绑定到其位置与头部的关联数据:
//
//	"czl-sync-doc-v1" || LP(space) || LP(collection) || LP(id) || uint32 v || uint32 kv
//
// LP 为大端 uint32 长度前缀。把密文挪到别的文档、集合或空间, 或改动头部, 解密都会失败。
func DocAAD(space, collection, id string, v, kv int) []byte {
	aad := appendLP([]byte("czl-sync-doc-v1"), []byte(space), []byte(collection), []byte(id))
	aad = binary.BigEndian.AppendUint32(aad, uint32(v))
	return binary.BigEndian.AppendUint32(aad, uint32(kv))
}

// ParseEnvelope 严格解析信封: 未知字段、版本不对、密钥版本非正数、nonce 长度不对都拒绝。
func ParseEnvelope(raw []byte) (*Envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var e Envelope
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("%w: envelope", ErrMalformed)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}

// Validate 不解密、只校验信封头部; e2e required 的服务端做的就是这项校验。
func (e *Envelope) Validate() error {
	if e.V != EnvelopeVersion || e.KV <= 0 || len(e.Nonce) != 12 || len(e.CT) < 16 {
		return fmt.Errorf("%w: envelope", ErrMalformed)
	}
	return nil
}

// Equal 判断两个信封是否完全相同。
func (e *Envelope) Equal(o *Envelope) bool {
	return e != nil && o != nil && e.V == o.V && e.KV == o.KV &&
		bytes.Equal(e.Nonce, o.Nonce) && bytes.Equal(e.CT, o.CT)
}

// Seal 用当前空间密钥加密文档 payload。
func (k *Keyring) Seal(collection, id string, plaintext []byte) (*Envelope, error) {
	kv, key, ok := k.currentKey()
	if !ok {
		return nil, ErrUnknownKeyVersion
	}
	nonce, ct, err := sealGCM(key, plaintext, DocAAD(k.space, collection, id, EnvelopeVersion, kv))
	if err != nil {
		return nil, err
	}
	return &Envelope{V: EnvelopeVersion, KV: kv, Nonce: nonce, CT: ct}, nil
}

// Open 解密文档 payload: 缺少 e.KV 版本返回 ErrUnknownKeyVersion, 认证失败返回 ErrDecrypt。
func (k *Keyring) Open(collection, id string, e *Envelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	key, ok := k.key(e.KV)
	if !ok {
		return nil, ErrUnknownKeyVersion
	}
	return openGCM(key, e.Nonce, e.CT, DocAAD(k.space, collection, id, e.V, e.KV))
}
