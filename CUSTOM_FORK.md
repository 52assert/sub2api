# 定制版维护与发布

仓库：https://github.com/52assert/sub2api

## 分支约定

- `main`：官方 `Wei-Shaw/sub2api:main` 的镜像，只允许快进同步。
- `custom`：默认开发、验证和镜像发布分支。
- `feature/<功能名>`：从 `custom` 创建，完成后向本仓库的 `custom` 提 PR。

```bash
git switch custom
git pull --ff-only origin custom
git switch -c feature/my-feature
```

独立文件和模块优先，尽量减少对官方核心文件的改动。数据库变更新增迁移，合并官方变更时检查迁移顺序和编号冲突。保留官方模块路径，避免不必要的全仓改名。

## 自动同步

`Fork - Sync upstream` 每小时第 23 分钟检查更新，也会在 `custom` 更新后运行。GitHub 定时任务可能延迟执行；可在 Actions 页面手动运行。没有上游新提交时直接结束，有更新才进入验证、合并和镜像发布流程。

1. 从官方抓取 `main`，检查 Fork 的 `main` 能否快进；有分叉立即失败，绝不强制覆盖。
2. 更新本仓库 `main`，为 `main → custom` 创建或复用同一条 PR。
3. 显式触发 `Fork - Validate and build`，测试该 PR 与当前 `custom` 合并后的提交，不依赖机器人 PR 事件自动触发 CI。
4. 必需状态 `custom/validation` 通过后，工作流再次核对 PR 的来源、分支和已测试的 head/base 提交，自动以 merge commit 合并本仓库 `main → custom` PR；冲突、失败或提交变化时保留 PR 待处理。其他功能 PR 不自动合并。仓库关闭 squash、rebase 和自动删除分支。
5. 机器人合并后显式触发 `custom` 镜像发布，避免 `GITHUB_TOKEN` 事件不触发后续工作流的问题。合并提交的合入分支（第二个父提交）若已带成功的 `custom/validation` 状态，直接复用该结果并跳过重复检查；无法确认（非 merge 提交、状态缺失或 API 出错）时回退到完整检查。

验证按改动范围裁剪：PR 只改 `frontend/**` 时跳过 backend 测试和 golangci-lint，只改 `backend/**` 时跳过前端检查，diff 无法确定时保守跑全量。`custom/validation` 仍由 `result` 汇总后写入，保护规则不变。

已开启上游同步 PR 自动合并；服务器自动部署保持关闭。没有冲突并不等于业务兼容；功能开发后应补充关键回归测试。

GitHub 可能延迟定时任务，公共仓库长期无活动时也可能停用定时任务。可在 Actions 页面重新启用并手动运行。首次验证记录和失败详情同样在 Actions 页面查看。

## 冲突处理

不能把定制代码合入 `main`。从 `custom` 创建解决分支，在本地合并官方代码：

```bash
git fetch origin
git switch -c fix/upstream-sync origin/custom
git merge origin/main
# 处理冲突，运行检查并提交
git push -u origin fix/upstream-sync
gh pr create --repo 52assert/sub2api --base custom --head fix/upstream-sync
```

解决分支合入后，原同步 PR 可能自动关闭；若仍打开，确认已包含所有上游提交后关闭。可手动重新运行同步验证。

## 镜像与部署

镜像：`ghcr.io/52assert/sub2api`，同时构建 `linux/amd64`、`linux/arm64`。

- `sha-<完整提交 SHA>`：对应源代码提交。
- `custom`：最近一次通过验证及镜像启动版本检查的构建。
- 生产部署使用 Actions 运行摘要中的 `@sha256:...` 摘要，确保固定产物；不要依赖浮动标签。

首次 GHCR 包的可见性可能为 private。部署主机需登录有该包读取权限的账号；若希望免登录拉取，可由仓库所有者在包设置中切换为 public。不要把访问令牌提交到仓库。

使用官方 Compose 配置叠加本仓库覆盖文件，继承后续官方配置改进：

