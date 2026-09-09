# Docker 页面在线更新

此服务专用于 `52assert/sub2api`，使用 Python 3.9+ 标准库、Docker Compose v2+ 与 systemd。页面版本号保持官方数字版本；完整源码 SHA 区分同版本定制构建。只有通过 CI、镜像检查并发布 `container-update.json` 的本仓库 Release 才可安装。

## 首次安装

适用于 `/root/sub2api-deploy/docker-compose.yml`、应用服务/容器 `sub2api`、数据库容器 `sub2api-postgres`、数据目录 `data` 的现有部署。先核对 Compose 项目名、容器名和 UID/GID；示例默认项目名 `sub2api-deploy`、应用 GID 1000。其他布局应先修改配置和服务的 `ReadWritePaths`。

1. 保留原 Compose、`.env` 和数据库配置。若已有 `docker-compose.override.yml`，先把其中自定义配置合入主配置；更新器拒绝覆盖非自身管理的覆盖文件。
2. 将 `agent.py` 安装为 `/opt/sub2api-updater/agent.py`（root:root，0644），配置安装为 `/etc/sub2api-updater/config.json`（root:root，0600）。配置目录应为 0700。
3. 将本目录服务文件安装到 `/etc/systemd/system/sub2api-updater.service`，执行 `systemctl daemon-reload`。
4. 确认本仓库已有可用的容器更新 Release，且服务器可以拉取对应 GHCR 镜像。停止更新守护进程后，执行首次切换：

```bash
python3 /opt/sub2api-updater/agent.py --config /etc/sub2api-updater/config.json --install-latest
systemctl enable --now sub2api-updater
```

首次命令会拉镜像、校验来源，停止应用并备份数据库/数据/配置/当前可执行程序，然后只重建应用。数据库和 Redis 容器不重建。命令返回失败时先查看任务，不能直接重复强制安装。

更新器管理默认的 `docker-compose.override.yml`，写入固定镜像摘要和只读 Unix socket 目录挂载。因此在部署目录执行普通 `docker compose up -d` 也会使用选定镜像。不要通过显式 `-f docker-compose.yml` 排除覆盖文件，不要用旧官方镜像覆盖自定义镜像。此实现会短暂停机，长请求可能中断。

## 页面使用

登录管理员账号，点击左上角版本 → 重新检查 → 在线更新 → 确认更新。前端在应用重建期间等待重连；仅当宿主机确认镜像、程序版本/源码 SHA、Docker 健康和 HTTP 健康检查均通过后显示成功。同一个数字版本的新构建也会显示更新。

更新器只接受固定仓库的摘要镜像，不开放 TCP 端口，也不向应用挂载 Docker socket。Unix socket 连接权限授予应用 GID；不要把该组授予不可信本地进程。未安装/无法连接更新服务时页面提示错误，不会退回下载官方二进制。

## 备份与故障处理

```bash
python3 /opt/sub2api-updater/agent.py --config /etc/sub2api-updater/config.json --status
journalctl -u sub2api-updater -n 100 --no-pager
docker inspect sub2api --format '{{json .State}}'
```

备份位于 `/var/lib/sub2api-updater/<任务ID>/`，包含 `database.dump`、`data.tar.gz`、Compose/`.env`（若存在）和 `sub2api.previous`。这些文件含敏感配置，目录仅 root 可读。保留并定期转存必要备份、检查磁盘剩余空间；当前不会自动删除备份或旧镜像。

- 拉取/校验失败：不停止原应用。
- 备份失败：启动原容器；启动命令失败则标记需要人工处理。
- 重建后健康检查失败或更新器中途退出：标记 `needs_attention`，阻止继续升级。查看宿主机记录与应用日志后修复。
- 不自动恢复数据库：新版本可能已执行迁移，直接退镜像或覆盖数据库可能丢失升级后的写入。人工回滚应先停应用、保留失败现场，并确认数据库兼容性和恢复时间点。旧在线更新修改过容器内程序时，旧镜像不能代表旧运行版本，需结合保存的实际程序恢复。

若修复后已成功运行该任务指定的新镜像，可停止守护进程并运行以下命令。它重新执行全部运行检查，通过才解除阻塞，随后再启动守护进程：

```bash
systemctl stop sub2api-updater
python3 /opt/sub2api-updater/agent.py --config /etc/sub2api-updater/config.json --verify-recovery
systemctl start sub2api-updater
```

若选择人工回退，确认原服务与数据恢复正确后，把失败任务目录完整归档到状态目录之外，再启动更新服务；不要删除备份，也不要未经验证修改任务为成功。

## 维护

宿主机更新器独立于应用镜像，修改该脚本需要单独安装并重启 systemd 服务，应在无活动更新任务时操作。发布清单格式为 schema 1，后续变更须保持兼容。

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/updater -p 'test_*.py'
```
