# 部署与更新

[返回 README](../README.md) · [使用指南](user-guide.md)

本页介绍镜像部署、源码构建、更新迁移与数据保存。

## 直接使用 GHCR 镜像

镜像发布地址为 `ghcr.io/wizcheu/v2echo-notifier`，支持 `linux/amd64` 和 `linux/arm64`，Docker 会按宿主机架构选择对应镜像。首次正式版本构建成功并将镜像包设为 Public 后，即可匿名拉取，无需下载源码或自行构建。

在部署目录创建 `compose.yaml`：

```yaml
services:
  notifier:
    image: ghcr.io/wizcheu/v2echo-notifier:latest
    restart: unless-stopped
    ports:
      - "8282:8282"
    volumes:
      - notifier-data:/data
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL

volumes:
  notifier-data:
```

启动并读取管理密钥：

```sh
docker compose pull
docker compose up -d
docker compose exec notifier /notifier -data /data -print-admin-token
```

打开 `http://<宿主机地址>:8282` 登录。`latest` 指向最近发布的正式版本；需要固定版本时，将标签替换为已发布版本（例如 `0.1.0`）。`edge` 是手动构建默认使用的测试标签；默认不更新 `latest`，发布者可在运行工作流时明确选择更新。

如果拉取提示 `manifest unknown`，说明指定标签可能尚未发布。只做过默认手动构建时可先使用 `ghcr.io/wizcheu/v2echo-notifier:edge`，或由维护者按[镜像发布说明](development.md#发布容器镜像)发布 `latest`。以成功工作流摘要中的拉取命令为准。

更新镜像时再次执行 `docker compose pull` 和 `docker compose up -d`。从源码构建迁移时，在原 `compose.yaml` 中用 `image:` 替换整个 `build:` 块；保留原 Compose 项目名称、部署目录和 `notifier-data:/data` 卷映射，继续使用原有配置与历史。不要执行会删除数据卷的 `docker compose down -v`。

## 从源码构建

仓库自带的 `compose.yaml` 使用本地 Dockerfile，适合开发或自定义构建。在仓库目录执行：

```sh
docker compose up -d --build
docker compose exec notifier /notifier -data /data -print-admin-token
```

如果下载 Go 依赖超时，可通过 `GOPROXY` 指定可访问的模块镜像，例如 [Goproxy.cn](https://goproxy.cn/)：

```sh
GOPROXY=https://goproxy.cn,direct docker compose up -d --build
```

也可在 Compose 同目录的 `.env` 中设置 `GOPROXY=https://goproxy.cn,direct`，以后直接执行 `docker compose up -d --build`。未设置时使用 Go 官方代理；模块校验保持启用。该设置只影响 Go 构建阶段的模块下载。

构建期间的 `npm ci` 和 `go mod download` 可通过 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` 使用代理。Compose 从当前终端或同目录 `.env` 读取这些值，并作为 Docker 的预定义构建参数传入。例如，将下面的示例地址替换为构建容器可访问的 HTTP 代理地址：

```sh
HTTP_PROXY=http://192.168.1.100:7890 \
HTTPS_PROXY=http://192.168.1.100:7890 \
docker compose up -d --build
```

代理运行在另一台电脑上时，需允许局域网访问，并使用该电脑的局域网 IP；构建容器中的 `127.0.0.1` 指向容器自身。`HTTPS_PROXY` 的值可使用 `http://`，表示通过 HTTP 代理隧道访问 HTTPS。这些参数用于构建步骤；拉取基础镜像所需的代理应另行配置到 Docker daemon 或 BuildKit，运行中的服务也不自动继承构建代理。详见 [Docker 构建代理参数](https://docs.docker.com/build/building/variables/#proxy-arguments)。

## 数据与访问

上述两种部署方式均使用 Docker 命名卷持久化 `/data`，通过 `8282:8282` 将容器端口发布到宿主机，可在局域网访问 `http://<宿主机地址>:8282`。也可通过 SSH 转发访问：

```sh
ssh -L 8282:127.0.0.1:8282 user@your-server
```

也可在用户自己的 HTTPS 反向代理后运行，并设置 `ECHO_SECURE_COOKIE=true`。代理需保留原 Host；接口校验 Origin 与自定义请求头。不要把未加密的管理接口直接暴露到公网。

根目录 `data/accounts.db` 保存账号索引与共享请求额度；每个账号的数据存入 `data/accounts/<随机配置ID>/notifier.db`，PAT、网页 Cookie 与推送凭据使用该目录下的 `encryption.key` 做 AES-GCM 加密。此加密不防止拿到整个数据目录的攻击者；备份必须保护整个卷，凭据文件不应加入源码或日志。单个数据目录只运行一个助手进程。当前账号数据库格式为 4，不自动转换旧开发版数据库；格式不兼容时请使用新的数据目录。

## 管理密钥与登录

服务日志只显示密钥文件的位置，不显示密钥本身。首次创建的密钥保存在 `data/admin-token`；管理会话只存于浏览器的 HttpOnly、SameSite=Strict Cookie，12 小时过期，程序重启后需重新登录。

Docker 部署时，数据目录对应容器中的 `/data`，管理密钥位于 `/data/admin-token`。忘记密钥时重新运行本页的 `-print-admin-token` 命令读取原值，不需要删除数据卷。
