# NOTES

- MCP 创建待办前通过只读工具定义检查 `confirm_token`，不能调用写工具试探服务端版本；旧服务端会直接创建。
- App marker 使用顶层 `project` 与 `product`；在线通过账号级 `list_apps` 验证归属，离线只验证本地指纹。
