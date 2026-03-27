# Codex2API

Codex2API 是一个基于 **Go + Gin + PostgreSQL + Redis + React/Vite** 的 Codex 反向代理与管理后台项目。

它对外提供兼容 OpenAI 风格的接口，并在内部维护一套基于 **Refresh Token 账号池** 的调度、刷新、测试、限流恢复、用量观测与后台管理能力。

本项目在设计与实现思路上参考并鸣谢 [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 与 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)。

---

## 快速部署

### 部署模式总览

| 模式 | 文件 | 适用场景 |
| --- | --- | --- |
| Docker 镜像部署 | `docker-compose.yml` | **推荐**，服务器 / 测试环境，直接拉取预构建镜像 |
| 本地源码容器构建 | `docker-compose.local.yml` | 本地改代码后做完整容器验证 |
| 本地开发 | `go run .` + `npm run dev` | 前后端联调与调试 |

### 方案一：Docker 镜像部署（推荐）

```bash
# 1. 克隆仓库并创建环境配置
git clone https://github.com/zhy0504/codex2api.git
cd codex2api
cp .env.example .env

# 2. 按需编辑 .env（至少设置镜像版本与安全变量）
# CODEX2API_TAG=vX.Y.Z
# ADMIN_SECRET=...
# CREDENTIALS_ENCRYPTION_KEY=...
# CODEX_API_KEYS=sk-...

# 3. 拉取镜像并启动
docker compose pull
docker compose up -d

# 4. 查看日志
docker compose logs -f codex2api
```

启动后访问：

- 管理台：`http://localhost:8080/admin/`
- 健康检查：`http://localhost:8080/health`

升级命令：

```bash
git pull && docker compose pull && docker compose up -d && docker compose logs -f codex2api
```

### 方案二：本地源码构建容器

```bash
cp .env.example .env
docker compose -f docker-compose.local.yml up -d --build
docker compose -f docker-compose.local.yml logs -f codex2api
```

> `docker-compose.yml` 会强制 `APP_ENV=production`；`docker-compose.local.yml` 会强制 `APP_ENV=development`。

### 方案三：本地开发模式

**后端：**

```bash
cp .env.example .env
cd frontend && npm ci && npm run build && cd ..
go run .
```

> 首次启动需要先构建前端，因为 Go 使用 `go:embed` 嵌入 `frontend/dist` 。

**前端开发服务器（联调）：**

```bash
cd frontend && npm ci && npm run dev
```

Vite 会自动代理 `/api` 和 `/health` 到后端，开发时访问 `http://localhost:5173/admin/`。

---

## 环境配置

### `.env` 环境变量

| 变量 | 说明 |
| --- | --- |
| `APP_ENV` | 运行环境，`development` / `production` |
| `CODEX_PORT` | HTTP 端口，默认 `8080` |
| `CODEX2API_IMAGE` | Docker 镜像仓库，默认 `ghcr.io/zhy0504/codex2api` |
| `CODEX2API_TAG` | Docker 镜像版本标签（建议固定版本，不用 latest） |
| `ADMIN_SECRET` | 管理后台密钥（可用于首次初始化） |
| `CREDENTIALS_ENCRYPTION_KEY` | 凭据加密密钥（用于加密存储 refresh/access/id_token，建议 32+ 长度） |
| `CREDENTIALS_ENCRYPTION_KEY_FILE` | 可选。未设置 `CREDENTIALS_ENCRYPTION_KEY` 时，自动生成并持久化密钥的文件路径 |
| `CODEX_API_KEYS` | 静态 API Keys（逗号分隔，可作为数据库 key 的补充） |
| `CORS_ALLOWED_ORIGINS` | 允许跨域来源（逗号分隔，默认仅同源） |
| `DATABASE_HOST` | PostgreSQL 主机 |
| `DATABASE_PORT` | PostgreSQL 端口，默认 `5432` |
| `DATABASE_USER` | PostgreSQL 用户 |
| `DATABASE_PASSWORD` | PostgreSQL 密码 |
| `DATABASE_NAME` | PostgreSQL 数据库名 |
| `DATABASE_SSLMODE` | SSL 模式，默认 `disable` |
| `REDIS_ADDR` | Redis 地址，例如 `redis:6379` |
| `REDIS_PASSWORD` | Redis 密码 |
| `REDIS_DB` | Redis DB 库号 |
| `TZ` | 时区，例如 `Asia/Shanghai` |

