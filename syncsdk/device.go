package syncsdk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/woodchen-ink/go-web-utils/syncsdk/e2e"
)

// 新设备授权流程 (协议细节见 e2e 包 device.go):
//
//	新设备                              已有设备
//	StartDeviceEnrollment  ──请求──▶   PendingDeviceRequests
//	                       ◀─challenge─ BeginDeviceApproval
//	WaitCode (reveal) ─────reveal───▶   WaitCode
//	  显示 6 位码                         显示 6 位码, 用户核对一致后
//	WaitApproval           ◀──封装────  Approve
//
// 两端的 6 位码都在本地计算, 服务端只转交; 服务端替换临时公钥会让两端码不一致。

// maxDeviceRequestTTL 是客户端接受的请求有效期上限 (服务端 10 分钟 + 时钟偏差余量)。
const (
	maxDeviceRequestTTL = 10 * time.Minute
	clockSkew           = 2 * time.Minute
)

// checkDeviceTimes 校验请求时间戳: 未过期、有效期不超过 10 分钟、创建时间不在未来。
func checkDeviceTimes(r *DeviceRequest, now time.Time) error {
	if r.ExpiresAt.IsZero() || r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: device request without timestamps", ErrProtocol)
	}
	if r.ExpiresAt.Sub(r.CreatedAt) > maxDeviceRequestTTL+time.Second || r.CreatedAt.After(now.Add(clockSkew)) {
		return fmt.Errorf("%w: device request timestamps", ErrProtocol)
	}
	if !now.Before(r.ExpiresAt.Add(clockSkew)) {
		return ErrRequestExpired
	}
	return nil
}

// DeviceEnrollment 是新设备一侧进行中的授权请求。
type DeviceEnrollment struct {
	m        *KeyManager
	req      *e2e.DeviceRequester
	userPub  e2e.PublicKey
	info     DeviceRequest
	revealed bool
}

