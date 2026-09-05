# 统一网关运行与发布

统一网关只有在目录发布版、售价、成本、凭据、部署成员、目录就绪和五类加密能力就绪全部满足时才接收新流量。目标目录一旦存在但尚未就绪，服务启动会直接失败，不会回退旧执行路径。

## 管理接口

- `GET /api/admin/unified-gateway/overview`：查看迁移、目录和部署状态。
- `GET /api/admin/unified-gateway/catalog`：分页查看发布版。
- `POST /api/admin/unified-gateway/catalog/:id/publish`：发布草稿。
- `GET /api/admin/unified-gateway/calls`：分页查看统一调用。
- `GET /api/admin/unified-gateway/calls/:id`：查看调用与分页 Attempt 元数据，不返回密文或原始正文。
- `POST /api/admin/unified-gateway/catalog/:id/activate`：在指定部署代次和就绪证明通过后原子切换活动发布版。
- `POST /api/admin/unified-gateway/deployments`：创建准备中的部署代次。
- `POST /api/admin/unified-gateway/deployments/:id/members`：登记实例成员。
- `POST /api/admin/unified-gateway/deployments/:id/members/:member_id/catalog-readiness`：上报目录摘要就绪。
- `POST /api/admin/unified-gateway/deployments/:id/members/:member_id/crypto-readiness`：上报加密密钥就绪。
- `POST /api/admin/unified-gateway/deployments/:id/activate`：通过就绪校验后激活代次。

## 运行时边界

统一路由会固定发布版、SKU、线路、Offering、凭据版本和用途授权；同步与后台请求分别创建 Call/Attempt，后台请求额外创建 AsyncExecution。计费使用发布币种和定点十进制 Reservation，终态只结算实际用量并释放未使用额度。

请求和结果正文只有在配置独立 Payload KEK/HMAC 后才进入加密 Blob；密钥缺失时不降级保存明文。管理列表只返回状态、时间和摘要，正文需按权限单独读取。

## 迁移与上线

生产环境先执行只读 `prism migrate audit` 和 `prism migrate audit-deep`。深度核对必须确认目标表完整、导入运行全部成功、无开放差异且历史源对象均有映射；随后由停机变更流程删除旧表和旧敏感字段，再激活部署代次与发布版。SSH、数据库凭据和加密密钥不得写入仓库或通过管理页面回显。
