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
4. 必需状态 `custom/validation` 通过后，由维护者选择 **Create a merge commit** 合并。仓库关闭 squash、rebase 和自动删除分支。
5. 合并后验证 `custom`，通过后构建并发布自己的镜像。

暂未开启自动合并或服务器自动部署。没有冲突并不等于业务兼容；功能开发后应补充关键回归测试。

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

本次仅准备仓库和发布流程，没有部署生产服务或修改现有数据库。

## 定制版更新保护

镜像使用 `BUILD_TYPE=custom` 构建。前端复用现有构建类型判断隐藏官方更新入口；后端同时拒绝原地更新、下载历史官方版本和本地二进制回滚。版本查询不访问官方发布源，也不读取官方更新缓存。

普通 `release` / `source` 构建保持原有行为。手动构建定制镜像必须传入 `--build-arg BUILD_TYPE=custom`。

## Actions 与凭据

- `UPSTREAM_SYNC_SSH_KEY`：仅此仓库的可写 Deploy Key，用于推送上游提交（包括 `.github/workflows` 的改动），不使用个人 PAT。
- `GITHUB_TOKEN`：按 job 分配权限，创建 PR、显式触发验证、写状态和发布 GHCR 包。测试任务只有读权限，checkout 不保留凭据。
- `custom` 分支要求 `custom/validation` 成功，禁止强推与删除；允许手动 merge commit，不要求额外审批人。
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