```bash
cd deploy
cp .env.example .env # 仅首次，已有配置不要覆盖
# 编辑 .env，配置数据库密码等，并添加：
# SUB2API_IMAGE=ghcr.io/52assert/sub2api@sha256:<Actions 摘要中的真实 digest>
docker compose --env-file .env -f docker-compose.local.yml -f docker-compose.custom.yml config --quiet
docker compose --env-file .env -f docker-compose.local.yml -f docker-compose.custom.yml pull sub2api
docker compose --env-file .env -f docker-compose.local.yml -f docker-compose.custom.yml up -d
```

升级前备份数据库与数据目录，记录当前镜像摘要。回退镜像时将 `SUB2API_IMAGE` 改回旧摘要，再执行上述命令；如果升级涉及不兼容数据库迁移，需要配套恢复数据库，不能仅换旧镜像。

已有 Docker 部署接入页面在线更新，按照 [宿主机更新服务安装与恢复说明](deploy/updater/README.md) 操作。页面触发后先备份，再更换固定摘要镜像并重建应用容器，升级期间短暂不可用。

## 定制版更新保护

镜像使用 `BUILD_TYPE=custom` 构建。页面通过独立宿主机服务查询本仓库 Release 并升级整个应用容器；后端拒绝旧式原地二进制更新、下载历史官方版本和二进制回滚。更新服务不可用时不会切换到官方发布源。

可见版本保持 `0.2.3` 等官方版本号，完整源码 SHA 区分同版本的新构建。CI 仅在版本号与官方最新稳定 Release 一致时发布在线更新清单，使用独立 `custom-v...` 标签和不可变镜像摘要。上游 PR 合并并通过构建后，管理员即可在页面检查并安装；不自动重启生产服务器。

普通 `release` / `source` 构建保持原有行为。手动构建定制镜像必须传入 `--build-arg BUILD_TYPE=custom`。

## Actions 与凭据

自动创建同步 PR 需要在仓库 **Settings → Actions → General → Workflow permissions** 开启 **Allow GitHub Actions to create and approve pull requests**。GitHub 将创建和审批合并成同一个设置；本仓库的工作流创建 PR、不提交审批；上游同步 PR 在必需检查通过后自动合并。若该开关未开启，镜像同步仍可进行，但遇到需要新建 PR 的官方更新时会报权限错误；开启后重新运行同步即可。


- `UPSTREAM_SYNC_SSH_KEY`：仅此仓库的可写 Deploy Key，用于推送上游提交（包括 `.github/workflows` 的改动），不使用个人 PAT。
- `GITHUB_TOKEN`：按 job 分配权限，创建 PR、显式触发验证、写状态和发布 GHCR 包。测试任务只有读权限，checkout 不保留凭据。
- `custom` 分支要求 `custom/validation` 成功，禁止强推与删除；允许 merge commit，不要求额外审批人；仅上游同步 PR 自动合并。
- `main → custom` 的 PR 刻意不要求把 base 更新到 head，否则会污染官方镜像分支。每次 `custom` 更新会重新同步并验证；验证报告还检查测试期间 base/head 是否变化。
- 官方 `CI`、`Release`、`CLA Assistant` 工作流在此 Fork 的仓库设置中禁用，文件保留以减少同步冲突。自定义 CI 覆盖后端测试/lint、前端 lint/typecheck/关键测试/build 和部署检查；官方 `Security Scan` 保留启用。

## 本地检查

```bash
python3 tools/custom/test_sync.py
bash -n tools/custom/sync-upstream.sh
make -C backend test-unit test-integration
pnpm --dir frontend install --frozen-lockfile
make test-frontend
pnpm --dir frontend run build
```

## 降智测试的原生 Codex CLI

