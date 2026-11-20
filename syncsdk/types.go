package syncsdk

import (
	"encoding/json"
	"time"
)

// 本文件是 /api/sync/v1 的线上类型。JSON 字段统一 camelCase, 时间为 RFC3339,
// 二进制值 (公钥、封装密钥、nonce) 为无填充 base64url 字符串。
// 成功响应外层是 resputil 的 {code: 200, data, msg}, 这里的类型都是 data 的内容。

// 空间类型。
const (
	SpaceTypePersonal = "personal"
	SpaceTypeOrg      = "org"
)

// PersonalSpace 是个人空间在路径中的标识。
const PersonalSpace = "me"

// 组织角色, 由高到低。个人空间里本人视为 owner。
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// 应用的端到端加密设置。
const (
	E2EOff      = "off"
	E2ERequired = "required"
)

// Space 是 GET /spaces 返回的一个可访问空间。
type Space struct {
	// ID 是路径里用的空间标识: 个人空间为 "me", 组织空间为组织公开标识。
	ID string `json:"id"`
	// Type 为 SpaceTypePersonal 或 SpaceTypeOrg。
	Type string `json:"type"`
	// Name 是组织名, 个人空间为空。
	Name string `json:"name,omitempty"`
	// Role 是本人在该空间的角色。
	Role string `json:"role"`
	// ReadOnly 为 true 时 (订阅到期、组织 readonly) 只能拉取与下载。
	ReadOnly bool `json:"readonly"`
	// E2E 是应用的端到端加密设置 (E2EOff / E2ERequired)。
	E2E string `json:"e2e"`
	// Seq 是空间当前序号。
	Seq int64 `json:"seq"`
	// Quota 与 Usage 是配额与用量; 字段为 0 表示不限或未统计。
	Quota Usage `json:"quota"`
	Usage Usage `json:"usage"`
}

// Usage 是空间的存储量与文档数。
type Usage struct {
	Bytes int64 `json:"bytes"`
	Docs  int64 `json:"docs"`
}

// Doc 是服务端的一份文档。UpdatedBy / UpdatedAt 由服务端按令牌写入, 在密文之外下发;
// 展示「谁改的」只认这两个字段, 不认 payload 里自报的身份。
type Doc struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Rev        int64  `json:"rev"`
	Deleted    bool   `json:"deleted"`
	// Payload 为不透明 JSON; 端到端加密空间里是 e2e.Envelope。墓碑与写入应答不带。
	Payload   json.RawMessage `json:"payload,omitempty"`
	UpdatedBy string          `json:"updatedBy"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// Presence 是一条「正在编辑」记录。
type Presence struct {
	Collection string    `json:"collection"`
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// ChangesPage 是 GET /spaces/{space}/changes 的一页。
type ChangesPage struct {
	// Docs 按 Rev 升序, 含墓碑。
	Docs []Doc `json:"docs"`
	// Cursor 是下次请求的 since。
	Cursor int64 `json:"cursor"`
	// HasMore 为 true 时应立即继续拉取。
	HasMore bool `json:"hasMore"`
	// Presence 是他人正在编辑的文档 (不含本人)。
	Presence []Presence `json:"presence"`
}

// PutDocRequest 是 PUT /spaces/{space}/docs/{collection}/{id} 的请求体。
type PutDocRequest struct {
	BaseRev int64           `json:"baseRev"`
	Payload json.RawMessage `json:"payload"`
}

// 批量写操作类型。
const (
	OpPut    = "put"
	OpDelete = "delete"
)

// BatchOp 是 POST /spaces/{space}/batch 里的一个写操作。
type BatchOp struct {
	Op         string          `json:"op"`
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	BaseRev    int64           `json:"baseRev"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// BatchRequest 是批量写请求体, 最多 MaxBatchOps 个操作。
type BatchRequest struct {
	Ops []BatchOp `json:"ops"`
}

// MaxBatchOps 是一次批量写的操作数上限。
const MaxBatchOps = 100

// BatchResult 是批量写里一个操作的结果, 与请求顺序一一对应。
type BatchResult struct {
	// Status 为该操作单独执行时的 HTTP 状态 (200 / 409 / 403 / 413 ...)。
	Status int `json:"status"`
	// Code 与 Msg 为业务码与英文短语, 成功时 Code 为 200。
	Code int    `json:"code"`
	Msg  string `json:"msg,omitempty"`
	// Doc 成功时为写入后的元数据 (不带 payload); 409 时为服务端当前文档 (带 payload)。
	Doc *Doc `json:"doc,omitempty"`
}

