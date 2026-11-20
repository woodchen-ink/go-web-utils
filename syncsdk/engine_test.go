package syncsdk_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/go-web-utils/syncsdk"
	"github.com/woodchen-ink/go-web-utils/syncsdk/e2e"
	"github.com/woodchen-ink/go-web-utils/syncsdk/synctest"
)

const me = syncsdk.PersonalSpace

func TestOfflineQueueThenSync(t *testing.T) {
	srv := newE2EServer(t)
	laptop := newDevice(t, srv, "alice", deviceOpts{})
	code := laptop.enroll()
	laptop.sync(me) // 建立个人空间密钥

	srv.SetOffline(true)
	laptop.save(me, "shipments", "s1", `{"no":"INV-1","total":100}`)
	laptop.save(me, "shipments", "s2", `{"no":"INV-2"}`)
	laptop.save(me, "shipments", "s1", `{"no":"INV-1","total":120}`) // 同一文档只保留最新
	if _, err := laptop.engine.Sync(laptop.ctx(), me); !syncsdk.IsTransient(err) {
		t.Fatalf("offline sync should fail transiently, got %v", err)
	}
	if n := laptop.outbox.Len(); n != 2 {
		t.Fatalf("outbox has %d items, want 2", n)
	}

	srv.SetOffline(false)
	res := laptop.sync(me)
	if res.Push.Pushed != 2 || laptop.outbox.Len() != 0 {
		t.Fatalf("pushed %d, outbox %d", res.Push.Pushed, laptop.outbox.Len())
	}
	if d := laptop.app.get("shipments", "s1"); d.Rev == 0 || d.UpdatedBy != "alice" {
		t.Fatalf("commit not applied: %+v", d)
	}

	// 服务端只有密文。
	raw := srv.RawDoc(synctest.PersonalKey("alice"), "shipments", "s1")
	if bytes.Contains(raw.Payload, []byte("INV-1")) {
		t.Fatal("server stores plaintext")
	}
	if _, err := e2e.ParseEnvelope(raw.Payload); err != nil {
		t.Fatalf("payload is not an envelope: %v", err)
	}

	// 第二台设备用恢复码解锁后拉到同样的内容。
	desktop := newDevice(t, srv, "alice", deviceOpts{token: tokenOf(t, srv, "alice")})
	if st, _ := desktop.keys.Status(desktop.ctx()); st != syncsdk.KeyStatusLocked {
		t.Fatalf("new device status %v", st)
	}
	if err := desktop.keys.UnlockWithRecoveryCode(desktop.ctx(), strings.ToLower(code)); err != nil {
		t.Fatal(err)
	}
	desktop.sync(me)
	if got := desktop.app.get("shipments", "s1"); got == nil || got.Value != `{"no":"INV-1","total":120}` {
		t.Fatalf("desktop got %+v", got)
	}
}

// tokenOf 给已存在的用户再发一个令牌 (同一用户的另一台设备)。
func tokenOf(t *testing.T, srv *synctest.Server, user string) string {
	t.Helper()
	return srv.AddUser(user)
}

func setupOrg(t *testing.T, srv *synctest.Server, members ...string) map[string]*device {
	t.Helper()
	devs := map[string]*device{}
	srv.CreateOrg("org_1", "ACME", "owner")
	owner := newDevice(t, srv, "owner", deviceOpts{})
	owner.enroll()
	devs["owner"] = owner
	for _, m := range members {
		d := newDevice(t, srv, m, deviceOpts{})
		srv.AddMember("org_1", m, syncsdk.RoleMember)
		d.enroll()
		devs[m] = d
	}
	owner.sync("org_1") // 建立空间密钥并授予已开通的成员
	for _, m := range members {
		devs[m].sync("org_1")
	}
	return devs
}

