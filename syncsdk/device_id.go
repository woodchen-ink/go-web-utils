package syncsdk

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 设备标识: 让「正在编辑」按设备区分。同一账号在两台设备上打开同一份文档时,
// 双方都能看到对方; 不带设备标识的老客户端仍按账号区分 (看不到自己任何设备)。
const (
	// HeaderDevice 携带设备标识 (ClientOptions.DeviceID), 每个请求都带。
	HeaderDevice = "X-Sync-Device"
	// HeaderDeviceName 携带设备显示名 (ClientOptions.DeviceName), UTF-8 经百分号编码。
	HeaderDeviceName = "X-Sync-Device-Name"
	// MaxDeviceIDLen 是设备标识的最大长度。
	MaxDeviceIDLen = 64
	// MaxDeviceNameLen 是设备显示名的最大字符数, 超出截断。
	MaxDeviceNameLen = 64
)

// ValidDeviceID 报告 s 是否是合法的设备标识: 1~64 位字母、数字、'-'、'_'、'.'。
func ValidDeviceID(s string) bool {
	if s == "" || len(s) > MaxDeviceIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// CleanDeviceName 去掉控制字符与首尾空白, 截断到 MaxDeviceNameLen 个字符。
func CleanDeviceName(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxDeviceNameLen {
		s = strings.TrimSpace(string([]rune(s)[:MaxDeviceNameLen]))
	}
	return s
}

// DeviceFromHeader 从请求头取设备标识与显示名 (服务端用)。
// 标识不合法时两者都返回空串, 按不带设备标识的老客户端处理; 没有标识时忽略显示名。
func DeviceFromHeader(h http.Header) (id, name string) {
	id = h.Get(HeaderDevice)
	if !ValidDeviceID(id) {
		return "", ""
	}
	raw := h.Get(HeaderDeviceName)
	if dec, err := url.PathUnescape(raw); err == nil {
		raw = dec
	}
	return id, CleanDeviceName(raw)
}

// IsOwnPresence 报告 p 是否是调用方 (user, device) 自己的心跳, 下发 presence 时应排除。
// device 为空 (老客户端) 时按账号判定, 与引入设备标识前的行为一致。
func IsOwnPresence(p Presence, user, device string) bool {
	if p.UserID != user {
		return false
	}
	return device == "" || p.DeviceID == device
}

// NewDeviceID 生成一个随机设备标识 (32 位十六进制)。每台设备生成一次并持久化, 见 LoadOrCreateDeviceID。
func NewDeviceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand 在受支持平台上不会失败
	}
	return hex.EncodeToString(b[:])
}

// LoadOrCreateDeviceID 读取 path 里保存的设备标识; 文件不存在或内容不合法时生成新标识写入 (权限 0600)。
// path 应放在应用数据目录 (不随配置同步), 否则多台设备会读到同一个标识。
func LoadOrCreateDeviceID(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); ValidDeviceID(id) {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("syncsdk: read device id: %w", err)
	}
	id := NewDeviceID()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("syncsdk: write device id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("syncsdk: write device id: %w", err)
	}
	return id, nil
}
