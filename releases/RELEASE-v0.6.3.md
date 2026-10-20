## v0.6.3 · 2026-10-20 · 模型级限频与管理台整理

### 下载

下载对应系统的二进制，并用同版本 `SHA256SUMS.txt` 校验：

| 系统 | 文件 |
|---|---|
| Windows x64 | `codebuddy-proxy-windows-x64.exe` |
| Linux x64 | `codebuddy-proxy-linux-amd64` |
| Linux ARM64 | `codebuddy-proxy-linux-arm64` |
| Linux ARMv7 | `codebuddy-proxy-linux-armv7` |
| Linux ARMv6 | `codebuddy-proxy-linux-armv6` |
| macOS Apple 芯片 | `codebuddy-proxy-darwin-arm64` |
| macOS Intel | `codebuddy-proxy-darwin-amd64` |

### 改动

- 上游 6004 / `rate-model` 限频按模型记录，不再把一个模型的限频误当成整账号冷却；当前模型不可用时，普通会话可选择其他账号。固定账号 Key 不会换号。
- 管理台账号池状态显示已启用数量和无整号阻塞数量，并在账号卡片与概览中呈现模型冷却。
- 管理台概览新增「需要关注」列表；签到和批量测试放在账号池页；账号次要操作收进「更多」菜单；模型重新拉取按钮位于模型目录。
- 调整客户端状态 API 文档表格格式。

### 升级

保留现有 `.env`、账号池和用量数据。可从管理台手动检查更新，或按原部署方式替换二进制；Docker 部署使用镜像更新流程。
