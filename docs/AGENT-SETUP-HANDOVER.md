# 交接文档：本地 LLM 网关 + Agent 工具链

> 本文档记录 2026-10-03 搭建的**本地 Z.AI 网关（zcode2api）与四个 Agent 客户端的接线方案**。
> 与 `HANDOVER.md`（PCMannager Go 项目自身交接）**无关**，请勿混淆。

---

## 0. 一句话架构

```
Z.AI Coding Plan 账号（浏览器 OAuth 登录，用户本人完成）
        │  JWT + 阿里云无痕验证码
        ▼
zcode2api 网关  http://127.0.0.1:3000   （Python FastAPI，v2.6.8）
        │  双协议出口：/v1/messages（Anthropic）、/v1/chat/completions（OpenAI）
        ├──────────────► Claude Code 2.1.288
        ├──────────────► pi-agent 1.0.0   （+ pi-subagents / pi-dynamic-workflows / superpowers）
        ├──────────────► opencode 2.0.22  （默认模型）
        └──────────────► omo 5.1.13
```

四个客户端**共用同一份账号额度**。网关是单点：它挂了，四家全断。

---

## 1. 已验证的模型规格（勿凭记忆改）

| 项 | GLM-5.3-Flash | GLM-5.3 |
|---|---|---|
| 上下文窗口 | **1,000,000** | **1,000,000** |
| 最大输出 | 131,072（默认 65,536） | 131,072 |
| 推理 | **恒开，不可关闭**（effort: low/high/max） | 恒开 |
| 输入模态 | 文本 + 图像 + 视频 + 文件（原生多模态） | 仅文本 |

来源：Z.AI 官方文档（`docs.z.ai/guides/llm/glm-5.3-flash`、`.../glm-5.3`）。网关侧 `app/constants.py:92` 的 `MAX_TOKENS_LIMIT = 131072` 与之一致。

**实测补充**：网关接受约 5.75MB 请求体、拒绝约 6.9MB（上游 `code 1261 prompt is too long`），与 1M token 量级吻合。

> ⚠️ **不要用 `usage.input_tokens` 反推窗口大小**。实测同一 4.6MB 请求体两次分别回报 `800,317` 和 `61`——该字段在大prompt 下不可靠（含缓存/截断行为）。判断窗口请查上表，不要跑探针。

---

## 2. 文件清单（全部已落地）

| 路径 | 作用 |
|---|---|
| `zcode2api/.env` | 网关进程配置（端口、绑定地址、验证码求解器、Chromium 路径） |
| `zcode2api/data/accounts.db` | SQLite：账号池 + 设置 + 后台密码 |
| `~/.pi/agent/models.json` | pi 的 `zcode-local` provider（两个模型，1M） |
| `~/.pi/agent/settings.json` | pi 已装包清单（3 个） |
| `~/.omo/agent/` | omo 的**独立**配置目录（见 §6 坑 4） |
| `~/.claude/settings.json` | Claude Code：base URL + 模型钉死 + 1M |
| `~/.config/opencode/opencode.json` | opencode：provider + **默认模型** + superpowers 插件 |

备份（迁移前）：`/tmp/opencode/pi-backup/`（`models.json`、`auth.json`、`.rustcode/settings.json`）。

---

## 3. 客户端接线要点

### 3.1 Claude Code（`~/.claude/settings.json`）

```
ANTHROPIC_BASE_URL = http://127.0.0.1:3000
ANTHROPIC_MODEL / DEFAULT_{OPUS,SONNET,HAIKU} / SMALL_FAST = GLM-5.3-Flash
CLAUDE_CODE_MAX_CONTEXT_TOKENS = 1000000
```

模型名**必须显式覆盖**：Claude Code 默认发 `claude-sonnet-4-5-*`，网关只映射 `glm-*` 别名，未知名原样透传，上游回 `3006 model not allowed`。

### 3.2 pi-agent（`~/.pi/agent/models.json`）

provider `zcode-local`，`api: anthropic-messages`，`compat.supportsEagerToolInputStreaming: false`。

### 3.3 opencode（`~/.config/opencode/opencode.json`）

```jsonc
"model": "zcode/glm-5.3-flash",     // 默认模型，不带 --model 也走这里
"providers": { "zcode": {
  "package": "@opencode/ai/providers/anthropic",
  "settings": { "baseURL": "http://127.0.0.1:3000/v1" },
  "models": { "glm-5.3-flash": { "modelID": "GLM-5.3-Flash", "limit": {...} } }
}}
```

### 3.4 omo

配置目录已含 `zcode-local`（1M）。默认模型未改，需`--provider zcode-local --model GLM-5.3-Flash` 指定。

---

## 4. 运维

```bash
# 启动网关（前台；后台请自行 nohup / tmux）
cd /workspaces/PCMannager/zcode2api && .venv/bin/python cli.py serve

# 查看账号与额度
.venv/bin/python cli.py accounts
.venv/bin/python cli.py quota

# 验证码求解器自检（应输出 VERIFY_PARAM=...，约 5s）
cd captcha_node && ZCODE_CHROMIUM_PATH=<见 .env> node solver_pw.js 11xygtvd cn no8xfe
```

后台 UI：`http://127.0.0.1:3000/admin/login`，密码见 `.env` 的 `ZCODE_ADMIN_KEY`。
监控页：`/admin/monitoring`。

