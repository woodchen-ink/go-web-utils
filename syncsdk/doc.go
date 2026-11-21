/*
Package syncsdk 是 CZL Connect 数据同步 (/api/sync/v1) 的 Go 客户端 SDK, 供桌面端直接引用:
本地出站队列、重试退避、游标、SSE 订阅与回退轮询、冲突回调、附件去重与下载缓存、
只追加集合 (操作记录), 以及端到端加密的完整密钥生命周期。密码学原语在子包 e2e,
测试用假服务端在子包 synctest。

分层:

  - Client: 类型化 HTTP 客户端, 每个接口一个方法; 鉴权 (TokenSource, 401 时经 TokenInvalidator 刷新重试一次)、
    错误归类 (ErrConflict / ErrCursorExpired ...)、瞬时错误指数退避 + 抖动、尊重 Retry-After
  - KeyManager: 开通、恢复码解锁与重置、新设备授权、空间密钥建立 / 补授 / 轮换, 私钥与空间密钥存本机 KeyStore
  - Engine: Save / Delete 入队 → Push (加密、batch、409 冲突回调) → Pull (解密、Applier、410 全量重拉);
    Subscribe 持续同步; UploadBlob / BlobPath 附件; Editing presence 心跳; ActivityLog 只追加记录

应用需实现的接口: Outbox (持久化出站队列, 通常 SQLite)、CursorStore、KeyStore (Windows DPAPI /
macOS 钥匙串)、Applier (把变更落到本地库)、ConflictResolver。内存实现 MemoryOutbox /
MemoryCursorStore / MemoryKeyStore 只用于测试。

接入流程:

	client, _ := syncsdk.NewClient(syncsdk.ClientOptions{
		BaseURL: "https://connect.czl.net/api/sync/v1",
		Tokens:  tokens, // 实现 Token(ctx), 可选 InvalidateToken
		// 本机设备标识, presence 按设备区分 (同账号其他设备可见); 存本机数据目录, 不随配置同步
		DeviceID: deviceID, // syncsdk.LoadOrCreateDeviceID(filepath.Join(dataDir, "device-id"))
	})
	keys, _ := syncsdk.NewKeyManager(client, dpapiStore, syncsdk.KeyManagerOptions{
		Namespace: "tradedocs/" + userID,
	})

	switch st, _ := keys.Status(ctx); st {
	case syncsdk.KeyStatusNotEnrolled:
		code, _ := keys.Enroll(ctx) // 展示 code.String() 一次, 要求用户确认已保存
	case syncsdk.KeyStatusLocked:
		// 二选一: 输入恢复码
		_ = keys.UnlockWithRecoveryCode(ctx, typed)
		// 或由已登录设备批准: 两端显示同一个 6 位码
		enr, _ := keys.StartDeviceEnrollment(ctx, "Windows · 前台电脑")
		code, _ := enr.WaitCode(ctx) // 显示 code
		_ = enr.WaitApproval(ctx)
	}

	engine, _ := syncsdk.NewEngine(syncsdk.EngineOptions{
		Client: client, Keys: keys,
		Outbox: sqliteOutbox, Cursors: sqliteCursors, Applier: localDB,
		Resolver: func(ctx context.Context, c syncsdk.Conflict) (syncsdk.Resolution, error) {
			// 单证的做法: 服务端版本落地, 本地版本另存为「（冲突副本）」
			id := syncsdk.NewID()
			localDB.InsertCopy(c.Local, id)
			return syncsdk.Resolution{Kind: syncsdk.SaveAsCopy, CopyID: id}, nil
		},
		AppendOnly:   []string{"activity"},
		BlobCacheDir: filepath.Join(cacheDir, "blobs"),
	})

	// 保存时入队 (断网照常), baseRev 为本地行的 rev, 新建为 0
	_ = engine.Save(ctx, "org_xxx", "shipments", id, row.Rev, payload)
	// 后台持续同步, 直到 ctx 结束
	go engine.Subscribe(ctx, "org_xxx", syncsdk.SubscribeOptions{})

在已解锁的设备上批准新设备:

	reqs, _ := keys.PendingDeviceRequests(ctx)
	ap, _ := keys.BeginDeviceApproval(ctx, reqs[0])
	code, _ := ap.WaitCode(ctx) // 显示 code, 用户确认与新设备一致
	_ = ap.Approve(ctx)

约定与边界:

  - 文档内容以明文进出 SDK, 推送时加密、拉取后解密; 服务端只见空间、集合名、文档 id、版本、大小、
    修改人、时间与 presence, 业务信息不得放进集合名或文档 id
  - 展示「谁改的」只认 Change.UpdatedBy / UpdatedAt (服务端按令牌写入), 不认 payload 里的自报身份
  - 空间级拒绝 (非成员、只读、订阅到期) 让 Sync 返回 ErrForbidden, 修改留在队列;
    单条拒绝 (集合无权写、超限) 交给 EngineOptions.OnRejected
  - 成员被移除后, 下一个在线的 owner / admin 同步时自动轮换空间密钥; Engine.Reencrypt 可把旧文档换到新版本
  - 被移除者本机已有的副本无法收回; 用户丢失全部设备且没有恢复码时个人空间不可恢复

Documentation: https://go-web-utils.czl.net/docs/syncsdk
*/
package syncsdk
