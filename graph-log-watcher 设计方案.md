# graph-log-watcher 开发规格

## 1. 背景

当前 Congraph 使用 Docker Compose 部署 graph-node，主要包含：

- `index-node-0`
- `query-node-0`

graph-node 版本：

```text
graph-node 0.34.1 (2024-02-08)
```

Docker 日志使用：

```text
log-driver = json-file
max-size   = 1g
max-file   = 3
```

现有第一阶段监控链路：

```text
graph-node Metrics
    ↓
Prometheus
    ↓
Grafana Alerting
    ↓
DingTalk
```

主要解决：

- deployment failed
- RPC error rate
- DB connection error
- indexing/head lag
- 服务不可用

现在增加第二阶段：

```text
graph-node stdout/stderr
        ↓
Docker Engine API
        ↓
graph-log-watcher
        ↓
日志解析
        ↓
Rule / Window / Dedup / Cooldown
        ↓
DingTalk
```

用于捕获指标不容易表达的异常事件，例如：

- `CRIT`
- `ERRO`
- 高频 `WARN`
- panic
- 特定 RPC / DB / indexing 错误
- graph-node 特殊异常事件

本项目不是日志平台，不替代 Loki。

核心目标：

> 将大量 graph-node 运行日志转化为少量、高信噪比、能够驱动人工处理的 Incident Alert。

---

# 2. 核心设计原则

系统围绕六个核心能力：

```text
Rule
Window
Dedup
Cooldown
Checkpoint
Context
```

分别解决：

```text
Rule
→ 什么事件值得关注？

Window
→ 多长时间内发生多少次才值得报警？

Dedup
→ 大量相似错误是否属于同一个 Incident？

Cooldown
→ 同一个问题已经通知后，多久不要再次刷屏？

Checkpoint
→ watcher 重启后如何恢复消费、不漏日志？

Context
→ 报警时如何提供足够的前后文帮助排障？
```

另外增加一个 graph-node 特有的重要原则：

```text
Log Level
    ↓
决定日志是否进入 Alert Rule Engine
```

默认语义：

```text
CRIT  → 告警候选
ERRO  → 告警候选
WARN  → 根据 Rule 判断
INFO  → 不报警，仅进入 Context
DEBUG → 默认忽略，仅可选 Context
TRACE → 默认忽略
```

因此：

> Log Level 用于判断“是否值得进入告警分析”，Rule Regex 用于判断“属于哪类问题”。

不要使用 Regex 再次判断一条日志是不是 ERROR。

---

# 3. Metrics 与 Logs 的职责边界

必须明确：

```text
Metrics
→ State Detection
→ 系统当前是否异常

Logs
→ Event Detection
→ 刚刚发生了什么
```

例如：

```text
Prometheus:
deployment failed
head lag
RPC error rate
DB connection failure

graph-log-watcher:
CRIT
panic
specific error
repeated WARN
unknown runtime anomaly
```

日志告警不是服务健康状态的唯一依据。

不能：

```text
看到一条 ERROR
→ 直接认定系统故障
```

---

# 4. MVP Scope

## 4.1 必须实现

MVP 包含：

```text
Docker Engine API

Container 自动发现 / 监听

Docker log follow

graph-node 日志 Parser

Log Level 提取

结构化字段解析

Rule Matching

Sliding Window

Threshold

Fingerprint Dedup

Cooldown

Checkpoint

Restart Replay

Container Recreate Detection

Context Ring Buffer

Redaction

DingTalk Alert

go-conflux-util/alert 集成

Dry Run

Prometheus Metrics

Health Check

Graceful Shutdown
```

---

## 4.2 暂不实现

不实现：

```text
Loki

Elasticsearch

全文日志搜索

Web UI

日志长期存储

Kafka

跨机器日志中心

复杂 CEP

AI 日志分析

自动故障修复
```

后续出现：

```text
查询 7 天历史日志

跨节点查询

按 subgraph 检索

Confura / Congraph / IPFS 关联分析
```

再引入：

```text
Docker
  ↓
Grafana Alloy
  ↓
Loki
```

---

# 5. 总体架构

