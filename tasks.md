# graph-log-watcher MVP 开发任务

本文档依据 [`graph-log-watcher 设计方案.md`](./graph-log-watcher%20设计方案.md) 拆分 MVP 开发工作。每个任务原则上对应一个可独立评审和合并的 Pull Request；每次合并后，主分支都必须保持可构建、可测试。

## 使用约定

- 状态使用任务标题前的复选框维护；PR 合并后将对应任务标记为完成。
- 每个 PR 只完成对应任务中列出的能力，不提前混入后续任务。
- 所有 PR 必须通过 `go test ./...`；涉及并发的 PR 还必须通过 `go test -race ./...`。
- 新增行为必须随 PR 提供单元测试或集成测试，不能把测试统一留到最后一个 PR。
- 模块路径固定为 `github.com/wanliqun/congraph-log-watcher`，最低 Go 版本为 1.23。
- 默认只持久化 checkpoint。聚合窗口、cooldown 和 context 是进程内状态；重启后遵循 At-Least-Once 语义，允许少量重复处理或重复通知，但不能永久漏日志。

## 接口边界

下列边界应在对应 PR 中建立，并在后续任务中保持稳定：

```go
type Parser interface {
	Parse(RawLog) LogEntry
}

type LogSource interface {
	Run(ctx context.Context, starts map[string]ReplayStart, output chan<- RawLog) error
}

type StateStore interface {
	LoadCheckpoint(container string) (*ContainerCheckpoint, error)
	SaveCheckpoint(container string, checkpoint ContainerCheckpoint) error
	Flush() error
	Close() error
}

type Notifier interface {
	Notify(context.Context, Alert) error
}
```

Source-independent Processor 接收 `RawLog`，输出 checkpoint ack 和 `Alert`；CLI 固定提供 `run`、`check-config`，HTTP 固定提供 `GET /metrics`、`GET /healthz`。

---

## [ ] PR-01：仓库脚手架与核心数据契约

**优先级：** P0\\
**前置依赖：** 无

### 目标

建立可持续迭代的 Go 工程骨架和跨模块共享的数据模型，使后续配置、Parser、Rule、Docker Source 等任务可以并行开发。

### 交付内容

- 初始化 `github.com/wanliqun/congraph-log-watcher` Go module，声明最低 Go 1.23。
- 建立设计方案约定的 `cmd/graph-log-watcher` 和 `internal/*` 目录结构；只创建当前 PR 实际需要的包。
- 提供最小可编译的命令入口。
- 定义 `RawLog`、`LogEntry`、`LogLevel` 及等级字符串转换、解析接口。
- `LogEntry.Timestamp` 明确表示 Docker API 时间戳；graph-node App Timestamp 不作为 checkpoint 时间。
- 建立格式化、静态检查、单元测试及 race test 的 CI 基线。

### 验收条件

- `go build ./...`、`go vet ./...`、`go test ./...` 和 `go test -race ./...` 全部通过。
- `ERRO`/`ERROR`、`CRIT`/`CRITICAL`、`DEBG`/`DEBUG`、`TRCE`/`TRACE` 可映射到统一内部等级。
- 仓库中没有尚未使用的大型运行时依赖或空实现业务模块。

### 测试要求

- 表驱动测试覆盖所有合法等级别名、大小写策略和未知等级。
- CI 在 Linux 环境执行 build、vet、test 和 race test。

---

## [ ] PR-02：配置加载、默认值及 `check-config`

**优先级：** P0\\
**前置依赖：** PR-01

### 目标

把设计方案中的 YAML 配置固化为可验证的运行时契约，并在服务启动前快速发现配置错误。

### 交付内容

- 实现 Docker、containers、parser、levels、checkpoint、aggregation、context、redaction、rules、alert、notifier、metrics 配置模型。
- 支持配置值中的 `${ENV_VAR}` 展开；引用但未设置的环境变量必须报错，错误信息不得打印敏感值。
- 实现设计方案默认值，包括 replay overlap、checkpoint flush、group limits、context、队列容量和 metrics 地址。
- 补充并固定以下默认值：
  - Docker 不可用健康阈值：1 分钟。
  - 所有目标容器无法 attach 健康阈值：1 分钟。
  - notifier storm cooldown：1 分钟。
  - graceful shutdown timeout：10 秒。
  - log channel：10000；notification queue：100。
