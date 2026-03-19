# WorkSpace

OpenClaw 工作目录 Web 文件浏览器 — 用 Go 重写，更快、更可靠。

## 功能

- 📁 文件目录浏览，支持按名称 / 大小 / 修改时间排序
- 📝 Markdown 渲染（基于 [goldmark](https://github.com/yuin/goldmark)，支持 GFM 表格、任务列表、脚注等）
- 📥 文件下载
- 🔒 HTTP Basic 认证
- 📱 响应式布局，完美适配手机访问
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

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORTAL_DIR` | `/root/.openclaw/workspace` | 挂载的工作目录路径 |
| `PORTAL_PORT` | `3000` | 监听端口 |
| `PORTAL_USER` | `su600` | Basic Auth 用户名 |
| `PORTAL_PASS` | `password123` | Basic Auth 密码 |

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