```text
                      Docker Daemon
                           │
                  /var/run/docker.sock
                           │
                           ▼
                    DockerLogSource
                           │
                           ▼
                        RawLog
                           │
                           ▼
                    GraphNodeParser
                           │
              ┌────────────┼─────────────┐
              │            │             │
            Level        Message       Fields
              │                          │
              │                component/subgraph/etc.
              │
              ▼
                         Redactor
                            │
                            ▼
                     ContextBuffer
                            │
                            ▼
                      Level Router
               ┌────────────┼──────────────┐
               │            │              │
            INFO/DEBUG     WARN         ERRO/CRIT
               │            │              │
          Context only      ▼              ▼
                         RuleEngine ←───────┘
                            │
                            ▼
                       Fingerprint
                            │
                            ▼
                    Sliding Window
                            │
                      threshold?
                       /       \
                     no         yes
                     │           │
                     │           ▼
                     │       Cooldown
                     │       /      \
                     │    active    expired
                     │      │          │
                     │      ▼          ▼
                     │ suppress       Alert
                     │                 │
                     └─────────────────┘
                                       │
                                       ▼
                           go-conflux-util/alert
                                       │
                                       ▼
                                   DingTalk
```

旁路：

```text
DockerLogSource
      ↓
CheckpointStore
```

Watcher 自身：

```text
graph-log-watcher
      ↓
/metrics
/healthz
```

---

# 6. graph-node 日志格式

当前典型日志：

```text
Sep 14 15:17:33.743 INFO Committed write batch, time_ms: 5, weight: 312, entities: 0, block_count: 1, block_number: 262560262, sgd: 15, subgraph_id: Qm..., component: SubgraphInstanceManager
```

可拆解为：

```text
AppTimestamp:
Sep 14 15:17:33.743

Level:
INFO

Message:
Committed write batch

Fields:
time_ms      = 5
weight       = 312
entities     = 0
block_count  = 1
block_number = 262560262
sgd          = 15
subgraph_id  = Qm...
component    = SubgraphInstanceManager
```

---

# 7. LogEntry

统一日志对象：

```go
type LogEntry struct {
    ContainerID   string
    ContainerName string

    // Docker API timestamp。
    // 用于 checkpoint/replay/event order。
    Timestamp time.Time

    Stream string

    Level LogLevel

    Message string

    Fields map[string]string

    Raw string
}
```

Level：

```go
type LogLevel int

const (
    LevelUnknown LogLevel = iota
    LevelTrace
    LevelDebug
    LevelInfo
    LevelWarn
    LevelError
    LevelCritical
)
```

Parser 应兼容：

```text
INFO
WARN
ERRO
ERROR
CRIT
CRITICAL
DEBG
DEBUG
TRCE
TRACE
```

内部统一映射：

```text
ERRO / ERROR
→ LevelError

CRIT / CRITICAL
→ LevelCritical
```

---

# 8. Timestamp 设计

必须区分：

```text
Docker Timestamp
App Timestamp
```

Docker Timestamp：

```text
用于 checkpoint
用于 replay
用于内部事件排序
```

graph-node 日志里的：

```text
Sep 14 15:17:33.743
```

仅用于展示和诊断。

不能把 App Timestamp 作为可靠 checkpoint。

---

# 9. GraphNodeParser

接口：

```go
type Parser interface {
    Parse(raw RawLog) LogEntry
}
```

Parser 应至少提取：

```text
level
message
component
subgraph_id
block_number
sgd
```

其余：

```text
time_ms
weight
entities
block_count
block_hash
```

可以保存在：

```go
Fields map[string]string
```

---

# 10. Parser Failure

Parser 失败不能丢日志。

如果：

```text
无法识别 graph-node 格式
```

返回：

```text
Level   = UNKNOWN
Message = raw
Raw     = raw
```

这样 Rule 仍可针对：

```text
UNKNOWN + pattern
```

做 fallback 检测。

---

# 11. Level Router

这是 Rule Engine 前的重要过滤层。

默认：

```text
TRACE
→ discard

DEBUG
→ optionally context

INFO
→ ContextBuffer
→ Checkpoint
→ 不进入普通 Rule Engine

WARN
→ ContextBuffer
→ Rule Engine

ERROR
→ ContextBuffer
→ Rule Engine

CRITICAL
→ ContextBuffer
→ Rule Engine
```

这样大量：

```text
INFO Committed write batch
INFO Applying entity operations
```

不会进入复杂 Rule Matching。

---

# 12. Rule

Rule 定义：

> 对进入 Rule Engine 的日志进行进一步分类和报警策略控制。

结构：

```go
type Rule struct {
    ID       string
    Enabled  bool
    Severity string

    Containers []string

    Match MatchConfig

    Threshold int
    Window    time.Duration
    Cooldown  time.Duration

    Dedup DedupConfig

    Context ContextConfig

    StopOnMatch bool
}
```

---

# 13. MatchConfig

```go
type MatchConfig struct {
    Levels []LogLevel

    Pattern string

    Excludes []string

    Components []string

    FieldEquals map[string]string
}
```

