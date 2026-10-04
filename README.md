# AI Proxy - 本地 OpenAI 接口负载均衡代理

一个轻量、高效的 Windows 本地反向代理工具，用于在多个 OpenAI 兼容接口之间实现**流量轮询分摊（Load Balancing）**、**认证头覆盖重写**与**自定义 Header 注入**。

遵循**完全云端 GitHub Actions 构建**规范，本地无需安装 Go 或任何编译环境，直接在 GitHub Releases / Artifacts 下载即用。

---

## 核心特性

- 🔄 **平滑加权轮询 (Smooth Weighted Round-Robin)**：支持配置多个上游 OpenAI / DeepSeek / 自建中转节点，按权重均匀分摊并发流量，避免单节点限流（Rate Limit）。
- 🔑 **认证 Header 覆盖重写**：每个上游节点可配置其专属的 API Key（如 `Authorization: Bearer sk-xxx`），由本地代理统一自动改写上游认证，对客户端透明。
- 🌐 **全量 Header 透传 & 自定义覆写**：客户端的请求头（如 `Content-Type`, `User-Agent` 等）完整透传，同时支持按节点覆写或注入任意自定义 Header（例如 `HTTP-Referer`, `X-Title` 等）。
- ⚡ **原生流式响应 (SSE / Streaming)**：对 OpenAI `stream: true` 打字机流式响应即时刷新（Immediate Flush），零延迟无缓冲。
- 🛡️ **故障自动转移 (Failover Retry)**：上游节点网络中断、超时或返回 502/503/504 时，代理自动尝试下一个可用节点。
- 📊 **实时状态监控**：访问 `http://127.0.0.1:8080/_proxy/status` 即可查看各节点调用次数、成功率、最后响应时间与当前权重分配。
- 🪟 **原生 Windows 支持**：提供 `start.bat` 一键启动脚本，单文件可执行文件，内存占用极低（< 15MB）。

---

## 配置文件说明 (`config.yaml`)

程序启动时会自动读取同目录下的 `config.yaml`：

```yaml
server:
  host: "127.0.0.1"    # 本地监听地址（若需要局域网其他设备访问可设为 0.0.0.0）
  port: 8080           # 本地监听端口
  verbose: true        # 是否在控制台打印请求轮询明细
  retry_count: 2       # 单次请求失败时最大故障转移重试次数
  client_key: ""       # 可选：如果希望本地客户端访问此代理也需要认证，可填写

upstreams:
  # 节点 1：OpenAI 官方接口
  - name: "openai-node"
    base_url: "https://api.openai.com/v1"
    weight: 1                  # 轮询权重
    enabled: true
    headers:
      Authorization: "Bearer sk-proj-upstream-key-1"

  # 节点 2：DeepSeek 官方或中转
  - name: "deepseek-node"
    base_url: "https://api.deepseek.com/v1"
    weight: 1
    enabled: true
    headers:
      Authorization: "Bearer sk-upstream-key-2"

  # 节点 3：高权重第三方网关节点（支持自定义 Header 覆盖）
  - name: "gateway-node"
    base_url: "https://gateway.ai.cloudflare.com/v1/xxx/xxx"
    weight: 2                  # 权重更高，分配的流量更多
    enabled: true
    headers:
      Authorization: "Bearer sk-upstream-key-3"
      HTTP-Referer: "https://my-app.local"
      X-Title: "My-AI-App"
```

---

## 使用指南

### 1. 获取程序（GitHub Actions 云端构建）

本项目所有二进制构建均由 GitHub Actions 在云端完成，**本地不需要编译**：
1. 访问本仓库的 GitHub 页面中的 **Actions** 标签页。
2. 点击最新的工作流运行记录（Workflow run）。
3. 在页面底部的 **Artifacts** 区域，下载 `ai-proxy-windows-amd64` 压缩包。
4. 将压缩包解压到你喜欢的本地目录（例如 `D:\ai-proxy`）。

### 2. 配置与启动

1. 进入解压后的目录，将 `config.example.yaml` 复制为 `config.yaml`（或者直接双击 `start.bat` 也会自动生成）。
2. 用记事本或 VS Code 打开 `config.yaml`，填入你的 OpenAI 兼容 BaseURL 和各个节点的 API Key。
3. 双击 `start.bat` 启动代理！
4. 控制台将显示各上游节点的加载信息与本地监听端口（默认 `http://127.0.0.1:8080`）。

### 3. 客户端对接

在任何支持配置 OpenAI Base URL 的软件（如 NextChat, Chatbox, Cursor, Continue, 沉浸式翻译, Lobechat 等）中配置：

- **API 地址 (Base URL)**: `http://127.0.0.1:8080/v1`（或 `http://127.0.0.1:8080`）
- **API Key**: 任意输入（因为 `config.yaml` 中配置的 upstream headers 会自动将其覆盖为真实的 Key；若在 `server.client_key` 中设置了验证密码，则填入该密码）。

### 4. 查看运行监控

在浏览器中打开：
```
http://127.0.0.1:8080/_proxy/status
```
将返回 JSON 格式的节点健康状况与请求统计。

---

## GitHub Actions 自动构建与版本发布

- **自动编译**：每次推送代码至 `main` 分支，GitHub Actions 均会自动启动并构建最新版 Windows amd64 与 arm64 二进制。
- **发布 Release**：为代码打上标签（如 `v1.0.0`）推送到 GitHub 时，Actions 会自动创建 GitHub Release 并附带编译好的 `.zip` 文件。