### 业务运行配置

以下参数**保存在数据库 `SystemSettings` 中**，通过管理台设置页面修改：

`MaxConcurrency`、`GlobalRPM`、`TestModel`、`TestConcurrency`、`ProxyURL`、`PgMaxConns`、`RedisPoolSize`、`AdminSecret`、自动清理开关等。

首次启动时程序会自动写入默认设置。

### API Key 与管理密钥

- **对外 API Key**：`/v1/*` 始终要求 `Authorization: Bearer sk-xxx`。密钥来源为数据库 `api_keys` 与环境变量 `CODEX_API_KEYS`（二者任一命中即可）。
- **管理后台 Admin Secret**：`/api/admin/*` 始终要求鉴权。支持两种方式：`X-Admin-Key` 请求头，或通过 `/api/admin/auth/login` 登录后使用 HttpOnly 会话 Cookie。密钥优先使用数据库 `AdminSecret`，为空时回退到环境变量 `ADMIN_SECRET`。
- **凭据加密**：账号凭据中的 `refresh_token` / `access_token` / `id_token` 以加密格式落库；首次启用密钥会自动迁移历史明文值。
  - 若未配置 `CREDENTIALS_ENCRYPTION_KEY`，服务会在首次启动自动生成一份高强度密钥并保存到 `CREDENTIALS_ENCRYPTION_KEY_FILE`（默认：`~/.codex2api/credentials_encryption_key`），后续启动复用该密钥。
  - Compose 模板已默认挂载持久化卷 `codex2api-secrets` 并注入 `CREDENTIALS_ENCRYPTION_KEY_FILE=/home/codex/.codex2api/credentials_encryption_key`，因此可不在 `.env` 明文填写该密钥。
- **CORS 策略**：默认仅允许同源请求；跨域需显式配置 `CORS_ALLOWED_ORIGINS`。
- **生产环境安全基线**：当 `APP_ENV=production` 时，若缺少 `Admin Secret`、`API Key` 或 `CREDENTIALS_ENCRYPTION_KEY`，服务将拒绝启动。
- **Compose 启动校验**：`docker-compose.yml` 会在启动前校验 `CODEX2API_TAG`、`ADMIN_SECRET`、`CODEX_API_KEYS` 是否已配置。

---

## 对外接口

| 接口 | 说明 |
| --- | --- |
| `POST /v1/chat/completions` | Chat Completions 风格入口 |
| `POST /v1/responses` | Responses 风格入口 |
| `GET /v1/models` | 返回可用模型列表 |
| `GET /health` | 健康检查 |

---

## 管理后台

浏览器访问 `/admin/`，提供以下页面：

| 页面 | 路径 | 功能 |
| --- | --- | --- |
| Dashboard | `/admin/` | 总览指标、请求趋势、延迟趋势、Token 分布、模型排行 |
| 账号管理 | `/admin/accounts` | 导入、测试、批量处理、调度信息查看 |
| 使用统计 | `/admin/usage` | 请求日志、统计卡片、图表、日志清空 |
| 运维概览 | `/admin/ops` | 运行态监控与系统概览 |
| 调度看板 | `/admin/ops/scheduler` | 调度健康度、惩罚项和评分拆解 |
| 系统设置 | `/admin/settings` | 业务运行参数与后台密钥配置 |

---

## 核心能力

### 项目定位

这个项目不是单纯的接口转发，而是一套面向长期运行的 Codex 网关与管理后台：

- 对外提供统一的 OpenAI 风格入口，屏蔽上游多账号差异
- 对内维护基于 `Refresh Token` 的账号池、`Access Token` 生命周期和运行时调度
- 通过 PostgreSQL 与 Redis 实现配置持久化、运行态缓存和高频协调
- 通过 `/admin` 管理台提供全面的运维观测能力

### 架构概览

**对外请求链路：** 客户端请求 → Gin RPM 限流 → `proxy.Handler` API Key 校验 → `auth.Store` 调度选号 → 上游请求 → 响应回传 + 用量写入

**管理后台链路：** 浏览器 → `/admin/` 嵌入式前端 → `/api/admin/*` 管理接口 → 数据库 / 账号池 / Redis

**链路追踪：** 网关会为每个请求生成/透传 `X-Request-ID`，并在响应头返回该值。服务日志采用结构化 JSON，包含 `request_id`、`account_id`（命中账号时）等字段，便于排障。