因此 Rule 不仅可以匹配 message：

```yaml
match:
  levels:
    - ERROR

  pattern: '(?i)postgres'
```

还可以：

```yaml
match:
  levels:
    - ERROR

  components:
    - SubgraphInstanceManager
```

未来还可以：

```yaml
match:
  field_equals:
    network: mainnet
```

---

# 14. Rule Matching 顺序

Rule 判断顺序：

```text
Container
   ↓
Level
   ↓
Component
   ↓
Field filters
   ↓
Pattern
   ↓
Exclude
```

任意条件不满足：

```text
NO MATCH
```

---

# 15. Level Rule 示例

## Critical

默认：

```yaml
- id: critical-log

  match:
    levels:
      - CRITICAL

  severity: critical

  threshold: 1
  window: 1s
  cooldown: 10m
```

即：

```text
CRIT
→ 默认立即报警
```

---

# 16. Error Rule 示例

```yaml
- id: generic-error

  match:
    levels:
      - ERROR

  severity: high

  threshold: 5
  window: 1m
  cooldown: 10m
```

含义：

```text
一分钟内同类 ERROR >= 5
→ alert
```

---

# 17. Specific Error Rule

例如：

```yaml
- id: ethereum-rpc-error

  match:
    levels:
      - ERROR

    pattern: '(?i)(ethereum|rpc|request timeout)'

  severity: high

  threshold: 5
  window: 1m
  cooldown: 10m

  stop_on_match: true
```

Specific Rule 应放在 Generic Rule 前。

---

# 18. WARN Rule

默认不允许：

```text
WARN → 立即 DingTalk
```

应该：

```yaml
- id: repeated-warning

  match:
    levels:
      - WARN

  severity: medium

  threshold: 20
  window: 5m
  cooldown: 30m
```

或：

```yaml
- id: rpc-warning

  match:
    levels:
      - WARN

    pattern: '(?i)(rpc|timeout|connection|retry)'

  threshold: 10
  window: 5m
```

---

# 19. Rule Priority

同一日志可能匹配：

```text
ethereum-rpc-error
generic-error
```

Rule 按配置顺序处理。

支持：

```yaml
stop_on_match: true
```

推荐顺序：

```text
critical
panic

specific DB error
specific RPC error
specific indexing error

generic error

specific warn
generic repeated warn
```

---

# 20. Window

采用：

```text
Sliding Window
```

例如：

```yaml
threshold: 5
window: 1m
```

含义：

> 同一个 fingerprint 在任意连续 60 秒内出现至少 5 次。

实现：

```text
fingerprint
    ↓
timestamp deque

receive T
    ↓
remove timestamps < T-window
    ↓
append T
    ↓
len >= threshold ?
```

---

# 21. Dedup

Dedup 用于判断：

> 多条日志是否属于同一个 Incident。

默认：

```text
fingerprint =
hash(
    rule_id
    +
    container_name
    +
    normalized identity
)
```

---

# 22. Dedup 优先使用结构化字段

相比纯字符串 normalization，更推荐利用 Parser 提取的字段。

例如：

```text
block_number 每次不同

subgraph_id 相同
component 相同
错误类型相同
```

Rule 可以配置：

```yaml
dedup:
  mode: fields

  fields:
    - rule
    - container
    - component
    - subgraph_id
```

这样：

```text
block 262560262 error
block 262560263 error
block 262560264 error
```

如果：

```text
rule
container
component
subgraph_id
```

一致，则归为同一 Incident。

---

# 23. Dedup Mode

支持：

```text
exact
normalized
fields
rule
```

## exact

```text
rule
container
raw message
```

---

## normalized

```text
rule
container
normalized message
```

例如：

```yaml
normalize:
  - pattern: 'block [0-9]+'
    replace: 'block <N>'
```

---

## fields

推荐 graph-node 使用：

```text
rule
container
selected structured fields
```

---

## rule

```text
rule
container
```

适合：

```text
panic
critical
```

---

# 24. EventState

```go
type EventState struct {
    Fingerprint string

    FirstSeen time.Time
    LastSeen  time.Time

    WindowEvents []time.Time

    TotalCount int

    LastAlertAt   time.Time
    CooldownUntil time.Time

    SuppressedCount int

    Samples []string
}
```

---

# 25. Dynamic State 必须有限制

配置：

```yaml
aggregation:
  max_groups: 10000
  group_ttl: 24h
  max_samples: 3
```

避免：

```text
high cardinality
→ memory leak
```

---

# 26. Cooldown

Cooldown：

> 同一个 Incident 已经报警后，在一段时间内不重复通知。

