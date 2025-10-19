# uautil - User-Agent 工具包

专为 Web 应用设计的 User-Agent 检测和机器人过滤工具包。

## 功能特性

- ✅ 自动识别常见恶意机器人和爬虫
- ✅ 支持合法搜索引擎白名单（Google、Bing 等）
- ✅ 提供 HTTP 中间件支持
- ✅ 支持自定义机器人特征
- ✅ 零依赖，纯标准库实现
- ✅ 高性能，适用于高并发场景

## 快速开始

```go
import "github.com/woodchen-ink/go-web-utils/uautil"

func handler(w http.ResponseWriter, r *http.Request) {
    // 检测是否为机器人（允许搜索引擎）
    if uautil.IsBot(r, true) {
        http.Error(w, "Bot access denied", http.StatusForbidden)
        return
    }

    w.Write([]byte("Welcome!"))
}
```

## 使用中间件

```go
func main() {
    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("Success"))
    })

    // 应用机器人拦截中间件（允许搜索引擎）
    middleware := uautil.BlockBotMiddleware(true, "Access denied")
    protectedHandler := middleware(handler)

    http.ListenAndServe(":8080", protectedHandler)
}
```

## 主要函数

### IsBot
检测 HTTP 请求是否来自机器人。

```go
func IsBot(r *http.Request, allowLegitimate bool) bool
```

### IsBotUserAgent
直接检测 User-Agent 字符串。

```go
func IsBotUserAgent(userAgent string, allowLegitimate bool) bool
```

### BlockBotMiddleware
创建 HTTP 中间件拦截机器人。

```go
func BlockBotMiddleware(allowLegitimate bool, customMessage ...string) func(http.Handler) http.Handler
```

### AddCustomBotPattern
添加自定义机器人特征。

```go
func AddCustomBotPattern(pattern string) func()
```

### AddLegitimateBot
添加自定义合法爬虫。

```go
func AddLegitimateBot(pattern string) func()
```

## 内置识别特征

### 恶意机器人/工具
- `python-requests`, `python-urllib`
- `curl`, `wget`
- `scrapy`, `selenium`, `phantomjs`
- `nmap`, `sqlmap`, `nikto` 等扫描器
- 其他常见爬虫工具

### 合法搜索引擎
- Google (`googlebot`)
- Bing (`bingbot`)
- Baidu (`baiduspider`)
- Yahoo (`slurp`)
- DuckDuckGo (`duckduckbot`)
- 社交媒体爬虫（Facebook、Twitter、LinkedIn 等）

## 使用场景

1. **API 保护** - 防止恶意爬虫消耗资源
2. **内容保护** - 拦截自动化爬虫工具
3. **SEO 优化** - 允许搜索引擎，拦截恶意机器人
4. **流量控制** - 减少无效流量
5. **安全防护** - 拦截扫描器和漏洞探测工具

## 测试

```bash
go test github.com/woodchen-ink/go-web-utils/uautil -v
```

## 完整文档

https://go-web-utils.czl.net/docs/uautil

## 许可证

MIT License
