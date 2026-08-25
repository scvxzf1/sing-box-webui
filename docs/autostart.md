# 开机自启动

源码开发模式可以通过当前用户的 systemd 服务开机启动。服务以普通用户运行，
只使用 `CAP_NET_ADMIN` 让 sing-box 核心执行 TUN；不会以 root 启动 Go、Vite 或 WebUI。

## 安装并启用

先确保已经完成一次前端依赖安装，并且托管核心已经下载：

```bash
npm --prefix web install
./scripts/dev.sh
```

停止手动启动的开发服务后，运行安装脚本：

```bash
./scripts/install-autostart.sh
```

安装脚本会把当前仓库的绝对路径、Node/npm/Go 路径写入
`~/.config/systemd/user/sing-box-webui-dev.service`，随后执行
`systemctl --user enable --now` 等价的启用和启动操作。当前用户需要启用 user manager
常驻；如果 `loginctl show-user "$USER" -p Linger` 不是 `Linger=yes`，执行：

如果开发服务当前已经在另一个终端运行，为避免启动第二份实例，可只安装并启用单元，
不立即启动：

```bash
./scripts/install-autostart.sh --no-start
```

```bash
sudo loginctl enable-linger "$USER"
```

核心缺少能力时，安装脚本会停止并给出精确的授权命令。只需对解析后的核心版本文件授权一次：

```bash
sudo setcap cap_net_admin+ep "$(readlink -f var/data/core/sing-box)"
```

## 管理服务

```bash
systemctl --user status sing-box-webui-dev.service
journalctl --user -u sing-box-webui-dev.service -f
systemctl --user restart sing-box-webui-dev.service
systemctl --user disable --now sing-box-webui-dev.service
```

默认端口仍为 WebUI `127.0.0.1:33333` 和 API `127.0.0.1:33334`。Node 版本或仓库路径变化后，重新运行安装脚本即可刷新单元中的环境和路径。
