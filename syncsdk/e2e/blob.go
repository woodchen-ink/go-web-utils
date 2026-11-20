package e2e

import (
	"bufio"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
)

// 加密附件流格式:
//
//	header = "CZLB" || uint8 format(1) || uint32 kv || uint32 chunkSize   (13 bytes)
//	chunk  = nonce[12] || AES-256-GCM(spaceKey[kv], nonce, plaintextChunk, chunkAAD)
//	chunkAAD = "czl-sync-blob-v1" || LP(space) || LP(blobId) || header ||
//	           uint64 chunkIndex || uint8 final
//
// 除最后一块外每块恰好 chunkSize 字节明文; 最后一块 0..chunkSize 字节且 final=1。
// 至少一块 (空附件是一个空的最后块)。AAD 里的序号与 final 标记检出截断、乱序与追加,
// AAD 里的 blobId 把流绑定到其标识。读完最后一块后还校验 HMAC-SHA256(附件标识密钥, 明文) == blobId,
// 持有空间密钥的成员也无法把内容挂到别的标识下。
const (
	blobMagic         = "CZLB"
	blobFormat        = 1
	blobHeaderSize    = 13
	blobNonceSize     = 12
	blobTagSize       = 16
	blobDomain        = "czl-sync-blob-v1"
	minBlobChunkSize  = 1 << 10
	maxBlobChunkSize  = 16 << 20
	blobChunkOverhead = blobNonceSize + blobTagSize
)

// DefaultChunkSize 是新附件流的明文分块大小 (64 KiB)。
const DefaultChunkSize = 64 << 10

// ErrBlobMismatch 表示解密后的附件内容与其标识不符。
var ErrBlobMismatch = errors.New("e2e: blob content does not match its id")

// BlobIDHash 返回带密钥的哈希, 写入内容后其十六进制摘要即附件标识。
func (k *Keyring) BlobIDHash() (hash.Hash, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.blobKey == nil {
		return nil, ErrUnknownKeyVersion
	}
	return hmac.New(sha256.New, k.blobKey), nil
}