- 启动时预校验 invalid regexp、unknown level、非正 window/threshold、负 cooldown、重复 rule ID、unknown dedup mode、fields mode 缺少 fields、非法 duration 和非正队列容量。
- 实现 `graph-log-watcher check-config --config <path>`；合法配置退出码为 0，加载或校验失败使用非 0 退出码。

### 验收条件

- 配置错误一次性返回带字段路径的可操作错误，不通过 panic 或 `log.Fatal` 终止库代码。
- 默认配置能够通过校验；缺少目标容器、无有效规则或缺少必需通知配置时给出明确结果。
- `check-config` 与后续 `run` 共用同一套加载、默认值及校验逻辑。

### 测试要求

- 覆盖所有设计方案列出的失败条件、默认值和环境变量展开。
- CLI 测试覆盖成功、文件不存在、YAML 非法、语义校验失败及稳定退出码。

---

## [ ] PR-03：graph-node Parser 与 Level Router

**优先级：** P0\\
**前置依赖：** PR-01

### 目标

把 Docker 原始日志转换为统一 `LogEntry`，并在进入复杂 Rule Matching 前按日志等级完成低成本路由。

### 交付内容

- 实现 `Parser.Parse(RawLog) LogEntry` 和 graph-node Parser。
- 提取 level、message、component、subgraph_id、block_number、sgd 及其他 `key: value` 字段。
- 保留 Docker timestamp、container ID/name、stream 和原始日志。
- App Timestamp 只用于展示或诊断，不覆盖 Docker timestamp，也不参与 replay/checkpoint。
- 无法识别格式时返回 `LevelUnknown`、原始 message 和 raw，不丢弃日志。
- 实现可配置 Level Router：TRACE 丢弃；DEBUG 可选进入 Context；INFO 仅进入 Context；WARN/ERROR/CRITICAL 进入 Context 和 Rule Engine。

### 验收条件

- INFO、WARN、ERRO/ERROR、CRIT/CRITICAL、DEBG/DEBUG、TRCE/TRACE 均能正确识别。
- 常见 graph-node 字段能够从示例日志中稳定提取。
- 大量 INFO 不会进入 Rule Engine；UNKNOWN 只有被显式配置的规则才可能匹配。

### 测试要求

- Parser 表驱动测试覆盖各等级、字段解析、空字段、异常行和 Docker stream 元数据。
- Level Router 测试覆盖默认路由及 DEBUG/UNKNOWN 的配置变化。
- 增加 Parser fuzz test，保证任意输入不 panic、不丢失 raw。

---

## [ ] PR-04：Redaction 与 Context Buffer

**优先级：** P0\\
**前置依赖：** PR-02、PR-03

### 目标

确保敏感信息在进入任何长生命周期状态前被清洗，并为告警提供有界、按容器隔离的前后文。

### 交付内容

- 编译并执行配置化 Redaction 规则，同时清洗 `Raw`、`Message` 和 `Fields`。
- 固定处理顺序为 Parse → Redact → Context/Dedup/State/Notifier。
- 实现每个容器独立的固定容量 ring buffer。
- 支持 before、after、after_wait、max_bytes 和 buffer eviction。
- 提供 after-context 收集能力：满足 after 行数或达到 after_wait 时完成，先到者为准。
- 删除容器状态时能够回收其 buffer 和未完成的 context collector。

### 验收条件

- API key、Bearer token、RPC credential 等匹配内容不会出现在 Context、Samples、Fingerprint 输入、状态日志或通知对象中。
- 一个容器的日志不会出现在另一个容器的 Context。
- `after > 0` 不阻塞中央事件处理器；等待时间始终受 `after_wait` 限制。

### 测试要求

- 覆盖 before/after、超时、字节截断、buffer 淘汰、容器隔离和并发 append/snapshot。
- 使用 canary secret 断言所有对外对象和内存快照均不含原始敏感值。

