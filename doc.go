/*
Package go-web-utils 提供了用于 Go Web 项目的实用工具库。

这个库包含了常用的 Web 开发工具，目前提供以下功能：

IP 工具 (iputil 包):
  - 获取客户端真实 IP 地址，支持主流 CDN 与代理场景
  - 可信代理模式 (TrustedProxies)，防请求头伪造，适用于安全决策场景
  - IP 地址验证、私有 IP 判断、CIDR 网段匹配、IPv4/IPv6 区分

User-Agent 工具 (uautil 包):
  - 检测和拦截机器人/爬虫请求，支持合法搜索引擎爬虫白名单
  - 浏览器检测与 HTTP 中间件支持
  - 支持自定义机器人/浏览器特征 (并发安全)

统一响应 (resputil 包):
  - 统一 JSON 响应结构 { code, data, msg }
  - 空数据自动归一化为空对象/空数组，永不返回 null

时区工具 (timex 包):
  - 程序入口显式初始化业务时区，杜绝容器内 UTC 静默偏差
  - 业务时区下的自然日/周/月边界计算与 RFC3339 格式化

Cookie 工具 (cookieutil 包):
  - 带安全默认值的认证 Cookie (强制 HttpOnly、SameSite)
  - CSRF double-submit token 生成与恒定时间校验

定时任务工具 (cronutil 包):
  - IdleBackoff 空轮指数退避闸门
  - TickGuard 单实例防重叠
  - Debounced 去抖聚合队列 (批量写外部系统)

Next.js 静态托管 (nextstatic 包):
  - Next.js export 产物托管，RSC payload 正确的 Content-Type 与 Vary
  - trailing slash 规范化、动态路由占位符回退、SEO 占位符注入钩子

示例用法:

	import "github.com/woodchen-ink/go-web-utils/iputil"
	import "github.com/woodchen-ink/go-web-utils/uautil"

	func handler(w http.ResponseWriter, r *http.Request) {
		// 获取客户端真实IP
		clientIP := iputil.GetClientIP(r)

		// 验证IP是否有效
		if iputil.IsValidIP(clientIP) {
			// 判断是否为私有IP
			if iputil.IsPrivateIP(clientIP) {
				// 处理内网IP
			} else {
				// 处理公网IP
			}
		}

		// 检测是否为机器人（允许合法搜索引擎）
		if uautil.IsBot(r, true) {
			http.Error(w, "Bot access denied", http.StatusForbidden)
			return
		}
	}

更多信息请参见各个子包的文档。

完整文档站: https://go-web-utils.czl.net/
各包文档: https://go-web-utils.czl.net/docs/<包名> (如 /docs/iputil, /docs/nextstatic)
*/
package gowebutils