// BlobID 返回 r 中内容的附件标识与字节数: 小写十六进制 HMAC-SHA256(附件标识密钥, 内容), 64 个字符。
// 同空间内相同内容标识相同, 上传可去重; 服务端始终看不到内容的明文摘要。
func (k *Keyring) BlobID(r io.Reader) (string, int64, error) {
	h, err := k.BlobIDHash()
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// EncryptedBlobSize 返回 plainSize 字节内容按 chunkSize 分块加密后的流大小。
func EncryptedBlobSize(plainSize int64, chunkSize int) int64 {
	chunks := plainSize / int64(chunkSize)
	if plainSize%int64(chunkSize) != 0 || plainSize == 0 {
		chunks++
	}
	return blobHeaderSize + plainSize + chunks*blobChunkOverhead
}

func blobChunkAAD(space, blobID string, header []byte, index uint64, final bool) []byte {
	aad := appendLP([]byte(blobDomain), []byte(space), []byte(blobID))
	aad = append(aad, header...)
	aad = binary.BigEndian.AppendUint64(aad, index)
	if final {
		return append(aad, 1)
	}
	return append(aad, 0)
}

// NewBlobEncrypter 返回用当前空间密钥加密 src 的流, 每块 chunkSize 字节 (0 取 DefaultChunkSize)。
// 无论文件多大, 内存占用不超过两块。
func (k *Keyring) NewBlobEncrypter(blobID string, src io.Reader, chunkSize int) (io.Reader, error) {
	if chunkSize == 0 {
		chunkSize = DefaultChunkSize
	}
	if chunkSize < minBlobChunkSize || chunkSize > maxBlobChunkSize {
		return nil, fmt.Errorf("e2e: chunk size %d out of range", chunkSize)
	}
	kv, key, ok := k.currentKey()
	if !ok {
		return nil, ErrUnknownKeyVersion
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 0, blobHeaderSize)
	header = append(header, blobMagic...)
	header = append(header, blobFormat)
	header = binary.BigEndian.AppendUint32(header, uint32(kv))
	header = binary.BigEndian.AppendUint32(header, uint32(chunkSize))
	return &blobEncrypter{
		aead: aead, space: k.space, blobID: blobID, header: header,
		src: bufio.NewReaderSize(src, chunkSize+1), plain: make([]byte, chunkSize),
		out: append([]byte(nil), header...),
	}, nil
}

type blobEncrypter struct {
	aead          cipher.AEAD
	space, blobID string
	header        []byte
	src           *bufio.Reader
	plain, out    []byte
	index         uint64
	done          bool
	err           error
}

func (e *blobEncrypter) Read(p []byte) (int, error) {
	for len(e.out) == 0 {
		if e.err != nil {
			return 0, e.err
		}
		if e.done {
			return 0, io.EOF
		}
		e.next()
	}
	n := copy(p, e.out)
	e.out = e.out[n:]
	return n, nil
}

func (e *blobEncrypter) next() {
	n, err := io.ReadFull(e.src, e.plain)
	final := false
	switch {
	case err == io.EOF || err == io.ErrUnexpectedEOF:
		final = true
	case err != nil:
		e.err = err
		return
	default:
		if _, perr := e.src.Peek(1); perr == io.EOF {
			final = true
		} else if perr != nil {
			e.err = perr
			return
		}
	}
	nonce := make([]byte, blobNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		e.err = err
		return
	}
	aad := blobChunkAAD(e.space, e.blobID, e.header, e.index, final)
	e.out = e.aead.Seal(nonce, nonce, e.plain[:n], aad)
	Zero(e.plain[:n])
	e.index++
	e.done = final
}

// NewBlobDecrypter 返回解密附件流的明文读取器。每块认证通过才返回; 被截断、乱序、换密钥或挪到
// 别的标识的流返回 ErrDecrypt, 内容与标识不符在末尾返回 ErrBlobMismatch。
// 任何错误 (包括已读出部分数据后的错误) 都应整体丢弃: 先写临时文件, 成功后再改名。
func (k *Keyring) NewBlobDecrypter(blobID string, src io.Reader) (io.Reader, error) {
	header := make([]byte, blobHeaderSize)
	if _, err := io.ReadFull(src, header); err != nil {
		return nil, fmt.Errorf("%w: blob header", ErrMalformed)
	}
	if string(header[:4]) != blobMagic || header[4] != blobFormat {
		return nil, fmt.Errorf("%w: blob header", ErrMalformed)
	}
	kv := int(binary.BigEndian.Uint32(header[5:]))
	chunkSize := int(binary.BigEndian.Uint32(header[9:]))
	if chunkSize < minBlobChunkSize || chunkSize > maxBlobChunkSize {
		return nil, fmt.Errorf("%w: blob chunk size", ErrMalformed)
	}
	key, ok := k.key(kv)
	if !ok {
		return nil, ErrUnknownKeyVersion
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	mac, err := k.BlobIDHash()
	if err != nil {
		return nil, err
	}
	want, err := hex.DecodeString(blobID)
	if err != nil || len(want) != sha256.Size {
		return nil, fmt.Errorf("%w: blob id", ErrMalformed)
	}
	return &blobDecrypter{
		aead: aead, space: k.space, blobID: blobID, header: header, mac: mac, want: want,
		src: bufio.NewReaderSize(src, chunkSize+blobChunkOverhead+1),
		buf: make([]byte, chunkSize+blobChunkOverhead),
	}, nil
}

type blobDecrypter struct {
	aead          cipher.AEAD
	space, blobID string
	header        []byte
	mac           hash.Hash
	want          []byte
	src           *bufio.Reader
	buf, out      []byte
	index         uint64
	done          bool
	err           error
}

func (d *blobDecrypter) Read(p []byte) (int, error) {
	for len(d.out) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.done {
			return 0, io.EOF
		}
		d.next()
	}
	n := copy(p, d.out)
	d.out = d.out[n:]
	return n, nil
}

func (d *blobDecrypter) next() {
	n, err := io.ReadFull(d.src, d.buf)
	final := false
	switch {
	case err == io.EOF || err == io.ErrUnexpectedEOF:
		final = true
	case err != nil:
		d.err = err
		return
	default:
		if _, perr := d.src.Peek(1); perr == io.EOF {
			final = true
		} else if perr != nil {
			d.err = perr
			return
		}
	}
	if n < blobChunkOverhead {
		d.err = ErrDecrypt
		return
	}
	chunk := d.buf[:n]
	aad := blobChunkAAD(d.space, d.blobID, d.header, d.index, final)
	pt, err := d.aead.Open(nil, chunk[:blobNonceSize], chunk[blobNonceSize:], aad)
	if err != nil {
		d.err = ErrDecrypt
		return
	}
	d.mac.Write(pt)
	d.index++
	d.out = pt
	if final {
		d.done = true
		if !hmac.Equal(d.mac.Sum(nil), d.want) {
			d.out = nil
			d.err = ErrBlobMismatch
		}
	}
}