// StartDeviceEnrollment 在本机没有私钥时向已登录的其他设备发起授权请求。
// deviceName 显示在批准端 (如「Windows · 前台电脑」)。
func (m *KeyManager) StartDeviceEnrollment(ctx context.Context, deviceName string) (*DeviceEnrollment, error) {
	remote, err := m.client.MyKeys(ctx)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	userPub, err := e2e.ParsePublicKey(remote.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	req, err := e2e.NewDeviceRequester()
	if err != nil {
		return nil, err
	}
	created, err := m.client.CreateDeviceRequest(ctx, CreateDeviceRequest{
		DeviceName:    deviceName,
		TempPublicKey: req.TempPublic().String(),
		Commitment:    e2e.EncodeBase64(req.Commitment()),
	})
	if err != nil {
		return nil, err
	}
	if created.ID == "" || created.TempPublicKey != req.TempPublic().String() {
		return nil, fmt.Errorf("%w: device request echo", ErrProtocol)
	}
	if err := checkDeviceTimes(created, m.opts.Now()); err != nil {
		return nil, err
	}
	return &DeviceEnrollment{m: m, req: req, userPub: userPub, info: *created}, nil
}

// ID 返回请求标识。
func (d *DeviceEnrollment) ID() string { return d.info.ID }

// ExpiresAt 返回请求过期时间。
func (d *DeviceEnrollment) ExpiresAt() time.Time { return d.info.ExpiresAt }

// poll 轮询请求状态直到 done 返回 true。被拒绝 / 撤回 (404) 返回 ErrRequestRejected。
func (m *KeyManager) pollDevice(ctx context.Context, id string, done func(*DeviceRequest) bool) (*DeviceRequest, error) {
	for {
		r, err := m.client.DeviceRequest(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil, ErrRequestRejected
		}
		if err != nil && !IsTransient(err) {
			return nil, err
		}
		if err == nil {
			if r.Status == DeviceStatusRejected {
				return nil, ErrRequestRejected
			}
			if err := checkDeviceTimes(r, m.opts.Now()); err != nil {
				return nil, err
			}
			if done(r) {
				return r, nil
			}
		}
		if err := sleep(ctx, m.opts.PollInterval); err != nil {
			return nil, err
		}
	}
}

// WaitCode 等待已有设备响应, 然后揭示本机 nonce 并返回 6 位校验码供显示。
// 校验码只计算一次: 揭示之后再次调用返回同一个码。
func (d *DeviceEnrollment) WaitCode(ctx context.Context) (string, error) {
	r, err := d.m.pollDevice(ctx, d.info.ID, func(r *DeviceRequest) bool { return r.ApproverNonce != "" })
	if err != nil {
		return "", err
	}
	if d.revealed && r.ApproverNonce != d.info.ApproverNonce {
		return "", fmt.Errorf("%w: approver nonce changed", ErrProtocol)
	}
	na, err := e2e.DecodeBase64(r.ApproverNonce)
	if err != nil {
		return "", fmt.Errorf("%w: approver nonce", ErrProtocol)
	}
	code, err := d.req.Code(d.info.ID, d.userPub, na)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if !d.revealed {
		if _, err := d.m.client.RevealDeviceRequest(ctx, d.info.ID, e2e.EncodeBase64(d.req.Nonce())); err != nil {
			return "", err
		}
		d.revealed = true
		d.info.ApproverNonce = r.ApproverNonce
	}
	return code, nil
}

// WaitApproval 等待已有设备批准, 领取并解开用户私钥, 校验与服务端公钥一致后存入本机密钥库。
func (d *DeviceEnrollment) WaitApproval(ctx context.Context) error {
	if !d.revealed {
		return errors.New("syncsdk: call WaitCode before WaitApproval")
	}
	if _, err := d.m.pollDevice(ctx, d.info.ID, func(r *DeviceRequest) bool { return r.Status == DeviceStatusApproved }); err != nil {
		return err
	}
	r, err := d.m.client.ClaimDeviceRequest(ctx, d.info.ID)
	if err != nil {
		return err
	}
	sealed, err := e2e.DecodeBase64(r.SealedKey)
	if err != nil || len(sealed) == 0 {
		return fmt.Errorf("%w: sealed key", ErrProtocol)
	}
	u, err := d.req.OpenUserKey(d.info.ID, sealed, d.userPub)
	if err != nil {
		return err
	}
	d.req.Destroy()
	return d.m.saveUser(ctx, u)
}

// Cancel 撤回请求。
func (d *DeviceEnrollment) Cancel(ctx context.Context) error {
	d.req.Destroy()
	return d.m.client.DeleteDeviceRequest(ctx, d.info.ID)
}

// PendingDeviceRequests 由已解锁的设备列出待批准的请求 (已过期与时间戳异常的被过滤)。
func (m *KeyManager) PendingDeviceRequests(ctx context.Context) ([]DeviceRequest, error) {
	all, err := m.client.DeviceRequests(ctx)
	if err != nil {
		return nil, err
	}
	now := m.opts.Now()
	var out []DeviceRequest
	for _, r := range all {
		if r.Status == DeviceStatusApproved || r.Status == DeviceStatusRejected || checkDeviceTimes(&r, now) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// DeviceApproval 是已有设备一侧对一条请求的批准过程。
type DeviceApproval struct {
	m       *KeyManager
	user    *e2e.UserKey
	info    DeviceRequest
	tempPub e2e.PublicKey
	nonce   []byte
	code    string
}

// BeginDeviceApproval 开始批准一条请求: 提交本机 nonce。之后调用 WaitCode 取得校验码。
func (m *KeyManager) BeginDeviceApproval(ctx context.Context, req DeviceRequest) (*DeviceApproval, error) {
	user, err := m.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkDeviceTimes(&req, m.opts.Now()); err != nil {
		return nil, err
	}
	tempPub, err := e2e.ParsePublicKey(req.TempPublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	nonce, err := e2e.NewDeviceNonce()
	if err != nil {
		return nil, err
	}
	if _, err := m.client.ChallengeDeviceRequest(ctx, req.ID, e2e.EncodeBase64(nonce)); err != nil {
		return nil, err
	}
	return &DeviceApproval{m: m, user: user, info: req, tempPub: tempPub, nonce: nonce}, nil
}

// DeviceName 返回请求方设备名。
func (a *DeviceApproval) DeviceName() string { return a.info.DeviceName }

// WaitCode 等待新设备揭示 nonce, 校验承诺后返回 6 位校验码。用户确认与新设备上一致后再 Approve。
// 承诺不符 (服务端或中间人替换了临时公钥) 返回 ErrProtocol。
func (a *DeviceApproval) WaitCode(ctx context.Context) (string, error) {
	r, err := a.m.pollDevice(ctx, a.info.ID, func(r *DeviceRequest) bool { return r.DeviceNonce != "" })
	if err != nil {
		return "", err
	}
	if r.TempPublicKey != a.info.TempPublicKey || r.Commitment != a.info.Commitment {
		return "", fmt.Errorf("%w: device request changed", ErrProtocol)
	}
	nn, err := e2e.DecodeBase64(r.DeviceNonce)
	if err != nil {
		return "", fmt.Errorf("%w: device nonce", ErrProtocol)
	}
	commitment, err := e2e.DecodeBase64(a.info.Commitment)
	if err != nil || !e2e.VerifyDeviceCommitment(a.tempPub, commitment, nn) {
		return "", fmt.Errorf("%w: device commitment mismatch", ErrProtocol)
	}
	code, err := e2e.DeviceSAS(a.info.ID, a.tempPub, a.user.Public(), nn, a.nonce)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	a.code = code
	return code, nil
}

// Approve 在用户确认两端校验码一致后, 把用户私钥封装给新设备临时公钥并提交。
func (a *DeviceApproval) Approve(ctx context.Context) error {
	if a.code == "" {
		return errors.New("syncsdk: call WaitCode and confirm the code before Approve")
	}
	if err := checkDeviceTimes(&a.info, a.m.opts.Now()); err != nil {
		return err
	}
	sealed, err := e2e.SealUserKeyForDevice(a.info.ID, a.user, a.tempPub)
	if err != nil {
		return err
	}
	_, err = a.m.client.ApproveDeviceRequest(ctx, a.info.ID, e2e.EncodeBase64(sealed))
	return err
}

// Reject 拒绝请求。
func (a *DeviceApproval) Reject(ctx context.Context) error {
	return a.m.client.DeleteDeviceRequest(ctx, a.info.ID)
}