---

## [ ] PR-05：Rule Matcher、Normalization 与 Fingerprint

**优先级：** P0\\
**前置依赖：** PR-02、PR-03

### 目标

实现确定、可配置的异常分类和 Incident Identity 计算。

### 交付内容

- 按 Container → Level → Component → FieldEquals → Pattern → Excludes 的固定顺序匹配。
- 按配置顺序执行规则，支持 `enabled` 和 `stop_on_match`。
- Pattern 和 Excludes 只用于错误分类或排除，不用于重新推断日志等级。
- 实现 exact、normalized、fields、rule 四种 Dedup 模式。
- normalization 按配置顺序依次替换；所有正则在配置加载阶段预编译。
- 使用稳定哈希计算 fingerprint，输入至少包含 rule ID、逻辑 container name 和对应 Dedup identity。
- fields mode 支持 `rule`、`container` 等保留字段及 Parser 提取的结构化字段；字段序列必须确定。

### 验收条件

- 同一输入在不同进程中生成相同 fingerprint。
- fields mode 下 block_number 不同但 rule/component/subgraph 相同的日志归为同一 Incident。
- 不同 subgraph、container 或 rule 生成不同 fingerprint。
- specific rule 配置在 generic rule 前并设置 `stop_on_match` 时，不会重复计入 generic rule。

### 测试要求

- 覆盖每个匹配阶段、排除规则、规则顺序、禁用规则和 `stop_on_match`。
- 覆盖四种 Dedup mode、normalization 顺序、字段缺失和 fingerprint 稳定性。

---

## [ ] PR-06：Sliding Window、Cooldown 与有界聚合状态

**优先级：** P0\\
**前置依赖：** PR-02、PR-05

### 目标

把同类日志聚合成少量 Incident Alert，并在持续异常和高基数输入下保持可预测的内存占用。

### 交付内容

- 按 fingerprint 维护 `EventState`、滑动时间戳 deque、FirstSeen、LastSeen 和 TotalCount。
- 每次事件先淘汰 `< eventTime-window` 的时间戳，再追加并判断 threshold。
- 首次达到 threshold 时告警；cooldown 内继续统计并增加 suppressed count。
- cooldown 到期后，只要当前窗口仍满足 threshold，下一条事件生成新告警并携带自上次告警后的 suppressed count；发送后重置该计数。
- 保存 first、latest 和有限个不同 sample，总数不超过 `max_samples`。
- 定期清理超过 `group_ttl` 的组；达到 `max_groups` 时先清理过期组，再淘汰 LastSeen 最旧的组。
- 注入 Clock，避免测试依赖真实时间。

### 验收条件

- threshold、窗口边界和 cooldown 行为与设计场景一致。
- 活跃组数量永远不超过 `max_groups`，sample 数量永远不超过 `max_samples`。
- 单个高频 fingerprint 不会导致时间戳队列保留窗口外数据。

### 测试要求

- 覆盖 threshold 前后、滑动过期、精确边界、cooldown 抑制和到期再告警。
- 覆盖 suppressed count、sample 去重、TTL 清理、容量淘汰和乱序保护。

---

## [ ] PR-07：Source-independent Detection Pipeline 与 Dry Run

**优先级：** P0\\
**前置依赖：** PR-03、PR-04、PR-05、PR-06

### 目标

在不依赖 Docker、bbolt 或 DingTalk 的情况下完成核心检测状态机，便于独立测试和复用。

### 交付内容

- 串联 Parse → Redact → Context Append → Level Route → Rule Match → Fingerprint → Aggregate → Alert。
- 定义完整 `Alert` 模型，包含 rule、severity、container、level、component、subgraph、fingerprint、时间、窗口计数、总数、suppressed count、samples 和 context。
- 每个已消费 RawLog 都产生 checkpoint ack，不论等级、解析结果、是否匹配规则或是否触发告警。
- INFO 只更新 Context；TRACE 默认丢弃；UNKNOWN 仅参与显式 UNKNOWN rule。
- 实现 after-context 延迟组装，不阻塞后续日志处理。
- 实现 Dry Run alert sink，稳定输出 `WOULD ALERT`、rule、container、fingerprint 等字段，同时仍执行 window、dedup 和 cooldown。

