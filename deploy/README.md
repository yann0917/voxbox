# 服务器部署指南

单二进制部署（前端已 embed，纯 Go SQLite 驱动无外部依赖）。TLS 交给 Caddy，进程交给 systemd。

## 1. 构建与上传

```bash
make dist                                     # 产出 dist/voxbox-linux-*
scp dist/voxbox-linux-amd64 server:/tmp/     # arm64 机器换对应产物
```

## 2. 服务器侧安装

```bash
ssh server
sudo useradd -r -m -d /var/lib/voxbox voxbox          # 专用低权账号
sudo install /tmp/voxbox-linux-amd64 /usr/local/bin/voxbox
sudo -u voxbox /usr/local/bin/voxbox config set server.host 127.0.0.1
```

首次启动会创建管理员账号并在控制台打印一次随机密码（登录后强制改密）：

```bash
sudo -u voxbox /usr/local/bin/voxbox serve   # 记下初始密码，Ctrl+C 退出
```

## 3. 常驻与反代

```bash
sudo cp deploy/voxbox.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now voxbox
sudo apt install caddy && sudo cp deploy/Caddyfile /etc/caddy/   # 域名改好后 reload
```

## 4. 部署自动化（可选）

systemd 单元里预置 `Environment=VOXBOX_ADMIN_PASSWORD=...` 再首启，可跳过人工抄密码
（账号已存在时该变量无效）。

## 5. MCP 接入（远程）

登录 Web → 设置 → API Token → 生成，客户端配置：

```json
{
  "mcpServers": {
    "voxbox": {
      "url": "https://example.com/api/mcp",
      "headers": { "Authorization": "Bearer tbx_xxxxxxxx..." }
    }
  }
}
```

## 说明

- 数据/配置都在 `VOXBOX_HOME`（默认 `/var/lib/voxbox`：config.yaml 与 data/），
  备份即打包此目录。
- 默认 `server.host=127.0.0.1`，公网入口只有反代；确要直连时改 `0.0.0.0`
  并自行承担 TLS（强烈不建议）。
- 注册默认关闭（`auth.allow_registration`，P2 实装前仅 admin 经 DB/CLI 建号）。