例如：

```text
13:00
RPC Error × 50
→ ALERT

13:01
RPC Error × 30
→ SUPPRESS

13:05
RPC Error × 100
→ SUPPRESS
```

如果：

```text
cooldown = 10m
```

13:10 以后再次满足 threshold：

```text
→ 新 ALERT
```

---

# 27. Suppressed Count

Cooldown 期间仍必须统计：

```text
suppressed_count
```

重新报警时：

```text
Suppressed since previous alert:
824
```

这样值班人员可以知道问题是否持续恶化。

---

# 28. Checkpoint

目标：

> watcher crash/restart 后不能漏掉期间的 Docker 日志。

例如：

```text
10:00 watcher stop

10:00:05 ERROR
10:00:10 ERROR

10:01 watcher restart
```

必须能够 replay。

---

# 29. Delivery Semantics

使用：

```text
At-Least-Once + Dedup
```

不追求：

```text
Exactly Once
```

核心原则：

```text
宁愿 replay 少量日志
也不能因为时间边界永久丢日志
```

---

# 30. ContainerCheckpoint

```go
type ContainerCheckpoint struct {
    ContainerName string

    ContainerID string

    LastTimestamp time.Time

    LastEventHash string
}
```

逻辑主键：

```text
ContainerName
```

不是：

```text
ContainerID
```

---

# 31. Checkpoint Store

MVP 推荐：

```text
bbolt
```

默认：

```text
/var/lib/graph-log-watcher/state.db
```

接口：

```go
type StateStore interface {
    LoadCheckpoint(container string) (*Checkpoint, error)

    SaveCheckpoint(
        container string,
        checkpoint Checkpoint,
    ) error
}
```

---

# 32. Replay Overlap

Reconnect：

```text
since =
checkpoint.timestamp - replay_overlap
```

默认：

```yaml
checkpoint:
  replay_overlap: 2s
```

重复日志由：

```text
event hash
+
Dedup
```

处理。

---

# 33. Checkpoint Flush

不要每行 fsync。

默认：

```yaml
checkpoint:
  flush_interval: 1s
  flush_events: 100
```

满足任一条件：

```text
1 秒
或
100 条
```

执行持久化。

Shutdown 必须强制 flush。

---

# 34. Container Recreate

业务 identity：

```text
index-node-0
query-node-0
```

如果：

```text
container id A
→ recreate
→ container id B
```

Watcher 必须：

```text
检测旧 stream 结束
检测新 container
自动重新 attach
```

建议结合 Docker Events：

```text
create
start
die
destroy
```

---

# 35. Context

INFO 最重要的用途之一：

```text
Context
```

例如：

```text
INFO Applying entity operations
INFO Committed write batch
INFO Processing block
ERROR Failed to commit block
```

报警应携带前面的 INFO。

因此：

> INFO 不进入 Alert Engine，但必须进入 Context Buffer。

---

# 36. ContextBuffer

每个 container 独立 ring buffer：

```text
index-node-0
→ buffer

query-node-0
→ buffer
```

默认：

```yaml
context:
  buffer_lines: 100
```

---

# 37. Rule Context

```yaml
context:
  before: 10
  after: 3
  after_wait: 2s
  max_bytes: 16384
```

例如：

```text
10 lines before
trigger
3 lines after
```

---

# 38. Critical Context

Critical：

```yaml
context:
  before: 20
  after: 10
  after_wait: 2s
```

普通 ERROR：

```yaml
context:
  before: 5
  after: 0
```

---

# 39. Context Delay

`after > 0` 会延迟报警。

因此 Critical：

```text
after_wait <= 2s
```

不能因为 Context 等待很久。

---

# 40. Samples

重复日志不要全部发。

保存：

```text
first sample
latest sample
最多 N 个不同 sample
```

默认：

```text
max_samples = 3
```

---

# 41. Redaction

所有日志在进入：

```text
Context
Dedup
State
Notifier
```

之前必须经过 Redactor。

防止：

```text
API Key
RPC credential
Authorization header
Token
Secret
```

进入 DingTalk。

例如：

```yaml
redaction:
  rules:

    - pattern: '(?i)authorization:\s*bearer\s+\S+'
      replace: 'Authorization: Bearer ***'

    - pattern: '(?i)(api[_-]?key=)[^&\s]+'
      replace: '${1}***'
```

---

# 42. 通知系统

不自行重新实现 DingTalk Robot。

直接复用：

```text
github.com/Conflux-Chain/go-conflux-util/alert
```

graph-log-watcher 负责：