### 验收条件

- 核心 Processor 不导入 Docker SDK、bbolt 或 go-conflux-util。
- Dry Run 不构造或调用任何外部 notification channel。
- 通知结果不参与 checkpoint ack 判定。
- 相同输入和可控时钟产生确定的 Alert 序列。

### 测试要求

- 覆盖大量 INFO、偶发 ERROR、ERROR burst、CRIT、持续 ERROR、cooldown 后继续错误六个核心场景。
- 覆盖未匹配日志仍 ack、after-context 到齐/超时及 Dry Run 无外部调用。

---

## [ ] PR-08：Docker LogSource、容器发现及重建检测

**优先级：** P0/P1\\
**前置依赖：** PR-01、PR-02、PR-03

### 目标

通过 Docker Engine API 持续读取指定 graph-node 容器日志，并自动处理断线和容器重建。

### 交付内容

- 使用官方 Docker Go SDK 实现 `LogSource`。
- 按配置的逻辑容器名执行 list/inspect，统一处理 Docker 返回的 `/name` 格式。
- attach 日志时启用 stdout、stderr、follow 和 timestamps，并接受每个容器的 replay 起点。
- 正确处理非 TTY multiplexed stream 和 TTY plain stream。
- 每个容器一个 stream goroutine，统一写入容量 10000 的中央 bounded channel；队列满时阻塞读取形成背压，不静默丢日志。
- 监听 create/start/die/destroy 事件，stream 结束或 Docker 暂时不可用时进行有上限的指数退避重连。
- 容器 ID 变化后停止旧 stream 并自动 attach 新容器。
- 客户端代码只调用 container list、inspect、logs 和 events，不提供 exec/stop/kill/remove/create 能力。

### 验收条件

- index-node-0 和 query-node-0 可独立 attach、断线和恢复。
- 同一容器内日志顺序保持不变；不同容器之间不承诺全局顺序。
- Docker API 短暂失败不会终止整个 watcher，取消 context 后所有 goroutine 能退出。

### 测试要求

- 使用 fake Docker HTTP API 覆盖发现、日志流、stdout/stderr、TTY/multiplex、断线重连和事件订阅。
- 覆盖容器 ID A → B 的重建流程、队列背压及取消时无 goroutine 泄漏。

---

## [ ] PR-09：bbolt Checkpoint、Replay 与 Container Recreate 恢复

**优先级：** P1\\
**前置依赖：** PR-02、PR-08

### 目标

在 watcher crash/restart 或容器重建后恢复日志消费，实现 At-Least-Once 且尽量减少 overlap 重复。

### 交付内容

- 使用 bbolt 实现 `StateStore`，默认路径 `/var/lib/graph-log-watcher/state.db`。
- `ContainerCheckpoint` 持久化逻辑 container name、ContainerID、LastTimestamp 和 LastEventHash；逻辑名称是数据库主键。
- checkpoint 写入先缓存在内存中，达到 `flush_events` 或 `flush_interval` 任一条件时提交事务。
- shutdown/Close 强制 flush，写入失败必须向服务层报告并记录健康状态。
- 启动或重连时从 `LastTimestamp - replay_overlap` 请求日志。
- replay gate 在同一 ContainerID 中跳过日志直到找到 `(LastTimestamp, LastEventHash)` marker，再处理其后的日志；marker 缺失时处理整个 overlap，保证不漏日志。
- ContainerID 改变时不等待旧 marker，直接消费新容器在 replay 起点后的日志。
- checkpoint 由 Processor ack 推进，与 Rule Match、Alert 生成和通知成功完全解耦。

### 验收条件

- 正常重启能跳过已确认边界并处理停机期间日志。
- marker 因轮转丢失时允许重复处理，但不会跳过不确定日志。
- 同名容器 ID 改变后无需人工删除 state 或重启 watcher。
- bbolt 文件不存储原始日志或敏感字段。

### 测试要求