---

## 5. 已安装的 Agent 扩展

pi（`pi list`）：

| 包 | 版本 | 说明 |
|---|---|---|
| `pi-subagents` | 0.75.0 | 13 个 agent：`scout` `reviewer` `researcher` `oracle` `worker` `delegate` `evidence-auditor` + 6 个 external-CLI；2 个 skill：`pi-subagents`、`council-mode` |
| `pi-dynamic-workflows` | 1.0.1 | 工作流编排（`runs.run/all/lanes`） |
| `obra/superpowers` | git | skills 集（`superpowers` 内容） |

opencode：superpowers 插件已加载（`plugins` 字段），实测能正确描述自身工作流。

---

## 6. 踩过的坑（**请勿重蹈**）

**坑 1 · pi 有两个作用域，装错版本直接崩。**
pi 已由 `@mariozechner/pi-*` 改名 `@earendil-works/pi-*`。本机原为 `@mariozechner/pi-coding-agent@0.73.1`，现已迁移到 `@earendil-works/pi-coding-agent@1.0.0`。
切换点在 `pi-subagents` **0.24.0 → 0.24.1**；`pi-dynamic-workflows` **1.0.0 → 1.0.1**。
两个扩展的最新版都对 `@earendil-works/*` 有**运行时值导入**（非仅类型），作用域不匹配会硬崩而非降级。
（`0.24.0` 是最后可用旧版，但比当前 pi 落后 49 个版本，缺 council-mode 与多lane 编排，不建议。）

**坑 2 · opencode 的 baseURL 必须带 `/v1`。**
opencode 的 anthropic provider 会拼 `baseURL + "/messages"`。写 `http://127.0.0.1:3000` →请求 `/messages` → 404 `AI.Error.InvalidRequest: Not Found`。必须是 `.../v1`。

**坑 3 · zcode2api 的 README 关于验证码已过期。**
README 写 Node + jsdom；实际默认 `ZCODE_CAPTCHA_SOLVER=pw`（`app/settings.py:75`），用真 Chromium（puppeteer-core）+ 18 个系统库。jsdom 路线（`solver.js`）注释写明*"2026-09 起被风控全拒"*，仅留作回滚。
Ubuntu 24.04 的 `chromium` 是 snap 转接包，容器内不可用；本机用 Chrome for Testing 154（`~/.cache/puppeteer/`），已装 `libatk1.0-0t64` 等依赖。

**坑 4 · omo 会复制走 pi 的配置目录。**
omo 首次运行把 `~/.pi/agent` 整体拷到 `~/.omo/agent`，之后**只读自己的目录**。之后在 `~/.pi/agent` 的改动**不会**同步给 omo。两边要分别维护。

**坑 5 · 网关的 OpenAI 兼容层会丢弃推理内容。**
`app/openai_compat.py` 中 `grep thinking|reasoning_content` **零命中**。GLM 是恒开推理模型，走 `/v1/chat/completions` 时若`max_tokens` 偏小，推理 token 会吃光预算并返回 `content: null`。
**结论：四个客户端一律走 `/v1/messages`（Anthropic 协议）。**

**坑 6 · 未鉴权 + 绑`0.0.0.0` = 敞开白嫖。**
网关默认 `ZCODE_HOST=0.0.0.0`，而 `ZCODE_GATEWAY_KEY` 留空即不校验。已改 `127.0.0.1`。**若要对外暴露，必须先在后台设置页配网关 API Key。**

**坑 7 · 别调小额度轮询间隔。**
`ZCODE_QUOTA_REFRESH_INTERVAL`（1800s）与 `ZCODE_CLAIM_ROUND_INTERVAL`（3600s）是**风控相关值**。上游 billing 连续查询是触发 `3012 unusual activity` 的主信号源（`app/settings.py:88-90` 注释实证），调小等于主动触发封控。

---

## 7. 验收命令（改动后逐条跑）

```bash
# 1. 网关活着 + 只绑回环
curl -s http://127.0.0.1:3000/meta; ss -ltnp | grep 3000

# 2. 账号可用
cd zcode2api && .venv/bin/python cli.py accounts   # 期望 status=active

# 3. 验证码求解
cd zcode2api/captcha_node && node solver_pw.js 11xygtvd cn no8xfe   # 期望 VERIFY_PARAM=

# 4. Claude Code
claude -p "报出你的模型名"

# 5. pi + subagent
pi -p --no-session --provider zcode-local --model GLM-5.3-Flash "用 subagent 委派给 scout：列出 zcode2api/app/routes 下的文件名"

# 6. opencode 默认模型
opencode run "报出你的模型名"

# 7. omo
omo --list-models | grep -i glm
```

---

## 8. 待办/ 可选

- 网关目前是**前台手工进程**，未做开机自启/守护；容器重启后需手动 `cli.py serve`。
- 未配网关 API Key（当前仅靠回环隔离）。若要局域网共享，先配 key再改 `ZCODE_HOST`。
- omo 的默认模型未设为 GLM，如需可加别名或环境变量固化。
- `piagent@0.52.0` 是另一个无关工具（"multi-channel AI agent gateway"），自带嵌套 `node_modules`，与本链路无关，勿混淆。