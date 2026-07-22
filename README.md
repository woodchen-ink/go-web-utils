<div align="center">

# 🔧 go-web-utils

[![Go Reference](https://pkg.go.dev/badge/github.com/woodchen-ink/go-web-utils.svg)](https://pkg.go.dev/github.com/woodchen-ink/go-web-utils)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/woodchen-ink/go-web-utils)](https://goreportcard.com/report/github.com/woodchen-ink/go-web-utils)

**一个轻量级、高性能的 Go Web 实用工具库，专注于解决 Web 开发中的常见需求。**

</div>

---

## 🌍 语言 / Language

| [中文](README.md) | [English](README_EN.md) |


---

## 📖 文档

完整文档请访问：https://go-web-utils.czl.net/

## ✨ 特性

- 🚀 **高性能**: 零依赖，纯标准库实现
- 🛡️ **生产就绪**: 完整的测试覆盖和文档

## 📦 安装

```bash
go get github.com/woodchen-ink/go-web-utils
```

## 🔒 安全提示

`iputil.GetClientIP` 读取的所有代理/CDN 请求头（`CF-Connecting-IP`、`X-Forwarded-For` 等）都可以被直连客户端伪造。结果用于日志、展示是安全的；用于鉴权、限流、封禁等安全决策时，服务必须部署在可信代理/CDN 之后，且代理层会覆盖（而非透传）这些请求头。

## 🔗 相关链接

- [💻 GitHub 仓库](https://github.com/woodchen-ink/go-web-utils) - 源码和问题反馈
- [📦 pkg.go.dev](https://pkg.go.dev/github.com/woodchen-ink/go-web-utils) - 官方包文档
- [🐛 问题反馈](https://github.com/woodchen-ink/go-web-utils/issues) - Bug 报告和功能请求

## 🤝 贡献

欢迎提交 Issues 和 Pull Requests！

## 📄 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件。
