# Web 鉴权配置与运维

本文说明 sing-box WebUI 的本机 Web 鉴权模型、配置格式、启停流程、安全边界和
常见故障处理。鉴权默认开启，保护除健康检查外的全部 API、SSE 事件流和控制操作。

## 1. 配置文件

默认配置文件位于项目根目录的 `var/config.json`。首次启动时后端会自动创建该文件、
生成随机 token，并把文件权限设置为 `0600`：

```json
{
  "web": {
    "enabled": true,
    "token": "replace-with-a-private-token"
  }
}
```

字段说明：

- `web.enabled`：是否启用 Web 鉴权。省略时默认为 `true`；只有显式设为 `false` 才关闭。
- `web.token`：登录使用的访问令牌，鉴权开启时至少 8 个字符。首次启动自动生成高熵值。
- `SING_BOX_WEBUI_CONFIG`：可选环境变量，用于指定另一个配置文件路径；它只保存路径，
  不应直接承载 token。

`var/` 已被 Git 忽略，运行时 token 不会进入仓库。不要把真实 token 写入 README、提交、
截图、日志或问题报告。

## 2. 默认开启与禁用

默认配置和省略 `enabled` 都会开启鉴权：

```json
{
  "web": {
    "token": "replace-with-a-private-token"
  }
}
```

需要在可信的本机环境中临时关闭时，停止服务并修改为：

```json
{
  "web": {
    "enabled": false,
    "token": "replace-with-a-private-token"
  }
}
```

重启后生效。关闭时可以保留 token，之后把 `enabled` 改回 `true` 即可恢复，无需重新
分发 token。即使 API 仅监听回环地址，关闭鉴权仍会扩大同机恶意程序和浏览器扩展的
攻击面，不建议作为长期配置。

## 3. 修改和轮换 token

1. 停止 `./scripts/dev.sh` 或 `./scripts/dev-tun.sh`。
2. 修改 `var/config.json` 中的 `web.token`。
3. 确认文件权限：`stat -c '%a %n' var/config.json`，期望为 `600`。
4. 重新启动服务并使用新 token 登录。

服务重启会生成新的会话签名密钥，因此旧浏览器会话自动失效。修改 token 后，旧 token
也不能再创建会话。

## 4. 浏览器登录流程

1. 前端启动后请求 `GET /api/v1/session` 检查会话。
2. 未登录时后端返回 `401 authentication_required`，前端显示登录页。
3. 登录页将 token 通过 `POST /api/v1/auth/login` 提交到同源本机 API。
4. 后端进行常量时间比较，成功后签发 24 小时会话 Cookie。
5. 前端重新获取 CSRF token，随后加载应用与 SSE 事件流。
6. 点击顶栏退出按钮会调用 `POST /api/v1/auth/logout` 并清除 Cookie。

访问 token 不写入 `localStorage` 或 `sessionStorage`。会话 Cookie 使用 `HttpOnly` 和
`SameSite=Strict`，前端 JavaScript 无法读取其内容。

## 5. API 安全边界

以下端点无需登录：

- `GET /healthz`：只返回最小健康状态，不包含版本、路径或运行配置。
- `POST /api/v1/auth/login`：登录入口，但仍要求合法 Host、同源 Origin，并拒绝跨站请求。

其余 `/api/v1/*` 端点统一要求有效会话。所有修改状态的请求还必须携带
`X-CSRF-Token`，并继续接受 Host、Origin、`Sec-Fetch-Site` 和请求方法检查。SSE 使用
同源 Cookie 鉴权，不在 URL 查询参数中暴露 token。

后端仍只允许监听显式回环地址。Web 鉴权是回环监听之外的第二层保护，不能替代操作
系统用户隔离、配置文件权限或 TUN 的最小权限边界。

## 6. 常见故障

### 页面持续显示登录界面

- 确认 Go API 已启动：`curl -fsS http://127.0.0.1:11872/healthz`。
- 检查 `var/config.json` 是合法 JSON，且开启时 token 至少 8 个字符。
- 修改配置后必须重启后端；Vite 热更新不会重新加载 Go 配置。
- 服务重启后旧 Cookie 必然失效，需要重新登录。

### 正确 token 仍返回 401

- 确认输入没有额外空格；配置加载时会裁剪 token 首尾空白。
- 确认当前进程使用的配置路径。设置了 `SING_BOX_WEBUI_CONFIG` 时，默认文件不会生效。
- 检查是否启动了另一个监听相同前端但不同 API 地址的开发进程。

### 修改操作返回 403

登录成功但写请求返回 `403` 通常表示 Origin 或 CSRF 校验失败。应通过项目 Web 前端访问，
不要从其他站点、任意脚本环境或不同端口直接发起浏览器写请求。

## 7. 验证建议

变更配置或部署方式后至少检查：

```bash
# 健康检查保持公开
curl -i http://127.0.0.1:11872/healthz

# 鉴权开启且无 Cookie 时应返回 401
curl -i http://127.0.0.1:11872/api/v1/session

# 配置文件只允许当前用户读写
stat -c '%a %n' var/config.json
```

项目自动化测试覆盖配置首次生成、默认开启、显式关闭、短 token 拒绝、错误 token、
会话 Cookie 标志以及登录后的 API 访问。