账号管理 → 更多 → **降智测试**。OpenAI 账号默认使用 **Codex CLI**，也可选择 HTTP 进行对照；其他平台继续使用 HTTP。原生 CLI 支持 OAuth 和使用 Responses 协议的 API Key 账号，沿用所选账号的凭据、模型映射、代理以及选定的思考等级。Agent Identity 等不兼容认证会明确拒绝，不自动切换执行方式。

定制镜像内置官方 Codex CLI **0.160.0**，分别校验 amd64/arm64 的官方安装包 SHA-256。后台调用 `codex exec`，通过标准输入传入原始提示词，不读取已有 Codex 配置、登录账号或规则文件；OAuth 令牌由现有刷新逻辑取得，不向 CLI 提供刷新令牌。请求身份设为 `codex_cli_rs`，User-Agent 由真实 CLI 根据版本和容器系统生成。

每次任务拥有独立临时目录和登录文件，结束或超时后清理，并终止工具子进程。`codex-test-sandbox` 对整个 CLI 和子进程施加 Landlock 文件访问限制，允许读取运行所需系统文件、只在本次任务目录内写入文件内容；应用数据目录和其他任务不在读取范围。CLI 使用外部沙箱模式，不额外启动 bubblewrap。容器无需增加特权或关闭默认 seccomp；宿主内核须支持 **Landlock ABI ≥ 2**。ABI 2 额外安装系统调用过滤器，补齐文件截断保护；无法施加限制时创建测试会直接报错，保留 HTTP 选项。

管理员发起后台任务，普通登录用户在「我的账户 → 降智测试」查看同一份结果。最多两个任务同时执行，单次限时 15 分钟，保留最近 10 次完成记录。记录展示所用账号名称、执行方式、CLI 版本、实际模型、时间和产物文件；收集任务中的 HTML/SVG 文件并以隔离 iframe 预览，同时保留 CLI 最终回复。自定义推理题等文字测试无需生成文件，CLI 完整结束后直接展示非空最终回答。临时重连提示不会将最终成功的任务误判为失败，真正失败时显示固定错误类别并在服务端保留脱敏诊断。旧记录标记为 HTTP。

使用本仓库定制镜像无需自行安装 CLI。二进制部署需额外编译 `backend/cmd/codex-test-sandbox` 并安装 Codex 的 `codex-execve-wrapper`、`apply_patch` 等辅助别名；可通过 `INTELLIGENCE_TEST_CLI_PATH`、`INTELLIGENCE_TEST_SANDBOX_PATH` 配置同机绝对路径，默认分别为 `/usr/local/bin/codex`、`/usr/local/bin/codex-test-sandbox`。安装后可检查：

```bash
docker exec sub2api /usr/local/bin/codex --version
docker exec sub2api /usr/local/bin/codex-test-sandbox --check
```

## Codex 官方重置联动用户订阅

账号管理 → OpenAI OAuth 主账号操作菜单 → **重置关联订阅**。