### 调度系统

调度核心位于 `auth.Store`，将账号可用性、健康度、动态并发、历史错误和近期用量综合纳入选择。

**运行时状态模型：**

- `Status`：`ready` / `cooldown` / `error`
- `HealthTier`：`healthy` / `warm` / `risky` / `banned`
- `SchedulerScore`：以 100 为基线的实时调度分
- `DynamicConcurrencyLimit`：按健康层级动态收缩的并发上限

**账号选择策略：**

1. 过滤不可用账号（error / banned / 冷却中 / 无 AccessToken）
2. 重算健康层级、调度分和动态并发
3. 排除已达并发上限的账号
4. 按 `healthy > warm > risky > banned` 排序，同层级按调度分和并发数择优
5. 15% 概率随机打散，降低热点与饥饿

**动态并发规则：**

| 层级 | 并发上限 |
| --- | --- |
| `healthy` | 系统 `MaxConcurrency` |
| `warm` | 基础并发 ÷ 2（最少 1） |
| `risky` | 固定 1 |
| `banned` | 固定 0，不参与调度 |

**调度分惩罚/奖励：**

| 信号 | 影响 |
| --- | --- |
| `unauthorized` | `-50`，24h 线性衰减 |
| `rate_limited` | `-22`，1h 线性衰减 |
| `timeout` | `-18`，15min 线性衰减 |
| `server error` | `-12`，15min 线性衰减 |
| 连续失败 | 每次 `-6`，最多 `-24` |
| 连续成功 | 每次 `+2`，最多 `+12` |
| 近期成功率过低 | `<75%` 扣 8，`<50%` 扣 15 |
| Free 7d 用量 | `≥70%` 扣 8 → `≥100%` 扣 40 |
| 延迟 EWMA | `≥5s` 扣 4 → `≥20s` 扣 15 |

**冷却与恢复机制：**

- **429**：优先解析上游 `resets_at`，否则按套餐类型推断冷却时间
- **401**：直接进入 `banned`，6h 冷却，24h 内再触发升至 24h
- 冷却状态持久化到 PostgreSQL，重启后自动恢复
- 后台会对 `banned` 账号做周期性低频恢复探测

**调度可观测性：**

- `GET /api/admin/accounts` — 健康层级、调度分、惩罚拆解
- `GET /api/admin/ops/overview` — 系统运行态与连接池概览
- `/admin/ops/scheduler` — 前端调度看板

---

## 目录结构

```text
codex2api/
├─ main.go                      # 程序入口
├─ Dockerfile                   # 多阶段镜像构建
├─ docker-compose.yml           # 镜像部署模板
├─ docker-compose.local.yml     # 本地源码构建模板
├─ .env.example                 # 环境变量示例
├─ admin/                       # 管理后台 API
├─ auth/                        # 账号池、调度与 token 管理
├─ cache/                       # Redis 缓存封装
├─ config/                      # 环境变量加载
├─ database/                    # PostgreSQL 访问层
├─ proxy/                       # 对外代理、转发与限流
└─ frontend/                    # React + Vite 管理后台
   ├─ src/pages/                # Dashboard / Accounts / Usage / Ops / Settings
   ├─ src/components/           # UI 组件
   ├─ src/locales/              # 国际化语言文件 (zh/en)
   └─ vite.config.js            # Vite 配置
```

---


## 常见注意事项

- `docker-compose.yml` 拉取 GHCR 镜像用于部署；`docker-compose.local.yml` 用 `build: .` 做本地构建
- 前端基路径固定为 `/admin/`，本地开发和生产部署一致
- 本地手动构建 Go 二进制前需先执行 `frontend/` 的 `npm run build`
- `.env` 除物理层配置外，还包含安全基线相关变量（`APP_ENV`、`ADMIN_SECRET`、`CREDENTIALS_ENCRYPTION_KEY`、`CODEX_API_KEYS`）
- API Key 以数据库为主，可由 `CODEX_API_KEYS` 提供静态兜底
- OAuth 授权会话保存在 Redis（TTL 30 分钟），服务重启后仍可继续兑换授权码

---

## 免责声明与开源协议

- 本项目仅供学习、研究与技术交流使用。
- 本项目采用 `MIT License` 开源协议。
- 项目不对任何直接或间接使用后果提供担保；生产环境使用风险由使用者自行承担。
