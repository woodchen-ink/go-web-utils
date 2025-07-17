# go-web-utils 文档

基于 [Fumadocs](https://fumadocs.vercel.app/) 构建的现代化文档站点，使用 Next.js + shadcn/ui。

## 🚀 本地开发

### 安装依赖

```bash
npm install
```

### 启动开发服务器

```bash
npm run dev
```

访问 [http://localhost:3000](http://localhost:3000) 查看文档。

### 构建生产版本

```bash
npm run build
```

构建输出将在 `out` 目录中。

## 📁 项目结构

```
docs/
├── app/                    # Next.js App Router
├── content/
│   └── docs/              # 文档内容 (MDX)
│       ├── index.mdx      # 首页
│       ├── iputil.mdx     # IP 工具文档
│       ├── deployment.mdx # 部署指南
│       └── meta.json      # 导航配置
├── lib/                   # 工具库
└── out/                   # 构建输出 (静态文件)
```

## ✏️ 编辑文档

### 添加新页面

1. 在 `content/docs/` 中创建新的 `.mdx` 文件
2. 更新 `content/docs/meta.json` 添加导航项

### MDX 示例

```mdx
---
title: 页面标题
description: 页面描述
---

# 页面标题

页面内容...

## 代码示例

```go
package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}
```
```

## 🎨 特性

- ✨ **现代化 UI**: 基于 shadcn/ui 组件
- 🌙 **暗黑模式**: 自动切换支持
- 📱 **响应式**: 完美适配各种设备
- ⚡ **极速**: 静态生成，全球 CDN
- 🔍 **语法高亮**: 支持多种编程语言
- 📊 **SEO 优化**: 自动生成 meta 标签

## 🚀 部署到 Cloudflare Pages

### 自动部署

1. 将此仓库连接到 Cloudflare Pages
2. 设置构建配置：
   - **框架**: Next.js
   - **构建命令**: `npm run build`
   - **输出目录**: `out`
   - **根目录**: `docs`
3. 点击部署

### 环境变量

```
NODE_VERSION = 18
```

## 📝 许可证

MIT License - 查看 [LICENSE](../LICENSE) 文件获取详细信息。
