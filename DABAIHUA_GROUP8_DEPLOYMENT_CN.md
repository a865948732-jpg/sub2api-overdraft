# 大白话 CTF 精选版：穿山甲组部署说明

这不是把 `D:\2\ctf\dabaihua-pojia` 原目录复制进服务器。该资料包包含越狱提示、自动安装器、MCP 注册、载荷和绕过内容，不能原样注入生产。仓库只提供一个经过筛选的防御性提示词：用于授权 CTF、代码审计、静态逆向和取证，默认离线、不执行 Shell、不安装依赖、不注册 MCP、不扫描外部目标。

## 目标

- 目标分组：`穿山甲组`
- 当前已核实分组 ID：`8`
- 只对 API Key 的 `group_id=8` 注入
- 其他分组、未分组请求保持原样
- 提示词文件以只读方式挂载，应用启动时读取并缓存

## 当前服务器拓扑

- 源码与构建目录：`/root/sub2api-overdraft-src`
- 生产 Compose 目录：`/root/sub2api-deploy`
- 运行镜像：`sub2api-overdraft:local`

两个目录职责不同。源码目录只构建应用镜像；生产目录只重建 `sub2api` 服务。不要从源码目录启动 PostgreSQL 或 Redis。

## 应用补丁并构建镜像

补丁上传到 `/root/sub2api-dabaihua-group8-v0.1.176.patch` 后执行：

```bash
cd /root/sub2api-overdraft-src
git apply --check /root/sub2api-dabaihua-group8-v0.1.176.patch
git apply --whitespace=nowarn /root/sub2api-dabaihua-group8-v0.1.176.patch
test -f deploy/prompts/dabaihua-defensive-ctf.md

docker compose \
  -f deploy/docker-compose.local.yml \
  -f deploy/docker-compose.overdraft.yml \
  build sub2api
```

构建前必须先把当前运行容器的镜像另打 `sub2api-overdraft:rollback-pre-dabaihua-20260815` 标签。该标签应指向当前源码构建版，不要使用旧的 `rollback-release-20260815`；旧标签会重新显示更新按钮。

## 生产目录配置

构建成功后，把覆盖文件和提示词复制到现有生产目录：

```bash
install -d -m 0755 /root/sub2api-deploy/prompts
install -m 0644 \
  /root/sub2api-overdraft-src/deploy/prompts/dabaihua-defensive-ctf.md \
  /root/sub2api-deploy/prompts/dabaihua-defensive-ctf.md
install -m 0644 \
  /root/sub2api-overdraft-src/deploy/docker-compose.dabaihua.yml \
  /root/sub2api-deploy/docker-compose.dabaihua.yml
```

先检查三份 Compose 合并后的镜像、环境变量和只读挂载，再只重建应用容器：

```bash
cd /root/sub2api-deploy
docker compose \
  -f docker-compose.yml \
  -f docker-compose.overdraft.yml \
  -f docker-compose.dabaihua.yml \
  config

docker compose \
  -f docker-compose.yml \
  -f docker-compose.overdraft.yml \
  -f docker-compose.dabaihua.yml \
  up -d --no-deps --force-recreate --pull never sub2api
```

正式切换脚本必须检查 `up` 返回码、限时等待 `healthy`、请求 `/health`，任一步失败都要把 `rollback-pre-dabaihua-20260815` 重新标记为 `sub2api-overdraft:local`，并按部署前两份 Compose 重建应用容器。

切换前至少备份 `config.yaml`、数据库、两份现有 Compose 文件、合并后的 Compose 配置和当前镜像 ID；PostgreSQL dump 必须用 `test -s` 确认为非空。不要使用 `docker compose down -v`，也不要执行 `docker system prune`。

## 验收

1. 容器健康检查为 `healthy`，重启次数为 0。
2. 启动日志没有 `read openai group prompt file` 或 `apply openai group prompt` 错误。
3. 用穿山甲组的 API Key 调用 `/v1/responses`、`/v1/chat/completions` 或 `/v1/messages`，检查防御性模式是否生效。
4. 用其他分组 API Key 做同样请求，确认行为未被改变。
5. WebSocket 的首轮和后续 `response.create` 都应保持相同规则。

普通 API 响应看不到上游请求体，因此仅凭模型回复不能证明标记被精确注入。精确验证需要受控上游捕获；没有该证据时，只能声称配置、代码路径和外部行为已验证。

这份提示词是模型行为约束，不是技术隔离。若要真正禁止 Shell、联网或工具调用，还需要容器权限、网络策略和工具白名单。

不要在日志、命令回显或测试文件中粘贴 API Key、Cookie、JWT、数据库密码或上游凭据。出现错误时先切回部署前保存的镜像标签和配置备份，再分析日志。
