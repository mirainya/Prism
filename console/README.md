# Prism Console

Prism Console 是 Prism 项目的前端管理后台，基于 React + TypeScript + Vite 构建。

它负责提供：

- 登录与鉴权
- 仪表盘与统计图表
- 渠道 / 账号 / 能力管理
- Chat 模型与模型渠道映射管理
- Token 管理
- 请求日志查看
- Playground 在线调试

## 本地开发

### 环境要求

- Node.js 18+
- npm 9+

### 安装依赖

```bash
npm install
```

### 启动开发服务

```bash
npm run dev
```

默认地址：

```text
http://localhost:3000
```

## 构建

```bash
npm run build
```

构建产物输出到：

```text
dist/
```

## 与后端联调

前端页面依赖 Prism 后端接口，开发时通常需要同时启动后端：

```bash
go run ../cmd/server/main.go
```

后端默认地址：

```text
http://localhost:23523
```

## 目录说明

```text
console/
├── components/     # 通用组件
├── pages/          # 页面
├── services/       # API 请求封装
├── hooks/          # 自定义 Hook
├── utils/          # 工具函数
└── types.ts        # 类型定义
```
