# NOTES

- CLI 按当前 cloud 的两阶段接口创建待办：先预览项目，再携带 `confirm_token` 确认创建；不探测旧版接口。
- App marker 使用顶层 `project` 与 `product`；在线通过账号级 `list_apps` 验证归属，离线只验证本地指纹。
