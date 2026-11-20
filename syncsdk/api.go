package syncsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"encoding/json"
)

// 本文件逐一对应 /api/sync/v1 的接口, 一个方法一个端点, 只做编解码。

// MaxChangesLimit 是 changes 接口单页上限。
const MaxChangesLimit = 500

// Spaces 列出本人在本应用可访问的空间 (GET /spaces)。
func (c *Client) Spaces(ctx context.Context) ([]Space, error) {
	var out struct {
		Spaces []Space `json:"spaces"`
	}
	err := c.doJSON(ctx, &request{method: http.MethodGet, path: []string{"spaces"}}, nil, &out)
	return out.Spaces, err
}

// Changes 拉取 rev > since 的文档 (GET /spaces/{space}/changes)。
// 游标早于墓碑保留期时返回 ErrCursorExpired (410)。limit ≤ 0 取上限 500。
func (c *Client) Changes(ctx context.Context, space string, since int64, limit int) (*ChangesPage, error) {
	if limit <= 0 || limit > MaxChangesLimit {
		limit = MaxChangesLimit
	}
	q := url.Values{"since": {strconv.FormatInt(since, 10)}, "limit": {strconv.Itoa(limit)}}
	var out ChangesPage
	err := c.doJSON(ctx, &request{method: http.MethodGet, path: []string{"spaces", space, "changes"}, query: q}, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// PutDoc 写入一份文档 (PUT /spaces/{space}/docs/{collection}/{id})。新建时 baseRev 为 0。
// baseRev 与服务端不符时返回 409 的 *APIError, 用 Current() 取服务端当前文档。
func (c *Client) PutDoc(ctx context.Context, space, collection, id string, baseRev int64, payload json.RawMessage) (*Doc, error) {
	var out Doc
	r := &request{method: http.MethodPut, path: []string{"spaces", space, "docs", collection, id}}
	if err := c.doJSON(ctx, r, PutDocRequest{BaseRev: baseRev, Payload: payload}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDoc 写墓碑 (DELETE /spaces/{space}/docs/{collection}/{id}?baseRev=), 冲突规则同 PutDoc。
func (c *Client) DeleteDoc(ctx context.Context, space, collection, id string, baseRev int64) (*Doc, error) {
	var out Doc
	r := &request{method: http.MethodDelete, path: []string{"spaces", space, "docs", collection, id},
		query: url.Values{"baseRev": {strconv.FormatInt(baseRev, 10)}}}
	if err := c.doJSON(ctx, r, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Batch 一次提交最多 100 个写操作 (POST /spaces/{space}/batch), 逐条返回结果, 不保证原子。
func (c *Client) Batch(ctx context.Context, space string, ops []BatchOp) ([]BatchResult, error) {
	if len(ops) == 0 || len(ops) > MaxBatchOps {
		return nil, fmt.Errorf("syncsdk: batch needs 1..%d ops, got %d", MaxBatchOps, len(ops))
	}
	var out BatchResponse
	r := &request{method: http.MethodPost, path: []string{"spaces", space, "batch"}}
	if err := c.doJSON(ctx, r, BatchRequest{Ops: ops}, &out); err != nil {
		return nil, err
	}
	if len(out.Results) != len(ops) {
		return nil, fmt.Errorf("%w: batch returned %d results for %d ops", ErrProtocol, len(out.Results), len(ops))
	}
	return out.Results, nil
}

// Revisions 列出开启了历史版本的集合里一份文档的历史 (GET .../revisions), 新的在前。
func (c *Client) Revisions(ctx context.Context, space, collection, id string) ([]Doc, error) {
	var out struct {
		Revisions []Doc `json:"revisions"`
	}
	r := &request{method: http.MethodGet, path: []string{"spaces", space, "docs", collection, id, "revisions"}}
	err := c.doJSON(ctx, r, nil, &out)
	return out.Revisions, err
}

// HasBlob 判断附件是否已存在 (HEAD /spaces/{space}/blobs/{blobId})。
func (c *Client) HasBlob(ctx context.Context, space, blobID string) (bool, error) {
	resp, err := c.send(ctx, &request{method: http.MethodHead, path: []string{"spaces", space, "blobs", blobID}})
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

// PutBlob 上传附件 (PUT /spaces/{space}/blobs/{blobId}), 上传幂等。
// open 在每次尝试时重新打开内容, 以便重试; size 为字节数, 未知传 -1。
func (c *Client) PutBlob(ctx context.Context, space, blobID string, size int64, contentType string, open func() (io.ReadCloser, error)) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	resp, err := c.send(ctx, &request{method: http.MethodPut, path: []string{"spaces", space, "blobs", blobID},
		open: open, size: size, ctype: contentType})
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	return nil
}

// GetBlob 下载附件 (GET /spaces/{space}/blobs/{blobId}, 跟随 302 到预签名地址)。
// 调用方负责关闭返回的 ReadCloser。
func (c *Client) GetBlob(ctx context.Context, space, blobID string) (io.ReadCloser, error) {
	resp, err := c.send(ctx, &request{method: http.MethodGet, path: []string{"spaces", space, "blobs", blobID}})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: blob download status %d", ErrProtocol, resp.StatusCode)
	}
	return resp.Body, nil
}

// MyKeys 读取本人公钥与私钥加密副本 (GET /keys/me); 未开通返回 ErrNotFound。
func (c *Client) MyKeys(ctx context.Context) (*UserKeys, error) {
	var out UserKeys
	if err := c.doJSON(ctx, &request{method: http.MethodGet, path: []string{"keys", "me"}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutMyKeys 提交本人公钥与私钥加密副本 (PUT /keys/me)。
// 首次 RecoveryVersion 为 1; 重置恢复码时公钥不变、版本加 1。并发开通或版本不符返回 409。
func (c *Client) PutMyKeys(ctx context.Context, keys UserKeys) (*UserKeys, error) {
	var out UserKeys
	if err := c.doJSON(ctx, &request{method: http.MethodPut, path: []string{"keys", "me"}}, keys, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceKeys 读取本人在该空间各版本的封装密钥、待授予成员与全部成员公钥 (GET /spaces/{space}/keys)。
func (c *Client) SpaceKeys(ctx context.Context, space string) (*SpaceKeys, error) {
	var out SpaceKeys
	if err := c.doJSON(ctx, &request{method: http.MethodGet, path: []string{"spaces", space, "keys"}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutSpaceKeys 提交为成员封装的密钥 (PUT /spaces/{space}/keys/{version}), 仅 owner / admin。
// rotate 为 true 时建立新版本 (另一台客户端抢先返回 409), false 时给已有版本补授。
func (c *Client) PutSpaceKeys(ctx context.Context, space string, version int, rotate bool, grants []KeyGrant) error {
	r := &request{method: http.MethodPut, path: []string{"spaces", space, "keys", strconv.Itoa(version)}}
	return c.doJSON(ctx, r, PutSpaceKeysRequest{Rotate: rotate, Grants: grants}, nil)
}

// CreateDeviceRequest 由新设备发起授权请求 (POST /devices/requests)。
func (c *Client) CreateDeviceRequest(ctx context.Context, in CreateDeviceRequest) (*DeviceRequest, error) {
	var out DeviceRequest
	if err := c.doJSON(ctx, &request{method: http.MethodPost, path: []string{"devices", "requests"}}, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeviceRequests 由已有设备列出本人待处理的授权请求 (GET /devices/requests)。
func (c *Client) DeviceRequests(ctx context.Context) ([]DeviceRequest, error) {
	var out struct {
		Requests []DeviceRequest `json:"requests"`
	}
	err := c.doJSON(ctx, &request{method: http.MethodGet, path: []string{"devices", "requests"}}, nil, &out)
	return out.Requests, err
}

// DeviceRequest 读取一条授权请求的当前状态 (GET /devices/requests/{id})。
func (c *Client) DeviceRequest(ctx context.Context, id string) (*DeviceRequest, error) {
	var out DeviceRequest
	if err := c.doJSON(ctx, &request{method: http.MethodGet, path: []string{"devices", "requests", id}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) deviceStep(ctx context.Context, id, step string, in any) (*DeviceRequest, error) {
	var out DeviceRequest
	r := &request{method: http.MethodPost, path: []string{"devices", "requests", id, step}}
	if err := c.doJSON(ctx, r, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChallengeDeviceRequest 由批准设备提交其 nonce (POST /devices/requests/{id}/challenge)。
func (c *Client) ChallengeDeviceRequest(ctx context.Context, id, approverNonce string) (*DeviceRequest, error) {
	return c.deviceStep(ctx, id, "challenge", map[string]string{"approverNonce": approverNonce})
}

// RevealDeviceRequest 由新设备揭示其承诺过的 nonce (POST /devices/requests/{id}/reveal)。
func (c *Client) RevealDeviceRequest(ctx context.Context, id, deviceNonce string) (*DeviceRequest, error) {
	return c.deviceStep(ctx, id, "reveal", map[string]string{"deviceNonce": deviceNonce})
}

// ApproveDeviceRequest 由批准设备提交封装给临时公钥的用户私钥 (POST /devices/requests/{id}/approve)。
func (c *Client) ApproveDeviceRequest(ctx context.Context, id, sealedKey string) (*DeviceRequest, error) {
	return c.deviceStep(ctx, id, "approve", map[string]string{"sealedKey": sealedKey})
}

// ClaimDeviceRequest 由新设备领取封装结果 (POST /devices/requests/{id}/claim), 领取后服务端删除请求。
func (c *Client) ClaimDeviceRequest(ctx context.Context, id string) (*DeviceRequest, error) {
	return c.deviceStep(ctx, id, "claim", nil)
}

// DeleteDeviceRequest 撤回 (新设备) 或拒绝 (已有设备) 授权请求 (DELETE /devices/requests/{id})。
func (c *Client) DeleteDeviceRequest(ctx context.Context, id string) error {
	return c.doJSON(ctx, &request{method: http.MethodDelete, path: []string{"devices", "requests", id}}, nil, nil)
}

// TouchPresence 发送一次「正在编辑」心跳 (PUT /spaces/{space}/presence/{collection}/{id}), 服务端 60 秒过期。
func (c *Client) TouchPresence(ctx context.Context, space, collection, id string) error {
	r := &request{method: http.MethodPut, path: []string{"spaces", space, "presence", collection, id}, noRetry: true}
	return c.doJSON(ctx, r, struct{}{}, nil)
}