- 手动：预览账号当前所在分组的全部有效订阅，二次确认后清零日、周已用额度。月额度和到期时间不变；暂停、撤销、过期、未开始的订阅不处理。日窗口仍按当天午夜，周窗口从执行时重新开始。重复请求使用同一操作 ID，不会二次清零。
- 自动：每个账号单独开启，默认关闭。全部实例共用一次每 10 分钟的公告查询，地址固定为 `https://didcodexreset.com/openapi/v1/records/latest?kind=reset_completed`。读取 UTC `announcedAt`，界面按浏览器本地时间显示。接口失败从 10 分钟开始指数退避，最长通常 30 分钟；有 `Retry-After` 时至少等待 10 分钟并尊重更长的服务端要求。界面区分限流与其他错误，并显示下次查询时间。
- 只处理开启后发布且不超过 24 小时的 `reset_completed` 全局、所有套餐事件；不补执行开启前的历史公告。同一事件对共享分组的每份订阅最多处理一次；事件乱序、重启、重复轮询不会重复清零。
- 必须具有公告前大于 1% 的周用量快照和公告后不超过五分钟的快照，周用量降至 ≤1%，或相比公告前下降超过 1 个百分点（允许检测延迟期间已有新用量）。零长度的 secondary 窗口不能当成已重置的 5h 额度。上游账号身份缺失或发生变化、缺少基线、未观察到下降、自然到期或无法识别周窗口时不自动执行，页面显示原因。
- 新公告后的首次核验会对启用自动联动的有效主账号主动发送一次 `gpt-6-astra` 请求，提示词为 `Reply with exactly OK`，等待生成完成以尽快启动上游本周期窗口，并用返回的额度头核验。发送前持久化探测次数，同一公告／账号最多尝试一次，超时、失败、重启均不重发；失败或缺少新窗口时，在公告的 24 小时有效期内继续等待正常流量。已有用卡尝试、停用或身份变化的账号不会探测。正常流量的快照更新仍沿用约 30 秒落库节流；新公告会取代尚未完成的旧公告任务。
- **周窗口到期后主动探测**：独立账号开关，默认关闭。开启后每分钟检查已知的 `codex_7d_reset_at`，自然到期后也使用 `gpt-6-astra` 发送 `Reply with exactly OK`，不依赖官方随机重置公告、不清零关联订阅。仅处理活跃主账号。已知窗口只处理开启后到期的时间；没有有效 7d 窗口时，按本次开关开启时间进行一次初始化探测，取得窗口时间后继续按时触发。初始化失败或响应没有额度头也不重复发送；明确关闭再开启允许再次初始化。`243_custom_codex_window_probes.sql` 保存开关、开启时间和探测记录，按上游身份及窗口去重，容忍额度头时间数秒漂移；失败、重启或多实例不会重发同一窗口，后续新周窗口可再次探测。
- 自动执行从订阅余额中扣除 **UTC `announcedAt` 时刻及之前**记录的日、周已用额度，保留公告后、轮询延迟及核验期间的所有新增计费。金额快照由数据库触发器与订阅更新一起提交，按实际记账时间划分，不按请求发起时间或异步用量日志估算。公告前历史记录缺失时跳过并提示人工确认。周周期直接按已核验上游账号的 `7d_reset_at - 7 days` 对齐，同一事件／分组固定一个共享起点，不按各用户首次使用分别计算。缺少上游窗口时间时继续等待正常使用。公告发布可能晚于真实重置：若账号周期起点早于公告，则采用更早的起点取基线和金额历史，确保本周期消费也保留；不会清除公告后的消费。日额度继续使用自然日窗口，月额度及月窗口不变。日、周分别保护：某个窗口已被自然或人工重置时保留该窗口，另一窗口仍可补偿；之后发生过本功能手动或其他事件重置的订阅不再处理。
- 重置卡（手动、自动）在请求上游前记录尝试，与官方核验共用账号锁。从基线到核验期间有用卡记录即排除联动，超时也视为可能已消费。**在 ChatGPT 官方页面/外部工具用卡无法可靠归因，请先关闭自动联动再操作**。人工订阅重置不消耗重置卡。
- `custom_codex_reset_*` 表保存策略、公告、核验任务、订阅重置前后金额及操作人。关闭开关不删除记录。迁移 `241_custom_codex_subscription_reset.sql` 为此 Fork 独立表，并为订阅表增加重置修订号和触发器，以识别原有手动/自然重置路径；同步上游时保留此文件并检查同编号新增迁移。

迁移 `242_custom_codex_reset_history.sql` 新增订阅金额历史和账号额度快照历史，保留两天以及每份订阅/账号的最近一条更早基线。未发生金额或窗口变化的更新不写历史。升级前尚未执行的旧补偿任务无法转换成公告时点余额，会标记为缺少历史供人工确认。

本地重点验证：`go test -tags=unit ./internal/service -run 'TestCustomCodex'`；
`go test -tags=integration ./internal/service -run 'TestCustomCodexResetTransactions'`（隔离 PostgreSQL 容器）；
前端 `AccountSubscriptionResetModal.spec.ts`。
