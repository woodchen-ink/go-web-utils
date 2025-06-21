# go-web-utils

一个用于 Go Web 项目的实用工具库，提供常用的功能模块。

## 功能特性

- 🌐 **IP 工具** - 获取客户端真实 IP，支持 Cloudflare、代理等场景

## 安装

```bash
go get github.com/woodchen-ink/go-web-utils
```

## 模块说明


### IP 工具

```go
import "github.com/woodchen-ink/go-web-utils/ip"

// 获取客户端真实IP
clientIP := ip.GetClientIP(r)

// 验证IP是否有效
isValid := ip.IsValidIP("192.168.1.1")

// 判断是否为私有IP
isPrivate := ip.IsPrivateIP("192.168.1.1")
```

## 贡献

欢迎提交 Issue 和 Pull Request！

## 许可证

MIT License 