- 使用临时 bbolt 覆盖 save/load、事件数 flush、定时 flush、shutdown flush 和损坏/不可写错误。
- 集成测试覆盖 crash/restart、overlap marker 命中/缺失、相同 timestamp 多行日志和容器重建。

---

## [ ] PR-10：异步通知、DingTalk Adapter 与 Safety Valve

**优先级：** P2\\
**前置依赖：** PR-02、PR-07

### 目标

可靠地把 Alert 发送到 DingTalk，同时确保外部通知故障或错误规则不会阻塞日志消费或制造告警风暴。

### 交付内容

- 保留项目内 `Notifier.Notify(context.Context, Alert) error` 抽象。
- 直接构造 `go-conflux-util/alert` 的 DingTalk channel 和 formatter，适配 `Channel.Send(ctx, *Notification)`，不依赖其全局 Viper 初始化。
- 实现 low/medium/high/critical severity 映射和设计方案中的 DingTalk Markdown 内容。
- 支持 webhook、secret、atMobiles、isAtAll 和 custom tags；日志中不得输出 webhook 或 secret。
- Alert 进入容量 100 的 bounded queue，由独立 worker 发送。
- 首次发送失败后按 1s、2s、5s、10s 重试；全部失败后记录错误、增加 metric 并丢弃该通知。
- queue 满时不阻塞日志消费：记录错误、增加 dropped/error metric 并丢弃新通知。
- 实现默认每分钟 20 条的 safety valve 和 1 分钟 storm cooldown；超过限制时停止普通告警，每个 cooldown 周期最多 best-effort 发送一次 `Alert storm detected`。

### 验收条件

- DingTalk 不可用不会阻塞 Processor 或 checkpoint。
- Dry Run 路径不初始化 DingTalk channel。
- 关闭时 notifier 在服务 timeout 内排空；超时后取消在途请求并记录未发送数量。
- storm notification 本身不会递归触发限流。

### 测试要求

- 使用 fake channel 覆盖格式化、severity、成功、各次重试、最终失败、queue 满和 shutdown。
- 使用可控时钟覆盖每分钟限额、storm cooldown 及恢复正常发送。

---

## [ ] PR-11：Prometheus、Health Check 与结构化日志

**优先级：** P3\\
**前置依赖：** PR-07、PR-08、PR-09、PR-10

### 目标

让 watcher 的吞吐、检测、通知、checkpoint 和 Docker 连接状态可观测，并提供编排系统可用的健康检查。

### 交付内容

- 提供以下指标：
  - `graph_log_watcher_logs_received_total{container,level}`
  - `graph_log_watcher_parse_errors_total`
  - `graph_log_watcher_rule_matches_total{rule}`
  - `graph_log_watcher_alerts_sent_total{rule,severity}`
  - `graph_log_watcher_alerts_suppressed_total{rule}`
  - `graph_log_watcher_notifier_errors_total`
  - `graph_log_watcher_docker_reconnect_total{container}`
  - `graph_log_watcher_checkpoint_timestamp{container}`
  - `graph_log_watcher_active_groups`
  - `graph_log_watcher_notifier_queue_size`
- 不允许 fingerprint、message、subgraph_id 等高基数字段成为 Prometheus label。
- 在 `metrics.listen` 指定的同一 HTTP server 提供 `/metrics` 和 `/healthz`。
- Docker API 不可用超过 1 分钟，或所有目标容器无法 attach 超过 1 分钟时，`/healthz` 返回 HTTP 503；否则返回 200。
- 健康响应使用 JSON，并包含总体状态和失败组件，不泄露配置或日志内容。
- watcher 自身使用 `slog`，统一 component、container、level、rule、fingerprint 字段。

### 验收条件

- 指标值能随核心场景准确变化，重复注册 collector 不会 panic。
- 单个目标容器不可用、但仍有其他目标正常 attach 时不判定为“所有目标不可用”。
- 所有错误日志和健康响应均不包含通知凭证或未清洗日志。

### 测试要求

- 使用 Prometheus test utilities 验证指标名、labels 和计数。
- HTTP 测试覆盖 healthy、Docker timeout、全部 detached、部分 detached 和恢复。
- 日志 capture 测试验证结构字段和敏感信息清洗。