```text
Rule
Window
Dedup
Cooldown
Checkpoint
Context
```

`go-conflux-util/alert` 负责：

```text
Channel abstraction

DingTalk

Webhook

Secret signing

Markdown formatting

@ user

未来 Telegram/SMTP/PagerDuty/FlashDuty
```

---

# 43. Notifier Adapter

Watcher 内仍保留自己的抽象：

```go
type Notifier interface {
    Notify(
        context.Context,
        Alert,
    ) error
}
```

实现：

```go
type ConfluxAlertNotifier struct {
    channel alert.Channel
}
```

逻辑：

```go
func (n *ConfluxAlertNotifier) Notify(
    ctx context.Context,
    event Alert,
) error {
    return n.channel.Send(
        ctx,
        &alert.Notification{
            Title:    buildTitle(event),
            Severity: convertSeverity(event.Severity),
            Content:  buildContent(event),
        },
    )
}
```

这样业务层不直接耦合 DingTalk。

---

# 44. Severity Mapping

内部：

```text
low
medium
high
critical
```

直接映射：

```text
go-conflux-util/alert SeverityLow
SeverityMedium
SeverityHigh
SeverityCritical
```

建议：

```text
WARN
→ medium

ERROR
→ high

CRITICAL
→ critical
```

Rule 可覆盖默认 severity。

---

# 45. Alert Model

```go
type Alert struct {
    RuleID   string
    Severity string

    ContainerName string
    ContainerID   string

    Level LogLevel

    Component  string
    SubgraphID string

    Fingerprint string

    FirstSeen time.Time
    LastSeen  time.Time

    Window      time.Duration
    WindowCount int

    TotalCount int

    SuppressedCount int

    Samples []string

    Context []string
}
```

---

# 46. DingTalk 示例

Critical：

```text
[CRITICAL] graph-node critical log

Node:
index-node-0

Component:
SubgraphInstanceManager

Subgraph:
Qm...

Rule:
critical-log

Count:
1

Time:
2026-09-14 15:17:33

Error:
...

Context:
INFO ...
INFO ...
CRIT ...

Fingerprint:
abc123
```

重复 Error：

```text
[HIGH] graph-node RPC errors

Node:
index-node-0

Rule:
ethereum-rpc-error

Current:
38 errors / 1m

Suppressed since previous alert:
824

First Seen:
15:00:01

Last Seen:
15:10:04

Samples:
1. ...
2. ...
3. ...
```

---

# 47. Notification Failure

发送异步化：

```text
Alert
  ↓
bounded queue
  ↓
Notifier Worker
```

例如：

```text
queue = 100
```

Retry：

```text
1s
2s
5s
10s
```

达到最大次数：

```text
log error
increment metric
drop notification
```

不阻塞 Docker log consumption。

---

# 48. Global Safety Valve

防止错误配置造成 alert storm：

```yaml
notifier:
  max_alerts_per_minute: 20
```

超过：

```text
停止普通告警

发送一次：
Alert storm detected
```

进入短暂 global cooldown。

---

# 49. 推荐配置

```yaml
docker:
  socket: unix:///var/run/docker.sock

containers:
  - index-node-0
  - query-node-0

parser:
  type: graph-node

levels:
  context:
    - INFO
    - WARN
    - ERROR
    - CRITICAL

  alert_candidates:
    - WARN
    - ERROR
    - CRITICAL

checkpoint:
  path: /var/lib/graph-log-watcher/state.db
  replay_overlap: 2s
  flush_interval: 1s
  flush_events: 100

aggregation:
  max_groups: 10000
  group_ttl: 24h
  max_samples: 3

context:
  buffer_lines: 100

redaction:
  rules:

    - pattern: '(?i)authorization:\s*bearer\s+\S+'
      replace: 'Authorization: Bearer ***'

    - pattern: '(?i)(api[_-]?key=)[^&\s]+'
      replace: '${1}***'

rules:

  - id: critical-log
    enabled: true

    severity: critical

    match:
      levels:
        - CRITICAL

    threshold: 1
    window: 1s
    cooldown: 10m

    dedup:
      mode: rule

    context:
      before: 20
      after: 10
      after_wait: 2s
      max_bytes: 16384

    stop_on_match: true

  - id: rpc-error
    enabled: true

    severity: high

    match:
      levels:
        - ERROR

      pattern: '(?i)(ethereum|rpc|request timeout)'

    threshold: 5
    window: 1m
    cooldown: 10m

    dedup:
      mode: fields

      fields:
        - rule
        - container
        - component
        - subgraph_id

    context:
      before: 5
      after: 0

    stop_on_match: true

  - id: generic-error
    enabled: true

    severity: high

    match:
      levels:
        - ERROR

    threshold: 5
    window: 1m
    cooldown: 10m

    dedup:
      mode: normalized

    context:
      before: 5
      after: 0

  - id: repeated-warning
    enabled: false

    severity: medium

    match:
      levels:
        - WARN

    threshold: 20
    window: 5m
    cooldown: 30m

    dedup:
      mode: normalized

    context:
      before: 5
      after: 0

alert:
  custom_tags:
    - prod
    - congraph

  channels:

    dingrobot:
      platform: dingtalk
      webhook: ${DINGTALK_WEBHOOK}
      secret: ${DINGTALK_SECRET}
      atMobiles: []
      isAtAll: false

notifier:
  max_alerts_per_minute: 20

metrics:
  listen: ":9108"
```

