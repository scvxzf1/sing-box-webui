# 核心管理与更新

## 运行方式

Linux amd64 构建通过 `go:embed` 携带官方 sing-box `1.13.16` 发布包。首次启动会将
核心释放到开发数据目录，并创建一个稳定的执行入口：

```text
var/data/core/
├── sing-box -> versions/1.13.16/sing-box
├── state.json
└── versions/
    └── 1.13.16/
        ├── sing-box
        └── LICENSE
```

版本文件不会原地覆盖。supervisor、配置检查和节点延迟测试均使用稳定入口；更新只在
代理停止时原子切换入口，已经安装的前一版本作为回滚槽保留。

设置 `SING_BOX_BIN=/absolute/path/to/sing-box` 会切换到外部核心模式。外部模式只验证
并执行指定文件，不允许 WebUI 下载、更新或回滚该文件。

## 更新与回滚

核心页面支持留空版本号更新到官方最新稳定版本，也支持输入 `1.2.3` 格式的明确版本。
更新源固定为 SagerNet/sing-box 的 GitHub Release：

1. 读取非草稿、非预发布的 Release 元数据。
2. 选择当前操作系统和架构的精确资产名称。
3. 限制元数据和资产体积。
4. 使用 Release 资产中的 SHA-256 digest 校验完整下载。
5. 只提取预期目录中的 `sing-box` 和 `LICENSE`。
6. 执行 `sing-box version` 验证候选文件。
7. 安装到独立版本目录并原子切换稳定入口。

摘要不匹配、下载中断、版本输出异常或文件不完整时不会改变当前版本。回滚会交换当前
版本和上一版本，因此可以再次切回。

## API

```text
GET  /api/v1/core
POST /api/v1/core/update      {"version":"1.13.16"}
POST /api/v1/core/rollback
```

更新请求中的 `version` 可以省略，此时解析最新稳定 Release。修改操作使用与其他本地
API 相同的 Origin 和 CSRF 防护。

## 平台边界

当前内嵌资产仅支持 Linux amd64。版本目录和更新流程属于平台无关的 Core Manager
职责；后续 Windows 和其他架构需要提供各自构建资产及原子入口适配，不能执行不匹配
的平台文件。