func TestConcurrentEditConflict(t *testing.T) {
	cases := []struct {
		name    string
		resolve syncsdk.Resolution
		want    string // 最终服务端内容
		copy    bool
	}{
		{"keep remote", syncsdk.Resolution{Kind: syncsdk.KeepRemote}, `{"v":"bob"}`, false},
		{"overwrite", syncsdk.Resolution{Kind: syncsdk.Overwrite}, `{"v":"alice"}`, false},
		{"save as copy", syncsdk.Resolution{Kind: syncsdk.SaveAsCopy, CopyID: "s1-copy"}, `{"v":"bob"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newE2EServer(t)
			devs := setupOrg(t, srv, "alice", "bob")
			alice, bob := devs["alice"], devs["bob"]
			alice.save("org_1", "shipments", "s1", `{"v":"base"}`)
			alice.sync("org_1")
			bob.sync("org_1")

			bob.save("org_1", "shipments", "s1", `{"v":"bob"}`)
			alice.save("org_1", "shipments", "s1", `{"v":"alice"}`)
			bob.sync("org_1")

			var seen *syncsdk.Conflict
			alice.resolve = func(c syncsdk.Conflict) syncsdk.Resolution {
				seen = &c
				return tc.resolve
			}
			res := alice.sync("org_1")
			if res.Push.Conflicts != 1 || seen == nil {
				t.Fatalf("conflicts = %d", res.Push.Conflicts)
			}
			if string(seen.Local.Value) != `{"v":"alice"}` || string(seen.Remote.Value) != `{"v":"bob"}` || seen.Remote.UpdatedBy != "bob" {
				t.Fatalf("conflict payloads: local %s remote %s by %s", seen.Local.Value, seen.Remote.Value, seen.Remote.UpdatedBy)
			}
			bob.sync("org_1")
			alice.sync("org_1")
			for _, d := range []*device{alice, bob} {
				if got := d.app.get("shipments", "s1"); got.Value != tc.want {
					t.Errorf("%s has %s, want %s", d.user, got.Value, tc.want)
				}
			}
			if tc.copy {
				if got := bob.app.get("shipments", "s1-copy"); got == nil || got.Value != `{"v":"alice"}` {
					t.Fatalf("copy not synced: %+v", got)
				}
			}
			if alice.outbox.Len() != 0 {
				t.Fatal("outbox not drained")
			}
		})
	}
}

func TestLostResponseRetryIsNotAConflict(t *testing.T) {
	srv := newE2EServer(t)
	d := newDevice(t, srv, "alice", deviceOpts{})
	d.enroll()
	d.sync(me)
	d.save(me, "shipments", "s1", `{"a":1}`)
	d.save(me, "shipments", "s2", `{"a":2}`)
	called := false
	d.resolve = func(syncsdk.Conflict) syncsdk.Resolution {
		called = true
		return syncsdk.Resolution{Kind: syncsdk.KeepRemote}
	}
	srv.DropResponseNext(1) // batch 已写入, 应答丢失; 客户端重试后看到 409 + 自己的写入
	res := d.sync(me)
	if called || res.Push.Conflicts != 0 || res.Push.Pushed != 2 || d.outbox.Len() != 0 {
		t.Fatalf("retry after lost response: called=%v push=%+v outbox=%d", called, res.Push, d.outbox.Len())
	}
	if d.app.get("shipments", "s1").Rev == 0 {
		t.Fatal("rev not committed")
	}
}

func TestMemberJoinGrantRead(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv)
	owner := devs["owner"]
	owner.save("org_1", "shipments", "s1", `{"secret":"price list"}`)
	owner.sync("org_1")

	carol := newDevice(t, srv, "carol", deviceOpts{})
	srv.AddMember("org_1", "carol", syncsdk.RoleMember)
	carol.enroll()
	if _, err := carol.engine.Sync(carol.ctx(), "org_1"); !errors.Is(err, syncsdk.ErrKeyNotGranted) {
		t.Fatalf("before grant: %v", err)
	}

	res := owner.sync("org_1")
	if len(res.Keys.Granted) != 1 || res.Keys.Granted[0].UserID != "carol" {
		t.Fatalf("granted %+v", res.Keys.Granted)
	}
	carolPub, _ := carol.keys.PublicKey(carol.ctx())
	if res.Keys.Granted[0].Fingerprint != carolPub.Fingerprint() {
		t.Fatal("fingerprint shown to the granter does not match carol's key")
	}
	carol.sync("org_1")
	if got := carol.app.get("shipments", "s1"); got == nil || got.Value != `{"secret":"price list"}` {
		t.Fatalf("carol reads %+v", got)
	}
	// 成员不能管理密钥, 也不会触发授予。
	if _, err := carol.keys.RotateSpace(carol.ctx(), "org_1"); !errors.Is(err, syncsdk.ErrForbidden) {
		t.Fatalf("member rotate: %v", err)
	}
}

func TestMemberRemovalRotation(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv, "bob")
	owner, bob := devs["owner"], devs["bob"]
	owner.save("org_1", "shipments", "old", `{"n":1}`)
	owner.sync("org_1")
	bob.sync("org_1")
	if bob.app.get("shipments", "old") == nil {
		t.Fatal("bob cannot read before removal")
	}
	bobRing, _ := bob.keys.Keyring(bob.ctx(), "org_1")

	srv.RemoveMember("org_1", "bob")
	res := owner.sync("org_1")
	if res.Keys.Rotated != 2 {
		t.Fatalf("rotated = %d", res.Keys.Rotated)
	}
	owner.save("org_1", "shipments", "new", `{"n":2}`)
	owner.sync("org_1")

	raw := srv.RawDoc("org_1", "shipments", "new")
	env, _ := e2e.ParseEnvelope(raw.Payload)
	if env.KV != 2 {
		t.Fatalf("new doc encrypted with kv %d", env.KV)
	}
	if _, err := bob.engine.Pull(bob.ctx(), "org_1"); !errors.Is(err, syncsdk.ErrForbidden) {
		t.Fatalf("removed member pull: %v", err)
	}
	// 即使拿到密文 (服务端泄露), 被移除者的旧密钥也打不开新文档。
	if _, err := bobRing.Open("shipments", "new", env); !errors.Is(err, e2e.ErrUnknownKeyVersion) {
		t.Fatalf("removed member decrypted new doc: %v", err)
	}

	// 全量重加密把旧文档换到新版本。
	n, err := owner.engine.Reencrypt(owner.ctx(), "org_1")
	if err != nil || n != 1 {
		t.Fatalf("reencrypt n=%d err=%v", n, err)
	}
	oldEnv, _ := e2e.ParseEnvelope(srv.RawDoc("org_1", "shipments", "old").Payload)
	if oldEnv.KV != 2 {
		t.Fatalf("old doc still kv %d", oldEnv.KV)
	}
	if _, err := bobRing.Open("shipments", "old", oldEnv); err == nil {
		t.Fatal("removed member reads re-encrypted doc")
	}
	// owner 的本地 rev 已随 Committed 更新, 再改不会冲突。
	owner.save("org_1", "shipments", "old", `{"n":3}`)
	if r := owner.sync("org_1"); r.Push.Conflicts != 0 {
		t.Fatal("re-encryption left a stale rev behind")
	}
}

func TestCursorExpiredFullResync(t *testing.T) {
	srv := newE2EServer(t)
	a := newDevice(t, srv, "alice", deviceOpts{})
	a.enroll()
	a.sync(me)
	// b 是同一用户的第二台设备, 直接复用 a 的密钥库内容 (省去恢复码的 Argon2 开销)。
	b := cloneDevice(t, srv, a)
	a.save(me, "contacts", "c1", `{"n":"one"}`)
	a.save(me, "contacts", "c2", `{"n":"two"}`)
	a.sync(me)
	b.sync(me)

	if err := a.engine.Delete(a.ctx(), me, "contacts", "c1", a.app.get("contacts", "c1").Rev); err != nil {
		t.Fatal(err)
	}
	a.save(me, "contacts", "c3", `{"n":"three"}`)
	a.sync(me)
	srv.ExpireCursors(synctest.PersonalKey("alice"))

	res := b.sync(me)
	if !res.Pull.Reset || b.app.resets != 1 {
		t.Fatalf("reset=%v resets=%d", res.Pull.Reset, b.app.resets)
	}
	if got := strings.Join(b.app.ids("contacts"), ","); got != "c2,c3" {
		t.Fatalf("after resync: %s", got)
	}
}

// cloneDevice 建一台与 src 同用户、共享密钥库内容的新设备。
func cloneDevice(t *testing.T, srv *synctest.Server, src *device) *device {
	t.Helper()
	d := newDevice(t, srv, src.user, deviceOpts{token: tokenOf(t, srv, src.user)})
	for _, n := range src.keystore.Names() {
		v, _ := src.keystore.Get(context.Background(), n)
		_ = d.keystore.Put(context.Background(), n, v)
	}
	return d
}

func TestAppendOnlyActivityLog(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv, "bob")
	owner, bob := devs["owner"], devs["bob"]
	log := bob.engine.ActivityLog("org_1", "activity")
	id, err := log.Append(bob.ctx(), map[string]string{"action": "edit shipment s1", "actor": "owner"}) // 自报身份不可信
	if err != nil {
		t.Fatal(err)
	}
	bob.sync("org_1")
	owner.sync("org_1")
	got := owner.app.get("activity", id)
	if got == nil || got.UpdatedBy != "bob" {
		t.Fatalf("activity entry %+v: actor must come from the server", got)
	}

	if err := bob.engine.Save(bob.ctx(), "org_1", "activity", id, got.Rev, `{}`); !errors.Is(err, syncsdk.ErrAppendOnly) {
		t.Fatalf("update allowed: %v", err)
	}
	if err := bob.engine.Delete(bob.ctx(), "org_1", "activity", id, got.Rev); !errors.Is(err, syncsdk.ErrAppendOnly) {
		t.Fatalf("delete allowed: %v", err)
	}
	// 绕过引擎直接调用接口, 服务端同样拒绝。
	_, err = bob.client.DeleteDoc(bob.ctx(), "org_1", "activity", id, got.Rev)
	if !errors.Is(err, syncsdk.ErrForbidden) {
		t.Fatalf("server allowed delete: %v", err)
	}
	ring, _ := bob.keys.Keyring(bob.ctx(), "org_1")
	env, _ := ring.Seal("activity", id, []byte(`{"action":"forged"}`))
	payload, _ := json.Marshal(env)
	if _, err := bob.client.PutDoc(bob.ctx(), "org_1", "activity", id, got.Rev, payload); !errors.Is(err, syncsdk.ErrForbidden) {
		t.Fatalf("server allowed update: %v", err)
	}
}

func TestPermanentRejectionAndReadOnly(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv, "bob")
	bob := devs["bob"]
	// settings 需要 admin: 逐条 403, 交给 OnRejected 后出队, 其余照常推送。
	bob.save("org_1", "settings", "defaults", `{"x":1}`)
	bob.save("org_1", "contacts", "c1", `{"x":1}`)
	res := bob.sync("org_1")
	if res.Push.Rejected != 1 || res.Push.Pushed != 1 || len(bob.rejected) != 1 || bob.outbox.Len() != 0 {
		t.Fatalf("push %+v rejected %d outbox %d", res.Push, len(bob.rejected), bob.outbox.Len())
	}
	// 未登记的集合同样逐条拒绝。
	bob.save("org_1", "unknown", "x", `{}`)
	if r := bob.sync("org_1"); r.Push.Rejected != 1 {
		t.Fatalf("unregistered collection: %+v", r.Push)
	}

	// 空间只读: 整个请求被拒, 修改留在队列, 恢复后推送。
	srv.SetReadOnly("org_1", true)
	bob.save("org_1", "contacts", "c2", `{"x":2}`)
	if _, err := bob.engine.Sync(bob.ctx(), "org_1"); !errors.Is(err, syncsdk.ErrForbidden) {
		t.Fatalf("readonly sync: %v", err)
	}
	if bob.outbox.Len() != 1 {
		t.Fatal("readonly space dropped the local edit")
	}
	srv.SetReadOnly("org_1", false)
	if r := bob.sync("org_1"); r.Push.Pushed != 1 {
		t.Fatalf("after readonly: %+v", r.Push)
	}
}

func TestBlobDedupeAndDownload(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv, "bob")
	owner, bob := devs["owner"], devs["bob"]

	big := make([]byte, 300<<10+123) // 多个分块
	_, _ = rand.Read(big)
	path := filepath.Join(t.TempDir(), "stamp.png")
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	id1, err := owner.engine.UploadBlobFile(owner.ctx(), "org_1", path, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := owner.engine.UploadBlobBytes(owner.ctx(), "org_1", big, "image/png")
	if err != nil || id1 != id2 {
		t.Fatalf("ids %s %s err %v", id1, id2, err)
	}
	if n := srv.Count("PUT blob"); n != 1 {
		t.Fatalf("uploaded %d times, want 1", n)
	}
	if n := srv.Count("HEAD blob"); n != 2 {
		t.Fatalf("HEAD %d times", n)
	}

	p, err := bob.engine.BlobPath(bob.ctx(), "org_1", id1)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, big) {
		t.Fatal("downloaded content differs")
	}
	if _, err := bob.engine.BlobPath(bob.ctx(), "org_1", id1); err != nil {
		t.Fatal(err)
	}
	if n := srv.Count("GET blob"); n != 1 {
		t.Fatalf("cache miss: GET %d times", n)
	}
	// 他人在其它空间上传同样内容, 附件标识不同 (标识密钥按空间)。
	pid, _ := owner.engine.UploadBlobBytes(owner.ctx(), me, big, "")
	if pid == id1 {
		t.Fatal("blob ids leak equality across spaces")
	}
}

func TestDeviceApproval(t *testing.T) {
	srv := newE2EServer(t)
	old := newDevice(t, srv, "alice", deviceOpts{})
	old.enroll()
	old.save(me, "shipments", "s1", `{"x":"hello"}`)
	old.sync(me)
	fresh := newDevice(t, srv, "alice", deviceOpts{token: tokenOf(t, srv, "alice")})

	ctx := fresh.ctx()
	enr, err := fresh.keys.StartDeviceEnrollment(ctx, "Windows · 前台")
	if err != nil {
		t.Fatal(err)
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		code, err := enr.WaitCode(ctx)
		if err != nil {
			errCh <- err
			return
		}
		codeCh <- code
		errCh <- enr.WaitApproval(ctx)
	}()

	reqs, err := old.keys.PendingDeviceRequests(ctx)
	if err != nil || len(reqs) != 1 || reqs[0].DeviceName != "Windows · 前台" {
		t.Fatalf("pending %+v %v", reqs, err)
	}
	ap, err := old.keys.BeginDeviceApproval(ctx, reqs[0])
	if err != nil {
		t.Fatal(err)
	}
	codeOld, err := ap.WaitCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var codeNew string
	select {
	case codeNew = <-codeCh:
	case err := <-errCh:
		t.Fatal(err)
	}
	if codeOld != codeNew || len(codeOld) != 6 {
		t.Fatalf("codes differ: %s vs %s", codeOld, codeNew)
	}
	if err := ap.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if st, _ := fresh.keys.Status(ctx); st != syncsdk.KeyStatusUnlocked {
		t.Fatalf("status %v", st)
	}
	fresh.sync(me)
	if got := fresh.app.get("shipments", "s1"); got == nil || got.Value != `{"x":"hello"}` {
		t.Fatalf("fresh device reads %+v", got)
	}
}

func TestDeviceRequestExpiryAndReject(t *testing.T) {
	srv := newE2EServer(t)
	old := newDevice(t, srv, "alice", deviceOpts{})
	old.enroll()
	fresh := newDevice(t, srv, "alice", deviceOpts{token: tokenOf(t, srv, "alice")})
	enr, err := fresh.keys.StartDeviceEnrollment(fresh.ctx(), "pc")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _ := old.keys.PendingDeviceRequests(old.ctx())
	ap, _ := old.keys.BeginDeviceApproval(old.ctx(), reqs[0])
	if err := ap.Reject(old.ctx()); err != nil {
		t.Fatal(err)
	}
	if _, err := enr.WaitCode(fresh.ctx()); !errors.Is(err, syncsdk.ErrRequestRejected) {
		t.Fatalf("rejected request: %v", err)
	}

	enr2, _ := fresh.keys.StartDeviceEnrollment(fresh.ctx(), "pc")
	later := time.Now().Add(11 * time.Minute)
	srv.SetNow(func() time.Time { return later })
	if reqs, _ := old.keys.PendingDeviceRequests(old.ctx()); len(reqs) != 0 {
		t.Fatal("expired request still listed")
	}
	if _, err := enr2.WaitCode(fresh.ctx()); !errors.Is(err, syncsdk.ErrRequestRejected) {
		t.Fatalf("expired request: %v", err)
	}
}

func TestRecoveryCodeReset(t *testing.T) {
	srv := newE2EServer(t)
	a := newDevice(t, srv, "alice", deviceOpts{})
	oldCode := a.enroll()
	newCode, err := a.keys.ResetRecoveryCode(a.ctx())
	if err != nil {
		t.Fatal(err)
	}
	b := newDevice(t, srv, "alice", deviceOpts{token: tokenOf(t, srv, "alice")})
	if err := b.keys.UnlockWithRecoveryCode(b.ctx(), oldCode); !errors.Is(err, e2e.ErrDecrypt) {
		t.Fatalf("old code: %v", err)
	}
	if err := b.keys.UnlockWithRecoveryCode(b.ctx(), "0000-0000"); !errors.Is(err, e2e.ErrInvalidRecoveryCode) {
		t.Fatalf("typo code: %v", err)
	}
	if err := b.keys.UnlockWithRecoveryCode(b.ctx(), newCode.String()); err != nil {
		t.Fatal(err)
	}
	// 另一台设备抢先开通后再 Enroll 返回冲突, 不会覆盖公钥。
	c := newDevice(t, srv, "alice", deviceOpts{token: tokenOf(t, srv, "alice")})
	if _, err := c.keys.Enroll(c.ctx()); !errors.Is(err, syncsdk.ErrConflict) {
		t.Fatalf("second enroll: %v", err)
	}
}

func TestSubscribeReceivesChanges(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv, "bob")
	owner, bob := devs["owner"], devs["bob"]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- bob.engine.Subscribe(ctx, "org_1", syncsdk.SubscribeOptions{PollInterval: 50 * time.Millisecond, ReconnectMin: 10 * time.Millisecond, ReconnectMax: 50 * time.Millisecond})
	}()
	waitFor(t, func() bool { return true })

	owner.save("org_1", "shipments", "live", `{"x":1}`)
	owner.sync("org_1")
	waitFor(t, func() bool { return bob.app.get("shipments", "live") != nil })

	// 断开事件流后仍能收到 (重连或回退轮询)。
	srv.DropEventStreams("org_1")
	owner.save("org_1", "shipments", "live2", `{"x":2}`)
	owner.sync("org_1")
	waitFor(t, func() bool { return bob.app.get("shipments", "live2") != nil })

	// 本地保存会触发推送。
	bob.save("org_1", "contacts", "from-bob", `{"y":1}`)
	waitFor(t, func() bool { return bob.outbox.Len() == 0 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("subscribe returned %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPresence(t *testing.T) {
	srv := newE2EServer(t)
	devs := setupOrg(t, srv, "bob")
	owner, bob := devs["owner"], devs["bob"]
	stop := owner.engine.Editing(owner.ctx(), "org_1", "shipments", "s1")
	defer stop()
	waitFor(t, func() bool {
		bob.sync("org_1")
		bob.mu.Lock()
		defer bob.mu.Unlock()
		return len(bob.presence) == 1 && bob.presence[0].UserID == "owner" && bob.presence[0].ID == "s1"
	})
	owner.sync("org_1")
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.presence) != 0 {
		t.Fatal("own presence echoed back")
	}
}

func TestPlaintextMode(t *testing.T) {
	srv := synctest.New(synctest.Options{})
	t.Cleanup(srv.Close)
	registerAll(srv)
	a := newDevice(t, srv, "alice", deviceOpts{plain: true})
	a.save(me, "shipments", "s1", `{"b":2,"a":1}`)
	a.sync(me)
	raw := srv.RawDoc(synctest.PersonalKey("alice"), "shipments", "s1")
	if !strings.Contains(string(raw.Payload), `"a":1`) {
		t.Fatalf("plain payload %s", raw.Payload)
	}
	id, err := a.engine.UploadBlobBytes(a.ctx(), me, []byte("hello"), "text/plain")
	if err != nil || id != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("plain blob id %s err %v", id, err)
	}
	p, err := a.engine.BlobPath(a.ctx(), me, id)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hello" {
		t.Fatal("plain blob content")
	}
	// 服务端按 jsonb 规则重排了键, 拉取方拿到语义相同的 JSON。
	b := newDevice(t, srv, "alice", deviceOpts{plain: true, token: tokenOf(t, srv, "alice")})
	b.sync(me)
	if got := b.app.get("shipments", "s1"); got == nil || !strings.Contains(got.Value, `"a":1`) {
		t.Fatalf("plain pull %+v", got)
	}
}

func TestRetriesAndErrors(t *testing.T) {
	srv := newE2EServer(t)
	d := newDevice(t, srv, "alice", deviceOpts{})
	srv.FailNext(2, 503, 0)
	if _, err := d.client.Spaces(d.ctx()); err != nil {
		t.Fatalf("503 not retried: %v", err)
	}
	start := time.Now()
	srv.FailNext(1, 429, 1)
	if _, err := d.client.Spaces(d.ctx()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatal("Retry-After not respected")
	}
	srv.FailNext(5, 500, 0)
	_, err := d.client.Spaces(d.ctx())
	if !errors.Is(err, syncsdk.ErrServer) || statusOf(err) != 500 {
		t.Fatalf("exhausted retries: %v", err)
	}
	if _, err := d.client.Changes(d.ctx(), "org_missing", 0, 0); !errors.Is(err, syncsdk.ErrForbidden) {
		t.Fatalf("not a member: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.client.Spaces(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// refreshingTokens 模拟 OAuth2 刷新: 第一次给过期令牌, 被告知失效后给新令牌。
type refreshingTokens struct {
	stale, fresh string
	invalid      bool
}

func (r *refreshingTokens) Token(context.Context) (string, error) {
	if r.invalid {
		return r.fresh, nil
	}
	return r.stale, nil
}
func (r *refreshingTokens) InvalidateToken(string) { r.invalid = true }

func TestUnauthorizedRefreshesToken(t *testing.T) {
	srv := newE2EServer(t)
	stale := srv.AddUser("alice")
	fresh := srv.AddUser("alice")
	srv.RevokeToken(stale)
	ts := &refreshingTokens{stale: stale, fresh: fresh}
	c, _ := syncsdk.NewClient(syncsdk.ClientOptions{BaseURL: srv.BaseURL(), Tokens: ts, Retry: fastRetry()})
	if _, err := c.Spaces(context.Background()); err != nil {
		t.Fatalf("token not refreshed: %v", err)
	}
	c2, _ := syncsdk.NewClient(syncsdk.ClientOptions{BaseURL: srv.BaseURL(), Retry: fastRetry(),
		Tokens: syncsdk.TokenFunc(func(context.Context) (string, error) { return stale, nil })})
	if _, err := c2.Spaces(context.Background()); !errors.Is(err, syncsdk.ErrUnauthorized) {
		t.Fatalf("revoked token: %v", err)
	}
}

func TestNewIDOrdered(t *testing.T) {
	a := syncsdk.NewID()
	time.Sleep(2 * time.Millisecond)
	b := syncsdk.NewID()
	if len(a) != 26 || a >= b {
		t.Fatalf("ids %s %s", a, b)
	}
}
