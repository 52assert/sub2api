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

`Fork - Sync upstream` 每天北京时间约 10:23 检查更新，也会在 `custom` 更新后运行。可在 Actions 页面手动运行。

1. 从官方抓取 `main`，检查 Fork 的 `main` 能否快进；有分叉立即失败，绝不强制覆盖。
2. 更新本仓库 `main`，为 `main → custom` 创建或复用同一条 PR。
3. 显式触发 `Fork - Validate and build`，测试该 PR 与当前 `custom` 合并后的提交，不依赖机器人 PR 事件自动触发 CI。
4. 必需状态 `custom/validation` 通过后，工作流再次核对 PR 的来源、分支和已测试的 head/base 提交，自动以 merge commit 合并本仓库 `main → custom` PR；冲突、失败或提交变化时保留 PR 待处理。其他功能 PR 不自动合并。仓库关闭 squash、rebase 和自动删除分支。
5. 机器人合并后显式触发 `custom` 验证和镜像发布，避免 `GITHUB_TOKEN` 事件不触发后续工作流的问题。

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

## Codex 官方重置联动用户订阅

账号管理 → OpenAI OAuth 主账号操作菜单 → **重置关联订阅**。

- 手动：预览账号当前所在分组的全部有效订阅，二次确认后清零日、周已用额度。月额度和到期时间不变；暂停、撤销、过期、未开始的订阅不处理。日窗口仍按当天午夜，周窗口从执行时重新开始。重复请求使用同一操作 ID，不会二次清零。
- 自动：每个账号单独开启，默认关闭。全部实例共用一次每分钟的公告查询，地址固定为 `https://didcodexreset.com/openapi/v1/records/latest?kind=reset_completed`。读取 UTC `announcedAt`，界面按浏览器本地时间显示。接口失败指数退避，最长通常 30 分钟；有 `Retry-After` 时尊重服务端要求。
- 只处理开启后两小时内的 `reset_completed` 全局、所有套餐事件；不补执行开启前的历史公告。同一事件对共享分组的每份订阅最多处理一次；事件乱序、重启、重复轮询不会重复清零。
- 必须具有公告前大于 1% 的周用量快照和公告后不超过五分钟的快照，周用量降至 ≤1%，且已存在的 5h 窗口也接近零。零长度的 secondary 窗口不能当成已重置的 5h 额度。上游账号身份缺失或发生变化、缺少基线、未观察到下降、自然到期或无法识别周窗口时不自动执行，页面显示原因。
- 正常流量返回额度头时沿用已有快照更新（约 30 秒落库节流）。公告发现后先等待两分钟；仍缺少新快照才复用账号测试请求路径，发送 `hi`，固定 `gpt-6-astra` 和 `reasoning.effort=medium`，继承账号认证、代理和出站身份。每个账号每事件最多三次探测、间隔至少五分钟；模型不支持时不会替换成其他模型。
- 自动执行从订阅余额中扣除**发现公告时**记录的日、周已用额度，保留核验期间新增的计费。它不是精确追溯 `announcedAt` 的历史账单退款。窗口或日/周额度已被其他重置更新时跳过该订阅；之后发生过手动或其他事件重置的订阅不再处理。
- 重置卡（手动、自动）在请求上游前记录尝试，与官方核验共用账号锁。从基线到核验期间有用卡记录即排除联动，超时也视为可能已消费。**在 ChatGPT 官方页面/外部工具用卡无法可靠归因，请先关闭自动联动再操作**。人工订阅重置不消耗重置卡。
- `custom_codex_reset_*` 表保存策略、公告、核验任务、订阅重置前后金额及操作人。关闭开关不删除记录。迁移 `241_custom_codex_subscription_reset.sql` 为此 Fork 独立表，并为订阅表增加重置修订号和触发器，以识别原有手动/自然重置路径；同步上游时保留此文件并检查同编号新增迁移。

本地重点验证：`go test -tags=unit ./internal/service -run 'TestCustomCodex'`；
`go test -tags=integration ./internal/service -run 'TestCustomCodexResetTransactions'`（隔离 PostgreSQL 容器）；
前端 `AccountSubscriptionResetModal.spec.ts`。
