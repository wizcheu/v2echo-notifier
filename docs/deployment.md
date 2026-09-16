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

## NAS 浏览器验证

默认只部署 notifier，无需下载 browser。可选浏览器容器基于 LinuxServer Chromium / Selkies，提供网页远程操作窗口；NAS 无需桌面或显示器，需要能运行 Linux `amd64` / `arm64` 容器，并为 Chromium 留出额外内存。

当实际首页检查遇到 403/CF 挑战时，管理页提供安装引导。新账号只要 API 身份已确认，可以先保存为暂停同步的待网页验证账号；安装组件后继续验证，无需重新填写凭据。匿名代理测试不使用浏览器会话，它单独返回 403 不代表实际检查也失败。

### 使用已发布镜像按需安装

notifier 镜像为 `ghcr.io/wizcheu/v2echo-notifier`，browser 为独立镜像 `ghcr.io/wizcheu/v2echo-notifier-browser`。两者必须使用包含浏览器支持且配套的发布版本。发布者须先构建并公开 browser 镜像；以发布工作流摘要中的实际标签为准，不要假定已有 `latest` 包含此功能。

直接按照 [README 的完整配置](../README.md#按需安装-browser)，在原 `compose.yaml` 中加入 browser 服务以及 notifier 的连接环境变量，无需下载额外的 Compose 文件。保留原部署目录、项目名称和数据卷。在同目录 `.env` 中设置：

- `ECHO_BROWSER_TOKEN`：至少 32 字符的随机值，可用 `openssl rand -hex 32` 生成，两个容器共用。
- `ECHO_BROWSER_IMAGE`：配套的已发布 browser 镜像；未设置时使用 `ghcr.io/wizcheu/v2echo-notifier-browser:latest`。

```sh
docker compose pull
docker compose up -d
```

这会直接拉取镜像、增加 browser 容器，并重建 notifier 容器以应用内部连接配置；账号与历史保留在原数据卷。首次接入时须使用支持浏览器功能的 notifier 版本。之后刷新管理页，打开验证窗口，人工验证完成后点击“验证完成，重试首页”。待验证新账号仍需配对并主动开启同步。

browser 需要持续运行，后续首页检查使用同一浏览器会话；无需对公网发布它的任何端口。

已有多文件部署也可继续使用 [compose.browser.image.yaml](../compose.browser.image.yaml) 覆盖配置，此时启动和更新命令需保留 `-f compose.yaml -f compose.browser.image.yaml`。单文件与多文件部署二选一即可。

### 单独构建 browser 或从源码启用

在源码仓库根目录可只构建 browser，不构建 notifier：

```sh
docker build -t v2echo-notifier-browser:local ./browser
```

在上面的镜像部署方式中设置 `ECHO_BROWSER_IMAGE=v2echo-notifier-browser:local`，即可使用本地构建结果，此时跳过镜像拉取步骤。维护者也可在发布工作流选择 `component=browser` 单独构建、发布多架构镜像。

完整源码部署继续使用原来的 `compose.browser.yaml`：

在源码目录的 `.env` 添加 `ECHO_BROWSER_TOKEN`，使用至少 32 字符的随机值（可用 `openssl rand -hex 32` 生成），文件权限设为仅当前用户可读写。该密钥用于主服务与浏览器控制接口之间的认证，不是管理页登录密钥，不要提交到版本控制。

```sh
docker compose -f compose.yaml -f compose.browser.yaml up -d --build
```

已有数据时继续使用原 Compose 项目名称和 `notifier-data` 卷。浏览器容器不发布端口，也不挂载 Docker socket。访问窗口仍使用主服务的 `/browser-desktop/` 路径，需要登录管理页并先点击账号中的“打开验证窗口”；窗口绑定此次管理会话，有效期 10 分钟，验证成功、退出登录或修改代理/Cookie 后结束。

**管理页需要通过 HTTPS 访问**（本机 localhost 为浏览器安全上下文例外）。Selkies 的流式显示需要安全上下文及 WebSocket。已有反向代理必须保留 Host，并转发升级连接，例如 Nginx：

```nginx
location /browser-desktop/ {
    proxy_pass http://127.0.0.1:8282;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 660s;
    proxy_send_timeout 660s;
    proxy_buffering off;
}
```

此片段放在已有 HTTPS `server` 内；普通管理页及 `/api/` 继续代理到主服务。不要将 Selkies 3000/3001、浏览器控制接口 8090 或 Chromium 调试端口 9222 暴露给公网。独立配置时，主服务读取 `ECHO_BROWSER_CONTROL_URL`、`ECHO_BROWSER_DESKTOP_URL` 和 `ECHO_BROWSER_TOKEN`；前两个地址是容器内部地址，用户浏览器无需能够访问它们。

浏览器使用当前账号已保存的代理，包含 HTTP/HTTPS 代理认证；环境变量模式由主服务按首页 URL 解析为实际代理后传入。使用局域网可达的代理地址，不要使用指向主容器自身的 `127.0.0.1`。代理失败不回退直连。浏览器控制与桌面连接不经过该代理。

当前实现同一时刻运行一个浏览器、操作一个账号。人工验证期间其他账号的浏览器首页读取等待后续重试，普通 Go 检查不受此限制。各账号的 Cookie 单独加密保存于原账号数据库；活动 Chromium 配置和缓存仅放在 tmpfs，切换账号时重建配置，恢复该账号加密保存的 Cookie。持久化 Cookie 不保证重启后 CF 不再挑战。验证窗口限制文档导航到首页和 CF 验证相关页面，并关闭额外标签页，不提供通知列表浏览或已读操作。

关闭功能前，在已启用的账号中点击“关闭兼容模式”，再移除 Compose 覆盖配置。活动会话需要容器在线才能清理。浏览器服务故障时已启用账号会显示错误，不会悄悄改回普通请求；默认 Go 模式账号继续运行。

部署验收应使用自己的代理，在窗口中手动完成一次验证，确认“重试首页”读到同账号未读数，并观察后续定时检查及容器重启后的恢复。自动化测试使用合成响应，不能证明某个代理能通过 Cloudflare；直接封禁、反复挑战或出口 IP 轮换仍可能导致失败。上游参考：[Chromium 容器](https://docs.linuxserver.io/images/docker-chromium/)、[Selkies 反向代理](https://docs.linuxserver.io/selkies/user-guide/reverse-proxy/)。
