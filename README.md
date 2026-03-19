# WorkSpace

OpenClaw 工作目录文件浏览器，使用 **Go** 重写，提供高性能、可靠的 Web 文件浏览服务。

## 功能特性

- 📁 目录浏览，支持面包屑导航
- 📝 Markdown 文件渲染（支持 GFM、表格、任务列表、代码高亮等）
- 🎨 代码文件语法高亮（基于 Chroma，支持 200+ 语言）
- 🌙 深色 / 浅色主题切换，自动跟随系统偏好
- 📱 响应式布局，完美适配手机访问
- ⬇️ 文件下载支持
- 🚀 单文件二进制部署，无外部依赖

## 快速开始

### 本地运行

```bash
# 编译
go build -o workspace-server .

# 运行（浏览当前目录）
./workspace-server

# 运行（指定目录和端口）
./workspace-server -root /path/to/dir -port 8080
```

### 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `ROOT_DIR` | 要浏览的根目录 | 当前工作目录 |

### 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-root` | 要浏览的根目录 | 当前工作目录 |
| `-port` | 监听端口 | `8080` |

### Docker 部署

```bash
# 构建镜像
docker build -t workspace-server .

# 运行（挂载目录）
docker run -d \
  -p 8080:8080 \
  -v /your/openclaw/workdir:/data:ro \
  workspace-server
```

## 技术栈

- **语言**: Go 1.24+
- **Markdown 渲染**: [goldmark](https://github.com/yuin/goldmark)（支持 GitHub Flavored Markdown）
- **语法高亮**: [chroma](https://github.com/alecthomas/chroma)（通过 goldmark-highlighting）
- **前端**: 纯 HTML/CSS/JS，无框架依赖，内嵌于二进制文件中
