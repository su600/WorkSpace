# WorkSpace

OpenClaw 工作目录 Web 文件浏览器 — 用 Go 重写，更快、更可靠。

## 功能

- 📁 文件目录浏览，支持按名称 / 大小 / 修改时间排序
- 📝 Markdown 渲染（基于 [goldmark](https://github.com/yuin/goldmark)，支持 GFM 表格、任务列表、脚注等）
- ✏️ Markdown 文件在线编辑（浏览器内直接修改并保存）
- 👁️ 文件预览：PDF 内嵌浏览、图片缩放查看、文本/代码带行号高亮
- 🔍 全文搜索（支持文件名与目录名实时检索，最多返回 500 条结果）
- 📌 文件 / 目录收藏（置顶常用路径，持久化保存）
- 📥 文件下载
- 🔒 自定义登录页面，基于安全会话 Cookie 认证（默认 24 小时，勾选"记住我"延长至 30 天）
- 📱 响应式布局，完美适配手机访问
- 🌐 PWA 支持，可添加到桌面作为独立应用使用
- 🚀 单二进制文件部署，无外部依赖

## 快速开始

```bash
# 编译
go build -o workspace-portal .

# 运行（使用默认配置）
./workspace-portal

# 自定义配置（通过环境变量）
PORTAL_DIR=/your/directory \
PORTAL_PORT=3000 \
PORTAL_USER=admin \
PORTAL_PASS=yourpassword \
./workspace-portal
```

## Docker 部署

```bash
# 构建镜像
docker build -t workspace-portal .

# 运行容器（将宿主机目录挂载到 /workspace）
docker run -d \
  -p 3000:3000 \
  -v /your/directory:/workspace \
  -e PORTAL_USER=admin \
  -e PORTAL_PASS=yourpassword \
  workspace-portal
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORTAL_DIR` | `/root/.openclaw/workspace` | 挂载的工作目录路径 |
| `PORTAL_PORT` | `3000` | 监听端口 |
| `PORTAL_USER` | `su600` | 登录用户名（**生产环境请务必修改**） |
| `PORTAL_PASS` | `password123` | 登录密码（**生产环境请务必修改**） |
| `PORTAL_TLS` | `false` | 设为 `true` 启用 Cookie Secure 属性（部署在 HTTPS 后端时使用） |

> ⚠️ **安全提示**：`PORTAL_USER` 和 `PORTAL_PASS` 均有内置默认值，任何知道默认值的人都可以登录。**部署到公网或团队环境前，请务必通过环境变量设置强密码。**

## 截图

**桌面端**
![桌面端](https://github.com/user-attachments/assets/72670495-2c7f-4233-80ae-45c69ca8886a)

**Markdown 渲染**
![Markdown渲染](https://github.com/user-attachments/assets/7da1c005-bc05-4d6c-a701-e2a028bf2e7c)

**移动端**
![移动端](https://github.com/user-attachments/assets/a6a600a4-53c1-4484-a2e7-efb498dae6f6)

## 技术栈

- **语言**: Go 1.24+
- **Markdown**: [yuin/goldmark](https://github.com/yuin/goldmark)（GitHub Flavored Markdown）
- **前端**: 纯 HTML + CSS，无 CDN 依赖，内联样式极速加载
- **PWA**: 内嵌 Service Worker + Web App Manifest，支持离线访问 App Shell