// BatchResponse 是批量写应答。
type BatchResponse struct {
	Results []BatchResult `json:"results"`
}

// UserKeys 是 GET / PUT /keys/me 的内容。
type UserKeys struct {
	// PublicKey 是用户 X25519 公钥 (base64url)。
	PublicKey string `json:"publicKey"`
	// Escrow 是 e2e.Escrow 的 JSON, 服务端原样保存。
	Escrow json.RawMessage `json:"escrow"`
	// RecoveryVersion 每次重置恢复码加 1, 首次开通为 1。PUT 时服务端要求等于当前值 + 1,
	// 且已存在记录时 PublicKey 不得改变, 否则回 409。
	RecoveryVersion int       `json:"recoveryVersion"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
}

// WrappedKey 是本人在某空间某版本的封装密钥。
type WrappedKey struct {
	Version int    `json:"version"`
	Wrapped string `json:"wrapped"`
}

// KeyMember 是空间成员及其公钥。
type KeyMember struct {
	UserID    string `json:"userId"`
	PublicKey string `json:"publicKey"`
}

// SpaceKeys 是 GET /spaces/{space}/keys 的内容。
type SpaceKeys struct {
	// Role 是本人在该空间的角色, 决定是否由本客户端授予与轮换。
	Role string `json:"role"`
	// CurrentVersion 是空间当前密钥版本, 0 表示尚未建立。
	CurrentVersion int `json:"currentVersion"`
	// Keys 是本人持有的各版本封装密钥。
	Keys []WrappedKey `json:"keys"`
	// Pending 是已上传公钥、但还没有当前版本密钥的成员。
	Pending []KeyMember `json:"pending"`
	// Members 是全部已上传公钥的现有成员 (含本人), 轮换时新版本须覆盖他们。
	Members []KeyMember `json:"members"`
	// RotationRequired 在持有过密钥的成员被移除后为 true, 直到新版本提交。
	RotationRequired bool `json:"rotationRequired"`
}

// KeyGrant 是一条为成员封装的密钥。
type KeyGrant struct {
	UserID  string `json:"userId"`
	Wrapped string `json:"wrapped"`
}

// PutSpaceKeysRequest 是 PUT /spaces/{space}/keys/{version} 的请求体。
type PutSpaceKeysRequest struct {
	// Rotate 为 true 表示建立新版本 (首次 version 1 或轮换): 服务端要求 version 等于当前版本 + 1,
	// 否则回 409 (另一台管理端抢先), 且 grants 须覆盖全部成员。为 false 表示给已有版本补授,
	// version 不得大于当前版本, 已有的封装不覆盖 (重复提交幂等)。
	Rotate bool       `json:"rotate,omitempty"`
	Grants []KeyGrant `json:"grants"`
}

// 新设备授权请求状态。
const (
	DeviceStatusPending    = "pending"
	DeviceStatusChallenged = "challenged"
	DeviceStatusRevealed   = "revealed"
	DeviceStatusApproved   = "approved"
	DeviceStatusRejected   = "rejected"
)

// DeviceRequest 是一条新设备授权请求 (服务端 Redis 10 分钟 TTL)。
// 各 nonce 与封装结果只能写入一次, 重复提交相同值幂等, 不同值回 409。
type DeviceRequest struct {
	ID            string    `json:"id"`
	DeviceName    string    `json:"deviceName,omitempty"`
	TempPublicKey string    `json:"tempPublicKey"`
	Commitment    string    `json:"commitment"`
	ApproverNonce string    `json:"approverNonce,omitempty"`
	DeviceNonce   string    `json:"deviceNonce,omitempty"`
	SealedKey     string    `json:"sealedKey,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// CreateDeviceRequest 是 POST /devices/requests 的请求体。
type CreateDeviceRequest struct {
	DeviceName    string `json:"deviceName,omitempty"`
	TempPublicKey string `json:"tempPublicKey"`
	Commitment    string `json:"commitment"`
}

// Event 是 SSE 事件流 GET /spaces/{space}/events 里的一条事件。
type Event struct {
	// Type 为 "changed" 或 "presence"。
	Type string
	// Seq 是 changed 事件携带的最新序号。
	Seq int64
	// Presence 是 presence 事件携带的他人编辑状态。
	Presence []Presence
}

// SSE 事件类型。
const (
	EventChanged  = "changed"
	EventPresence = "presence"
)