---

# 50. 配置设计原则

Rule 中：

```text
Level
```

用于：

```text
严重度第一层过滤
```

Pattern：

```text
错误分类
```

Component / Fields：

```text
进一步缩小语义
```

因此不要写：

```yaml
pattern: '(?i)\berror\b'
```

来判断错误等级。

正确方式：

```yaml
match:
  levels:
    - ERROR
```

---

# 51. CLI

```bash
graph-log-watcher run \
  --config /etc/graph-log-watcher/config.yaml
```

配置检查：

```bash
graph-log-watcher check-config \
  --config config.yaml
```

Dry Run：

```bash
graph-log-watcher run \
  --config config.yaml \
  --dry-run
```

Dry Run：

```text
完整执行：
Parser
Rule
Window
Dedup
Cooldown

但是：
不调用 notification channel
```

stdout 输出：

```text
WOULD ALERT
rule=rpc-error
container=index-node-0
fingerprint=...
```

---

# 52. Runtime Pipeline

每收到日志：

```text
Docker Raw Log
      ↓
GraphNodeParser
      ↓
Redact
      ↓
ContextBuffer.Append
      ↓
LevelRouter
      │
      ├─ INFO
      │    ↓
      │ checkpoint
      │
      ├─ DEBUG
      │    ↓
      │ optional context
      │
      └─ WARN / ERROR / CRITICAL
             ↓
          Rule Match
             ↓
          Fingerprint
             ↓
           Window
             ↓
          Threshold
             ↓
          Cooldown
             ↓
            Alert
             ↓
        Notification Queue
             ↓
     go-conflux-util/alert
             ↓
          DingTalk
```

无论是否匹配 Rule：

```text
checkpoint
```

都必须继续推进。

---

# 53. Checkpoint 与 Notification 的关系

Checkpoint 不依赖 DingTalk 成功。

即：

```text
log consumed
    ↓
checkpoint advance
    ↓
DingTalk failed
```

不能因此不断 replay 同一批日志。

Notification 自己做 retry。

---

# 54. Prometheus Metrics

至少：

```text
graph_log_watcher_logs_received_total{
  container="",
  level=""
}

graph_log_watcher_parse_errors_total

graph_log_watcher_rule_matches_total{
  rule=""
}

graph_log_watcher_alerts_sent_total{
  rule="",
  severity=""
}

graph_log_watcher_alerts_suppressed_total{
  rule=""
}

graph_log_watcher_notifier_errors_total

graph_log_watcher_docker_reconnect_total{
  container=""
}

graph_log_watcher_checkpoint_timestamp{
  container=""
}

graph_log_watcher_active_groups

graph_log_watcher_notifier_queue_size
```

Level metric 很重要，可以看到：

```text
INFO/sec
WARN/sec
ERROR/sec
CRIT/sec
```

---

# 55. Health Check

```text
GET /healthz
```

正常：

```json
{
  "status": "ok"
}
```

以下情况应该 unhealthy：

```text
Docker API 长时间不可用

所有目标 container 长时间无法 attach
```

---

# 56. Logging

Watcher 自己使用：

```text
slog
```

结构字段：

```text
component
container
level
rule
fingerprint
```

例如：

```text
level=INFO
component=aggregator
container=index-node-0
rule=rpc-error
fingerprint=abc123
msg="alert suppressed by cooldown"
```

---

# 57. Package Structure

```text
graph-log-watcher/

cmd/
  graph-log-watcher/
    main.go

internal/

  config/
    config.go
    validate.go

  source/
    docker.go
    stream.go
    events.go

  parser/
    parser.go
    graphnode.go

  logentry/
    entry.go
    level.go

  redact/
    redact.go

  context/
    buffer.go

  rule/
    rule.go
    matcher.go
    normalize.go

  aggregate/
    window.go
    fingerprint.go
    dedup.go
    cooldown.go

  checkpoint/
    store.go
    bbolt.go

  notify/
    notifier.go
    conflux_alert.go

  metrics/
    metrics.go

  service/
    watcher.go

config/
  example.yaml

deploy/
  docker-compose.yaml

SPEC.md
README.md
```

