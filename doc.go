/*
Package go-web-utils 提供了用于 Go Web 项目的实用工具库。

这个库包含了常用的 Web 开发工具，目前提供以下功能：

IP 工具 (iputil 包):
  - 获取客户端真实 IP 地址，支持 Cloudflare、代理等场景
  - IP 地址验证和格式检查
  - 私有 IP 地址判断

示例用法:

	import "github.com/woodchen-ink/go-web-utils/iputil"

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
	}

更多信息请参见各个子包的文档。
*/
package main