---

## [ ] PR-12：Runtime 组装与 Graceful Shutdown

**优先级：** P1/P3\\
**前置依赖：** PR-07、PR-08、PR-09、PR-10、PR-11

### 目标

组装完整可运行服务，并保证启动失败、运行期错误和系统信号下的资源生命周期可控。

### 交付内容

- 完成 `graph-log-watcher run --config <path> [--dry-run]`。
- 按“一容器一 stream goroutine → bounded log channel → 单事件 Processor → bounded notification queue → notifier worker”组装。
- 启动顺序为：配置校验、state store、notifier（非 Dry Run）、metrics/health、Docker source、Processor。
- SIGINT/SIGTERM 后按以下顺序关闭：
  1. 标记服务 shutting down 并停止接受新 attach。
  2. 取消 Docker streams。
  3. 排空已接收日志和 Processor ack。
  4. 强制 flush checkpoint。
  5. 在剩余 timeout 内排空 notifier queue。
  6. 关闭 bbolt 和 HTTP server。
- 默认总 shutdown timeout 为 10 秒；超时后取消剩余工作并以非 0 状态退出。
- 组件启动失败时逆序关闭已启动组件，不遗留 goroutine 或锁文件。

### 验收条件

- 正常模式和 Dry Run 模式均能启动、处理日志并响应信号。
- DingTalk 失败不会停止 Docker 消费；checkpoint 持久化失败必须让健康状态变为 unhealthy，并由服务层明确记录。
- `run` 和 `check-config` 使用相同配置加载流程。

### 测试要求

- 使用 fake Source、Store、Processor、Notifier 和 HTTP server 做服务级集成测试。
- 覆盖每个启动阶段失败、运行期组件错误、SIGINT/SIGTERM、shutdown timeout 和关闭顺序。
- `go test -race ./...` 必须通过，并检查 goroutine 泄漏。

---

## [ ] PR-13：Docker 部署、示例配置与 README

**优先级：** P3\\
**前置依赖：** PR-12

### 目标

提供可复现的构建和 Docker Compose 部署方式，并让运维人员能够安全地完成 Dry Run 到正式告警的上线过程。

### 交付内容

- 增加多阶段 Dockerfile，生成最小运行镜像和 `graph-log-watcher` 二进制。
- 增加 Compose service，挂载 Docker socket、只读配置、持久化 state volume，暴露 9108 并配置 `/healthz` 健康检查。
- 提供完整 `config/example.yaml`，包含 critical、RPC error、generic error 和 repeated warning 示例，不包含真实密钥。
- README 覆盖：
  - 本地构建、测试和 CLI 用法。
  - Docker Compose 部署和 state 数据位置。
  - Docker socket 的高权限风险及程序只读 API 范围。
  - DingTalk 环境变量配置。
  - `/metrics`、`/healthz` 和主要指标。
  - 24～48 小时 Dry Run、先启用 critical 再逐步开放规则的上线流程。
  - Rule 演进原则、常见故障和 graceful shutdown。
  - 生产环境固定镜像 digest 的建议。

### 验收条件

- `docker build` 成功，容器内 `check-config` 可执行。
- Compose 配置渲染成功，配置文件只读、state 路径可写且密钥只从环境变量注入。
- README 命令与真实 CLI 参数一致。

### 测试要求

- CI 构建 Docker 镜像并运行 `check-config` smoke test。
- 校验 Compose 配置和 example config。
- 扫描镜像层、示例文件和 Git diff，确保不存在真实凭证。

---

## [ ] PR-14：MVP 验收套件与压力场景

**优先级：** P0～P3\\
**前置依赖：** PR-12、PR-13

### 目标

用端到端场景验证所有 MVP 能力、资源边界和恢复语义，形成发布前的最终质量门槛。

### 交付内容