---

# 58. Concurrency Model

建议：

```text
1 goroutine / container stream
           ↓
central bounded channel
           ↓
single event processor
```

MVP 优先保证：

```text
container 内日志顺序
```

不要为了性能过早并发 Rule Engine。

Regex 不会是瓶颈。

---

# 59. Backpressure

所有 channel 必须 bounded。

例如：

```text
log channel:
10000

notification queue:
100
```

禁止：

```text
无限 goroutine
无限 channel
无限 fingerprint
```

---

# 60. Security

挂载：

```text
/var/run/docker.sock
```

意味着高权限。

程序只允许调用：

```text
container list
container inspect
container logs
docker events
```

禁止：

```text
exec
stop
kill
remove
create
```

未来可考虑：

```text
docker-socket-proxy
```

---

# 61. Docker Deployment

```yaml
services:

  graph-log-watcher:

    image: graph-log-watcher:v0.1.0

    restart: unless-stopped

    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./config.yaml:/etc/graph-log-watcher/config.yaml:ro
      - graph-log-watcher-state:/var/lib/graph-log-watcher

    environment:
      DINGTALK_WEBHOOK: ${DINGTALK_WEBHOOK}
      DINGTALK_SECRET: ${DINGTALK_SECRET}

    ports:
      - "9108:9108"

volumes:

  graph-log-watcher-state:
```

正式环境最后应 pin digest。

---

# 62. Graceful Shutdown

SIGTERM/SIGINT：

```text
cancel docker streams

stop accepting new logs

flush checkpoint

flush state

finish notifier queue within timeout

close bbolt

exit
```

---

# 63. Config Validation

启动必须 fail fast：

```text
invalid regexp

unknown log level

window <= 0

threshold <= 0

cooldown < 0

duplicate rule ID

unknown dedup mode

fields mode without fields

invalid duration
```

---

# 64. Unit Tests

## Parser

验证：

```text
INFO parse

WARN parse

ERRO parse

CRIT parse

fields parse

invalid line fallback UNKNOWN
```

---

## Level Router

验证：

```text
INFO 不进入 Rule Engine

WARN 进入 Rule Engine

ERROR 进入 Rule Engine

CRITICAL 进入 Rule Engine
```

---

## Rule

验证：

```text
level match

pattern

exclude

component

container

stop_on_match
```

---

## Window

验证：

```text
threshold

sliding expiration

window boundary
```

---

## Dedup

验证：

```text
same structured identity
→ same fingerprint

different block_number
same rule/component/subgraph
→ same fingerprint

different subgraph
→ different fingerprint
```

---

## Cooldown

验证：

```text
first alert

cooldown suppress

suppressed count

cooldown expiry

new alert
```

---

## Checkpoint

验证：

```text
save/load

restart replay

overlap

duplicate replay

container recreate
```

---

## Context

验证：

```text
INFO before ERROR

before lines

after lines

buffer eviction

max bytes

container isolation
```

---

# 65. 完整场景测试

## Case 1：大量 INFO

```text
100000 INFO
```

结果：

```text
0 Rule Match

0 Alert

Context 正常更新

Checkpoint 正常推进
```

---

## Case 2：偶发 ERROR

配置：

```text
threshold = 5
window = 1m
```

实际：

```text
2 ERROR/min
```

结果：

```text
NO ALERT
```

---

## Case 3：ERROR Burst

```text
10 same errors / 30s
```

结果：

```text
ONE ALERT
```

不是：

```text
10 ALERTS
```

---

## Case 4：CRIT

```text
1 CRIT
```

结果：

```text
IMMEDIATE ALERT
```

并包含前面的 INFO Context。

---

## Case 5：持续错误

```text
cooldown = 10m

1000 events
```

结果：

```text
first alert

others suppressed
```

---

## Case 6：Cooldown 后继续发生

重新满足 threshold：

```text
new alert
```

包含：

```text
suppressed_count
```

---

## Case 7：Watcher Crash

```text
watcher stop

graph-node logs continue

watcher restart
```

必须 replay。

允许重复处理。

不能永久漏日志。

---

## Case 8：Container Recreate

```text
container ID A
→ B
```

无需人工重启 watcher。

---

## Case 9：Log Storm

```text
100000 logs/min
```

