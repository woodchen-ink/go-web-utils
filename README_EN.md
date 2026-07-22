<div align="center">

# 🔧 go-web-utils

[![Go Reference](https://pkg.go.dev/badge/github.com/woodchen-ink/go-web-utils.svg)](https://pkg.go.dev/github.com/woodchen-ink/go-web-utils)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/woodchen-ink/go-web-utils)](https://goreportcard.com/report/github.com/woodchen-ink/go-web-utils)

**A lightweight, high-performance Go web utility library focused on solving common needs in web development.**

</div>

---

## 🌍 语言 / Language

| [中文](README.md) | [English](README_EN.md) |


---

## 📖 Documentation

For complete documentation, visit: https://go-web-utils.czl.net/

## ✨ Features

- 🚀 **High Performance**: Zero dependencies, pure standard library implementation
- 🛡️ **Production Ready**: Complete test coverage and documentation

### Packages

| Package | Description |
|---------|-------------|
| `iputil` | Real client IP extraction (major CDNs/proxies), trusted proxy mode, IP validation and CIDR matching |
| `uautil` | Bot/crawler detection and blocking, browser detection, HTTP middleware |
| `resputil` | Unified JSON response `{ code, data, msg }`, empty data never serialized as null |
| `timex` | Explicit business timezone initialization, day/week/month boundaries, RFC3339 formatting |
| `cookieutil` | Auth cookies with safe defaults (HttpOnly enforced), CSRF double-submit tokens |
| `cronutil` | Idle exponential backoff gate, non-overlapping tick guard, debounced flush queue |
| `nextstatic` | Next.js static export hosting: RSC headers, dynamic route fallback, SEO injection |

## 📦 Installation

```bash
go get github.com/woodchen-ink/go-web-utils
```

## 🔒 Security Notice

All proxy/CDN headers read by `iputil.GetClientIP` (`CF-Connecting-IP`, `X-Forwarded-For`, etc.) can be forged by directly-connected clients. The result is safe for logging and display; for security decisions (authentication, rate limiting, banning), the service MUST sit behind a trusted proxy/CDN that overwrites — not passes through — these headers.

## 🔗 Links

- [💻 GitHub Repository](https://github.com/woodchen-ink/go-web-utils) - Source code and issue feedback
- [📦 pkg.go.dev](https://pkg.go.dev/github.com/woodchen-ink/go-web-utils) - Official package documentation
- [🐛 Issue Tracker](https://github.com/woodchen-ink/go-web-utils/issues) - Bug reports and feature requests

## 🤝 Contributing

Issues and Pull Requests are welcome!

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details. 