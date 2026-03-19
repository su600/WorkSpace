import os
import base64
import datetime
import urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = 3000
DIRECTORY = "/root/.openclaw/workspace"
USERNAME = "su600"
PASSWORD = "password123"

# 💡 严格认证 Token
AUTH_STR = 'Basic ' + base64.b64encode(f"{USERNAME}:{PASSWORD}".encode('utf-8')).decode('ascii')

class CleanPortalServer(BaseHTTPRequestHandler):
    def do_AUTHHEAD(self):
        self.send_response(401)
        self.send_header('WWW-Authenticate', 'Basic realm="RocketWorkspace"')
        self.send_header('Content-type', 'text/html; charset=utf-8')
        self.end_headers()
        self.wfile.write(b'Authorization Required')

    def do_GET(self):
        # 💡 PWA 静态资源
        if self.path == '/manifest.json':
            self.send_response(200)
            self.send_header('Content-type', 'application/json; charset=utf-8')
            self.end_headers()
            with open('/root/.openclaw/workspace/dashboard/manifest_workspace.json', 'rb') as f:
                self.wfile.write(f.read())
            return
            
        if self.path == '/sw.js':
            self.send_response(200)
            self.send_header('Content-type', 'application/javascript; charset=utf-8')
            self.end_headers()
            with open('/root/.openclaw/workspace/dashboard/sw_workspace.js', 'rb') as f:
                self.wfile.write(f.read())
            return

        # 💡 注销路径
        if self.path == '/logout':
            self.send_response(401)
            self.send_header('WWW-Authenticate', 'Basic realm="LoggedOut"')
            self.end_headers()
            self.wfile.write(b'Logged out. Close browser to complete.')
            return

        # 💡 校验认证
        auth_header = self.headers.get('Authorization')
        if auth_header != AUTH_STR:
            self.do_AUTHHEAD()
            return

        parsed_url = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed_url.query)
        rel_path = urllib.parse.unquote(parsed_url.path.strip("/"))
        abs_path = os.path.abspath(os.path.join(DIRECTORY, rel_path))

        if not abs_path.startswith(DIRECTORY) or not os.path.exists(abs_path):
            self.send_error(404, "File not found")
            return

        if 'download' in query:
            self.serve_file(abs_path, as_attachment=True)
            return

        if os.path.isdir(abs_path):
            self.list_directory(abs_path, rel_path)
        elif abs_path.endswith(".md") and 'raw' not in query:
            self.render_markdown(abs_path, rel_path)
        else:
            self.serve_file(abs_path)

    def list_directory(self, path, rel_path):
        items = os.listdir(path)
        
        # 获取排序参数
        parsed_url = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed_url.query)
        sort_by = query.get('sort', ['name'])[0]  # name, mtime, size
        reverse = 'desc' in query
        
        # 排序逻辑
        if sort_by == 'mtime':
            items.sort(key=lambda x: (not os.path.isdir(os.path.join(path, x)), -os.path.getmtime(os.path.join(path, x))))
        elif sort_by == 'size':
            items.sort(key=lambda x: (not os.path.isdir(os.path.join(path, x)), -(os.path.getsize(os.path.join(path, x)) if os.path.isfile(os.path.join(path, x)) else 0)))
        else:
            items.sort(key=lambda x: (not os.path.isdir(os.path.join(path, x)), x.lower()))
        
        if reverse:
            items.reverse()
        
        self.send_response(200)
        self.send_header("Content-type", "text/html; charset=utf-8")
        self.end_headers()
        
        now_str = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')

        # 极速 HTML：极简内联样式，减少 CSS 解析
        html = """<!DOCTYPE html><html><head><title>🚀 Workspace</title><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>
body{margin:0;background:#f8f9fa;font-family:system-ui,-apple-system,sans-serif;color:#333}
header{background:#1a73e8;color:#fff;padding:12px 15px;display:flex;justify-content:space-between;align-items:center;box-shadow:0 1px 3px rgba(0,0,0,.1)}
.brand{font-weight:600;font-size:15px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.logout{color:#fff;background:rgba(255,255,255,.25);text-decoration:none;padding:5px 10px;border-radius:5px;font-size:12px}
.logout:hover{background:rgba(255,255,255,.35)}
.container{max-width:1100px;margin:15px auto;padding:0 10px}
table{width:100%;border-collapse:collapse;background:#fff;border-radius:10px;box-shadow:0 1px 3px rgba(0,0,0,.05)}
th{background:#f1f3f4;padding:10px 8px;text-align:left;font-size:12px;color:#5f6368;font-weight:500}
td{padding:10px 8px;border-bottom:1px solid #f0f0f0;font-size:13px}
tr:hover{background:#fdfdfd}
a{color:#1a73e8;text-decoration:none}
a:hover{text-decoration:underline}
.btn{padding:3px 8px;border:1px solid #dadce0;border-radius:4px;text-decoration:none;color:#3c4043;font-size:11px;display:inline-block}
.btn:hover{background:#f8f9fa}
</style></head><body>
<header><div class="brand">🚀 /""" + rel_path + """</div><a href="/logout" class="logout">退出</a></header>
<div class="container">
<table><thead><tr><th><a href="?sort=name" style="color:inherit">📝 名称</a></th><th><a href="?sort=size" style="color:inherit">📦 大小</a></th><th><a href="?sort=mtime" style="color:inherit">⏰ 修改</a></th><th>操作</th></tr></thead><tbody>"""
        
        if rel_path:
            parent = os.path.dirname(rel_path)
            html += '<tr><td><a href="/' + parent + '">📁 ..</a></td><td>-</td><td>-</td><td>-</td></tr>'

        for name in items:
            full = os.path.join(path, name)
            stat = os.stat(full)
            mtime = datetime.datetime.fromtimestamp(stat.st_mtime).strftime('%m-%d %H:%M')
            is_dir = os.path.isdir(full)
            size = f"{stat.st_size/1024:.1f}K" if not is_dir else "-"
            href = "/" + os.path.join(rel_path, name).replace('\\', '/')
            icon = '📁' if is_dir else '📄'
            btn = "" if is_dir else f'<a href="{href}?download=1" class="btn">📥</a>'
            target = 'target="_blank"' if name.endswith(".md") else ''
            
            html += f'<tr><td>{icon} <a href="{href}" {target}>{name}</a></td><td style="color:#70757a">{size}</td><td style="color:#70757a">{mtime}</td><td>{btn}</td></tr>'
        
        html += "</tbody></table></div></body></html>"
        self.wfile.write(html.encode('utf-8'))

    def render_markdown(self, path, rel_path):
        with open(path, 'r', encoding='utf-8') as f: content = f.read()
        self.send_response(200)
        self.send_header("Content-type", "text/html; charset=utf-8")
        self.end_headers()
        
        # 极速方案：不依赖 CDN，用原生 HTML5 + 简单渲染
        html = """<!DOCTYPE html><html><head><title>""" + rel_path + """</title>
        <meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
        <style>
        body{background:#f5f5f5;margin:0;padding:0;font-family:system-ui,-apple-system,sans-serif;color:#333}
        .container{max-width:920px;margin:0 auto;padding:20px;background:#fff}
        h1{border-bottom:2px solid #1a73e8;padding-bottom:10px;margin:30px 0 20px}
        h2{border-bottom:1px solid #e0e0e0;padding-bottom:8px;margin:25px 0 15px;font-size:1.4em}
        h3{margin:20px 0 10px;font-size:1.1em}
        p{line-height:1.6;margin:12px 0}
        ul,ol{line-height:1.8;margin:12px 0;padding-left:24px}
        li{margin:6px 0}
        code{background:#f1f1f1;padding:2px 6px;border-radius:3px;font-family:monospace;font-size:.9em}
        pre{background:#f5f5f5;padding:12px;border-radius:6px;overflow-x:auto;border-left:4px solid #1a73e8;margin:12px 0}
        pre code{background:none;padding:0;color:#333}
        table{border-collapse:collapse;margin:16px 0;width:100%}
        th,td{border:1px solid #ddd;padding:8px;text-align:left}
        th{background:#f1f1f1}
        blockquote{border-left:4px solid #1a73e8;margin:12px 0;padding:0 12px;color:#666}
        a{color:#1a73e8;text-decoration:none}a:hover{text-decoration:underline}
        </style></head><body><div class="container">"""
        
        # 简单的 Markdown 转 HTML（不依赖 marked.js）
        lines = content.split('\n')
        in_code = False
        in_list = False
        for line in lines:
            if line.startswith('```'):
                if in_code:
                    html += '</pre>'
                    in_code = False
                else:
                    html += '<pre><code>'
                    in_code = True
            elif in_code:
                html += line + '\n'
            elif line.startswith('# '):
                html += '<h1>' + line[2:].strip() + '</h1>'
            elif line.startswith('## '):
                html += '<h2>' + line[3:].strip() + '</h2>'
            elif line.startswith('### '):
                html += '<h3>' + line[4:].strip() + '</h3>'
            elif line.startswith('- ') or line.startswith('* '):
                if not in_list:
                    html += '<ul>'
                    in_list = True
                html += '<li>' + line[2:].strip() + '</li>'
            elif line.startswith('> '):
                html += '<blockquote>' + line[2:].strip() + '</blockquote>'
            elif line.strip() == '':
                if in_list:
                    html += '</ul>'
                    in_list = False
                html += '<p></p>'
            else:
                if in_list:
                    html += '</ul>'
                    in_list = False
                if line.strip():
                    html += '<p>' + line.strip() + '</p>'
        
        if in_list:
            html += '</ul>'
        if in_code:
            html += '</code></pre>'
        
        html += '</div></body></html>'
        self.wfile.write(html.encode('utf-8'))

    def serve_file(self, path, as_attachment=False):
        try:
            with open(path, 'rb') as f: content = f.read()
            self.send_response(200)
            if as_attachment: self.send_header("Content-Disposition", f'attachment; filename="{urllib.parse.quote(os.path.basename(path))}"')
            self.send_header("Content-type", "application/octet-stream")
            self.end_headers()
            self.wfile.write(content)
        except Exception as e: self.send_error(500, str(e))

def run():
    httpd = HTTPServer(('', PORT), CleanPortalServer)
    print(f"Portal running at {PORT}")
    httpd.serve_forever()

if __name__ == "__main__":
    run()
