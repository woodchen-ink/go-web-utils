package syncsdk_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/go-web-utils/syncsdk"
	"github.com/woodchen-ink/go-web-utils/syncsdk/synctest"
)

// localDoc 是测试应用本地库里的一行。
type localDoc struct {
	Rev       int64
	Value     string
	Deleted   bool
	UpdatedBy string
	Err       error
}

// app 是测试用的接入方: 本地库 + Applier 实现。
type app struct {
	mu      sync.Mutex
	docs    map[string]*localDoc // collection/id
	resets  int
	applied chan struct{}
}

func newApp() *app { return &app{docs: map[string]*localDoc{}, applied: make(chan struct{}, 100)} }

func key(c, id string) string { return c + "/" + id }

func (a *app) Apply(_ context.Context, _ string, changes []syncsdk.Change) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range changes {
		cur := a.docs[key(c.Collection, c.ID)]
		if c.Pending || (cur != nil && cur.Rev >= c.Rev) {
			continue
		}
		a.docs[key(c.Collection, c.ID)] = &localDoc{Rev: c.Rev, Value: string(c.Value), Deleted: c.Deleted, UpdatedBy: c.UpdatedBy, Err: c.Err}
	}
	select {
	case a.applied <- struct{}{}:
	default:
	}
	return nil
}

func (a *app) Committed(_ context.Context, c syncsdk.Commit) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.docs[key(c.Collection, c.ID)]
	if d == nil {
		d = &localDoc{}
		a.docs[key(c.Collection, c.ID)] = d
	}
	d.Rev, d.Deleted, d.UpdatedBy = c.Rev, c.Deleted, c.UpdatedBy
	return nil
}

func (a *app) ResetSpace(context.Context, string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resets++
	a.docs = map[string]*localDoc{}
	return nil
}

func (a *app) get(c, id string) *localDoc {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.docs[key(c, id)]; d != nil {
		cp := *d
		return &cp
	}
	return nil
}

func (a *app) ids(c string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for k, d := range a.docs {
		if len(k) > len(c) && k[:len(c)+1] == c+"/" && !d.Deleted {
			out = append(out, k[len(c)+1:])
		}
	}
	sort.Strings(out)
	return out
}

// device 是一台装了 SDK 的设备。
type device struct {
	t        *testing.T
	user     string
	client   *syncsdk.Client
	keys     *syncsdk.KeyManager
	engine   *syncsdk.Engine
	outbox   *syncsdk.MemoryOutbox
	keystore *syncsdk.MemoryKeyStore
	app      *app
	resolve  func(syncsdk.Conflict) syncsdk.Resolution
	rejected []syncsdk.OutboxItem
	grants   []syncsdk.PendingMember
	presence []syncsdk.Presence
	mu       sync.Mutex
}

type deviceOpts struct {
	plain      bool
	token      string
	cacheTo    string
	deviceID   string
	deviceName string
}

func fastRetry() syncsdk.RetryPolicy {
	return syncsdk.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, MaxRetryAfter: 3 * time.Second}
}

func newDevice(t *testing.T, srv *synctest.Server, user string, o deviceOpts) *device {
	t.Helper()
	tok := o.token
	if tok == "" {
		tok = srv.AddUser(user)
	}
	c, err := syncsdk.NewClient(syncsdk.ClientOptions{
		BaseURL:    srv.BaseURL(),
		Tokens:     syncsdk.TokenFunc(func(context.Context) (string, error) { return tok, nil }),
		Retry:      fastRetry(),
		DeviceID:   o.deviceID,
		DeviceName: o.deviceName,
	})
	if err != nil {
		t.Fatal(err)
	}
	d := &device{t: t, user: user, client: c, outbox: syncsdk.NewMemoryOutbox(), keystore: syncsdk.NewMemoryKeyStore(), app: newApp()}
	d.resolve = func(syncsdk.Conflict) syncsdk.Resolution { return syncsdk.Resolution{Kind: syncsdk.KeepRemote} }
	if !o.plain {
		d.keys, err = syncsdk.NewKeyManager(c, d.keystore, syncsdk.KeyManagerOptions{
			Namespace:    "test/" + user,
			PollInterval: 5 * time.Millisecond,
			ApproveGrant: func(_ context.Context, m syncsdk.PendingMember) bool {
				d.mu.Lock()
				d.grants = append(d.grants, m)
				d.mu.Unlock()
				return true
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	cache := o.cacheTo
	if cache == "" {
		cache = t.TempDir()
	}
	d.engine, err = syncsdk.NewEngine(syncsdk.EngineOptions{
		Client: c, Keys: d.keys, Outbox: d.outbox, Cursors: syncsdk.NewMemoryCursorStore(), Applier: d.app,
		Resolver: func(_ context.Context, cf syncsdk.Conflict) (syncsdk.Resolution, error) {
			return d.resolve(cf), nil
		},
		AppendOnly: []string{"activity"},
		OnRejected: func(_ context.Context, it syncsdk.OutboxItem, _ error) bool {
			d.mu.Lock()
			d.rejected = append(d.rejected, it)
			d.mu.Unlock()
			return true
		},
		OnPresence: func(_ string, p []syncsdk.Presence) {
			d.mu.Lock()
			d.presence = p
			d.mu.Unlock()
		},
		BlobCacheDir: cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (d *device) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	d.t.Cleanup(cancel)
	return ctx
}

func (d *device) enroll() string {
	d.t.Helper()
	code, err := d.keys.Enroll(d.ctx())
	if err != nil {
		d.t.Fatalf("%s enroll: %v", d.user, err)
	}
	return code.String()
}

func (d *device) save(space, coll, id, value string) {
	d.t.Helper()
	var base int64
	if cur := d.app.get(coll, id); cur != nil {
		base = cur.Rev
	}
	if err := d.engine.Save(d.ctx(), space, coll, id, base, json.RawMessage(value)); err != nil {
		d.t.Fatal(err)
	}
	d.app.mu.Lock()
	if cur := d.app.docs[key(coll, id)]; cur != nil {
		cur.Value = value
	} else {
		d.app.docs[key(coll, id)] = &localDoc{Value: value}
	}
	d.app.mu.Unlock()
}

func (d *device) sync(space string) syncsdk.SyncResult {
	d.t.Helper()
	res, err := d.engine.Sync(d.ctx(), space)
	if err != nil {
		d.t.Fatalf("%s sync %s: %v", d.user, space, err)
	}
	return res
}

func registerAll(srv *synctest.Server) {
	srv.RegisterCollection("shipments", synctest.Collection{Revisions: 3})
	srv.RegisterCollection("contacts", synctest.Collection{})
	srv.RegisterCollection("settings", synctest.Collection{MinRole: syncsdk.RoleAdmin})
	srv.RegisterCollection("activity", synctest.Collection{AppendOnly: true})
}

func newE2EServer(t *testing.T) *synctest.Server {
	srv := synctest.New(synctest.Options{E2E: syncsdk.E2ERequired})
	t.Cleanup(srv.Close)
	registerAll(srv)
	return srv
}

func statusOf(err error) int {
	if ae, ok := err.(*syncsdk.APIError); ok {
		return ae.Status
	}
	return 0
}

var _ = http.StatusOK