必须：

```text
bounded memory

bounded notification

no goroutine leak

no unbounded fingerprint growth
```

---

# 66. Dry Run 上线流程

正式 DingTalk 前建议：

```text
24~48h dry-run
```

重点观察：

```text
WARN 数量

ERROR 数量

CRIT 数量

Top matched rules

Top fingerprints

fingerprint cardinality

suppressed count
```

初始只启用：

```text
critical-log
```

然后逐渐启用：

```text
known RPC error
known DB error
known indexing error
```

最后才考虑：

```text
generic-error
generic WARN
```

---

# 67. Rule 演进原则

Rule 不应该：

```text
预先猜测所有 ERROR
```

而应该：

```text
真实 Incident
    ↓
分析日志
    ↓
加入 Rule
    ↓
Dry Run
    ↓
调整 Window / Threshold
    ↓
生产启用
```

Rule 是运维知识的沉淀。

---

# 68. Definition of Done

MVP 完成必须满足：

```text
实时监听 index-node-0 / query-node-0

通过 Docker Engine API 获取日志

正确解析 graph-node log level

正确解析常见 structured fields

INFO 不进入普通 Alert Engine

INFO 可作为 Context

WARN/ERROR/CRIT 支持 Rule

支持 Level + Pattern + Component + Field Match

支持 Sliding Window

支持 Threshold

支持 Fingerprint

支持 fields-based Dedup

支持 normalization fallback

支持 Cooldown

支持 suppressed count

支持 Checkpoint

支持 restart replay

支持 container recreate

实现 At-Least-Once + Dedup

支持 Context

支持 Redaction

复用 go-conflux-util/alert

支持 DingTalk

支持 Dry Run

支持 Prometheus Metrics

支持 /healthz

支持 graceful shutdown

核心模块 Unit Tests

提供 Dockerfile

提供 example config

提供 README
```

---

# 69. 开发优先级

## P0 — 核心 Detection Pipeline

```text
Docker LogSource

GraphNodeParser

Log Level

ContextBuffer

Rule Matcher

Window

Fingerprint

Dedup

Cooldown
```

先确保：

```text
INFO 不报警

CRIT 能报警

重复 ERROR 不刷屏
```

---

## P1 — Reliability

```text
Checkpoint

Replay

Container Recreate

State Persistence

Graceful Shutdown
```

---

## P2 — Notification

```text
go-conflux-util/alert adapter

DingTalk

Notification Retry

Safety Valve

Redaction
```

---

## P3 — Operability

```text
Prometheus Metrics

Health Check

Dry Run

check-config

Production Config
```

---

# 70. 最终状态机

```text
                  Docker Log
                      │
                      ▼
                GraphNodeParser
                      │
                      ▼
                   Redact
                      │
                      ▼
                Context Buffer
                      │
                      ▼
                 Level Router
                      │
          ┌───────────┼──────────────┐
          │           │              │
       INFO/DEBUG    WARN        ERROR/CRIT
          │           │              │
          │           └──────┬───────┘
          │                  │
          │                  ▼
          │              Rule Match
          │                  │
          │             matched?
          │               /    \
          │             no      yes
          │             │        │
          │             │        ▼
          │             │   Fingerprint
          │             │        │
          │             │        ▼
          │             │     Window
          │             │        │
          │             │   threshold?
          │             │      /    \
          │             │    no      yes
          │             │    │        │
          │             │    │        ▼
          │             │    │    Cooldown
          │             │    │     /     \
          │             │    │  active   expired
          │             │    │    │        │
          │             │    │    ▼        ▼
          │             │    │ suppress   ALERT
          │             │    │             │
          │             │    │             ▼
          │             │    │  go-conflux-util/alert
          │             │    │             │
          │             │    │             ▼
          │             │    │          DingTalk
          │             │    │
          └─────────────┴────┴──────────────┐
                                            │
                                            ▼
                                       Checkpoint
```

---

# 71. 产品边界

`graph-log-watcher` 的目标不是：

```text
收集所有日志

搜索所有日志

长期保存日志

尽可能发现所有 ERROR
```

目标是：

```text
大量运行日志
      ↓
结构化识别
      ↓
过滤低价值 INFO
      ↓
识别真正异常事件
      ↓
聚合同类错误
      ↓
抑制重复报警
      ↓
附带有效上下文
      ↓
形成少量、高价值 Incident Alert
```

核心评价指标不是：

```text
告警数量多
```

而应该是：

```text
High Signal / Low Noise
```

即：

> 值班人员收到一条 graph-log-watcher 消息时，大概率真的值得看。