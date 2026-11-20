package syncsdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/woodchen-ink/go-web-utils/syncsdk/e2e"
)

// 附件: 端到端加密时 blobId = HMAC-SHA256(附件标识密钥, 明文) 的十六进制, 内容为 e2e 分块密文;
// 未加密时 blobId = 明文 SHA-256 十六进制, 内容为明文, 由服务端校验摘要。
// 附件引用由应用写进文档 payload (服务端不解析)。

// UploadBlob 上传附件并返回 blobId。先在本地算出 blobId, HEAD 已存在则跳过上传 (同空间去重)。
// open 每次调用都要返回内容的开头 (计算 blobId 一次、每次上传尝试各一次), 大文件不整读进内存。
// contentType 只在未加密时发给服务端; 加密时一律 application/octet-stream, 不泄露类型。
func (e *Engine) UploadBlob(ctx context.Context, space string, open func() (io.ReadCloser, error), contentType string) (string, error) {
	ring, err := e.ring(ctx, space)
	if err != nil {
		return "", err
	}
	id, size, err := blobID(ring, open)
	if err != nil {
		return "", err
	}
	exists, err := e.opts.Client.HasBlob(ctx, space, id)
	if err != nil {
		return "", err
	}
	if exists {
		return id, nil
	}
	if ring == nil {
		return id, e.opts.Client.PutBlob(ctx, space, id, size, contentType, open)
	}
	encOpen := func() (io.ReadCloser, error) {
		src, err := open()
		if err != nil {
			return nil, err
		}
		r, err := ring.NewBlobEncrypter(id, src, 0)
		if err != nil {
			src.Close()
			return nil, err
		}
		return readCloser{r, src}, nil
	}
	return id, e.opts.Client.PutBlob(ctx, space, id, e2e.EncryptedBlobSize(size, e2e.DefaultChunkSize), "", encOpen)
}

type readCloser struct {
	io.Reader
	io.Closer
}

func blobID(ring *e2e.Keyring, open func() (io.ReadCloser, error)) (string, int64, error) {
	src, err := open()
	if err != nil {
		return "", 0, err
	}
	defer src.Close()
	if ring != nil {
		return ring.BlobID(src)
	}
	h := sha256.New()
	n, err := io.Copy(h, src)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// UploadBlobBytes 上传内存中的附件。
func (e *Engine) UploadBlobBytes(ctx context.Context, space string, data []byte, contentType string) (string, error) {
	return e.UploadBlob(ctx, space, func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }, contentType)
}

// UploadBlobFile 上传本地文件。
func (e *Engine) UploadBlobFile(ctx context.Context, space, path, contentType string) (string, error) {
	return e.UploadBlob(ctx, space, func() (io.ReadCloser, error) { return os.Open(path) }, contentType)
}

// BlobPath 返回附件在下载缓存里的明文文件路径, 本地没有时下载、解密并校验后写入缓存。
// 缓存布局为 <BlobCacheDir>/<空间摘要>/<blobId>; 写入先落临时文件, 校验通过才改名,
// 中途失败不会留下半截文件。缓存是明文, 目录应在应用自己的数据目录内。
func (e *Engine) BlobPath(ctx context.Context, space, blobID string) (string, error) {
	if e.opts.BlobCacheDir == "" {
		return "", errors.New("syncsdk: EngineOptions.BlobCacheDir is not set")
	}
	if b, err := hex.DecodeString(blobID); err != nil || len(b) != sha256.Size {
		return "", fmt.Errorf("syncsdk: invalid blob id %q", blobID)
	}
	sum := sha256.Sum256([]byte(space))
	dir := filepath.Join(e.opts.BlobCacheDir, hex.EncodeToString(sum[:8]))
	final := filepath.Join(dir, blobID)
	if _, err := os.Stat(final); err == nil {
		return final, nil
	}
	ring, err := e.ring(ctx, space)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	body, err := e.opts.Client.GetBlob(ctx, space, blobID)
	if err != nil {
		return "", err
	}
	defer body.Close()
	tmp, err := os.CreateTemp(dir, blobID+".*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := copyBlob(tmp, body, ring, blobID); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		if _, serr := os.Stat(final); serr == nil {
			return final, nil // 并发下载, 另一方已写好
		}
		return "", err
	}
	return final, nil
}

func copyBlob(dst io.Writer, src io.Reader, ring *e2e.Keyring, blobID string) error {
	if ring != nil {
		r, err := ring.NewBlobDecrypter(blobID, src)
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, r)
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), src); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != blobID {
		return e2e.ErrBlobMismatch
	}
	return nil
}

// OpenBlob 打开附件明文 (必要时先下载进缓存), 调用方负责关闭。
func (e *Engine) OpenBlob(ctx context.Context, space, blobID string) (*os.File, error) {
	p, err := e.BlobPath(ctx, space, blobID)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}