- 建立由 fake Docker API、fake Notifier、临时 bbolt 和可控时钟组成的端到端测试夹具。
- 自动化覆盖设计方案九个完整场景：
  1. 100000 条 INFO：无 Rule Match、无 Alert，Context 和 Checkpoint 正常。
  2. 偶发 ERROR：低于 threshold 不告警。
  3. ERROR burst：同一 Incident 只告警一次。
  4. 单条 CRIT：立即告警并携带 INFO context。
  5. 持续错误：首次告警，其余在 cooldown 内抑制。
  6. Cooldown 后继续错误：再次告警并携带 suppressed count。
  7. Watcher crash：重启 replay，不永久漏日志。
  8. Container recreate：ID A → B 自动恢复。
  9. Log storm：内存、队列、groups 和 goroutine 数有界。
- 增加非阻塞通知失败、checkpoint 独立推进、redaction end-to-end 和 graceful shutdown 场景。
- 将性能较重的 100000 行及 log storm 检查标记为稳定的 integration/benchmark job，避免普通单元测试超时。

### 验收条件

- 所有设计方案 Definition of Done 条目均映射到自动化测试或明确的人工 Docker/DingTalk 验收步骤。
- 正常 CI、race CI 和 integration CI 全部通过。
- 不存在无限 channel、无限 fingerprint group、无限 sample 或按日志创建的无限 goroutine。
- 测试失败能定位到具体场景和预期行为，而不是仅返回通用超时。

### 测试要求

- 记录并断言 Alert 数、Rule Match 数、Suppressed 数、Checkpoint 位置和 notifier 调用序列。
- 在 race detector 下重复运行并发/重连/关闭用例。
- 提供基准结果的记录方式，但不设置依赖特定 CI 硬件的脆弱绝对性能阈值。

---

## 实施依赖与并行波次

```text
Wave 1: PR-01
            │
Wave 2: ┌───┴───────────┐
        PR-02           PR-03
          │               │
Wave 3:   ├──── PR-04 ────┤
          ├──── PR-05 ────┤
          └──── PR-08 ────┘
                 │
Wave 4:     PR-06 + PR-09
                 │
Wave 5:         PR-07
                 │
Wave 6:     PR-10 + PR-11
                 │
Wave 7:         PR-12
                 │
Wave 8:         PR-13
                 │
Wave 9:         PR-14
```

说明：波次表示推荐的合并节奏，不替代每个任务声明的精确前置依赖。PR-14 依赖 PR-13，因此二者必须串行。

## Definition of Done 对照表

| 设计要求 | 主要负责 PR | 最终验收 |
| --- | --- | --- |
| Docker Engine API、容器发现、日志 follow | PR-08 | PR-14 |
| graph-node Parser、Level 提取、structured fields | PR-03 | PR-14 |
| INFO 仅 Context、WARN/ERROR/CRIT 进入 Rule | PR-03、PR-07 | PR-14 |
| Rule Matching、priority、stop_on_match | PR-05 | PR-14 |
| Sliding Window、Threshold | PR-06 | PR-14 |
| Fingerprint、四种 Dedup mode | PR-05 | PR-14 |
| Cooldown、suppressed count、samples | PR-06 | PR-14 |
| Checkpoint、restart replay、At-Least-Once | PR-09 | PR-14 |
| Container recreate | PR-08、PR-09 | PR-14 |
| Context before/after、ring buffer | PR-04、PR-07 | PR-14 |
| Redaction | PR-04 | PR-14 |
| go-conflux-util/alert、DingTalk、retry、safety valve | PR-10 | PR-14 |
| Dry Run | PR-07、PR-12 | PR-14 |
| Prometheus Metrics、Health Check、slog | PR-11 | PR-14 |
| Bounded channel/state、Backpressure | PR-06、PR-08、PR-10 | PR-14 |
| Graceful Shutdown | PR-12 | PR-14 |
| Dockerfile、Compose、example config、README | PR-13 | PR-14 |

## MVP 范围外

以下能力不进入上述 PR：

- Loki、Elasticsearch 或其他长期日志存储。
- 全文日志搜索、跨机器/跨节点日志中心和 Web UI。
- Kafka、复杂 CEP 或无限历史回放。
- AI 日志分析和自动故障修复。
- Confura、Congraph、IPFS 的跨系统关联分析。
- 聚合窗口、cooldown、context 的跨进程持久化。
