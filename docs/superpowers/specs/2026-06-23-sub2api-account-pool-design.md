# sub2api 账号池迁移设计

日期：2026-06-23

## 背景

Wynth API 当前的主线是多上游渠道聚合。已有“上游源”能力负责连接 new-api / sub2api 类中转站，发现上游分组、创建上游 key，并在本地生成普通渠道。这条链路的核心对象是“渠道”：请求进入后，系统先按分组、模型、优先级、权重、可用性和自动优先级规则选择一个渠道，再由该渠道转发到上游。

sub2api 的账号池能力不同于普通 key 聚合。它管理的是一批真实账号或 OAuth 凭据，并且围绕账号维护调度、并发、代理、token 刷新、模型映射、临时禁用、错误恢复和可用性统计。直接把每个账号变成本地渠道，或塞进现有 `Channel.Key` / 多 key 逻辑，会让渠道层承担账号生命周期，后续很难维护。

## 目标

- 迁移 sub2api 的账号反代和号池核心能力，但适配 Wynth API 的渠道调度架构。
- 让“自己的账号池”在产品层表现为一种上游能力，同时在代码层保持独立账号池域。
- 保持现有渠道选择逻辑不被账号池细节污染。
- 第一阶段优先支持 OpenAI / ChatGPT 账号池；Claude / Anthropic 账号池作为后续阶段。
- 为后续接入账号级监控、缓存率、首 token 延迟、自动优先级和模型映射检测留接口。

## 非目标

- 不直接复制 sub2api 的 Ent ORM 表结构。
- 不在第一阶段迁移 sub2api 的所有高级调度策略。
- 不把每个账号同步成一个普通 Wynth 渠道。
- 不改变现有 new-api / sub2api 上游源 key 同步流程。
- 不重写当前渠道调度器、计费系统或 relay 主流程。

## 核心决策

采用“独立账号池域 + 账号池渠道适配器”。

Wynth 仍然先按现有规则选择本地渠道。若选中的渠道绑定了账号池，则 relay 层在真正发起上游请求前进入账号池调度器，选择一个具体账号，并将该账号的 token、代理、上游地址和模型映射应用到本次请求。请求结束后，账号池记录成功、失败、延迟、首 token 延迟、token 用量和临时限流状态。

这样可以把两层调度分开：

- 渠道调度：决定这次请求走哪个上游能力入口。
- 账号调度：决定这个账号池渠道内部使用哪个账号。

计费模型名必须和运行时上游模型名分开。客户端请求的模型用于外层渠道选择、价格计算、预扣费和日志主键；账号池模型映射只允许改写实际上游请求里的模型名，不允许回头改变本次请求的计费模型。

## 产品模型

后台可以把账号池入口放在“上游源”附近，因为对管理员来说它也是一种上游能力。但实现上不应复用现有 `UpstreamSourceAdapter` 的 key 创建接口。账号池没有“向上游创建 key”的步骤，它创建的是本地账号池渠道，然后由本地账号池在运行时选择账号。

建议产品上分为三层：

1. 账号池源
   - 例如“自建 ChatGPT 账号池”。
   - 管理平台类型、默认代理、默认监控、默认调度策略。
2. 账号
   - 存储账号凭据、OAuth token、状态、代理、并发限制、临时禁用原因和模型能力。
3. 账号池渠道
   - 本地可被 Wynth 调度的渠道。
   - 绑定账号池源和账号筛选规则。
   - 配置本地分组、模型范围、优先级、权重、自动重试、禁用生图等渠道级能力。

## 数据模型

第一阶段建议新增独立模型，而不是扩展现有 `Channel` 表承载所有账号字段。

### `AccountPool`

账号池源。

关键字段：

- `Name`
- `Platform`：第一阶段 `openai`，后续扩展 `anthropic`
- `Status`
- `DefaultProxyID`
- `DefaultMonitorEnabled`
- `DefaultSchedulePolicy`
- `Remark`

### `AccountPoolAccount`

账号记录。

关键字段：

- `PoolID`
- `Name`
- `Email` 或外部账号标识
- `CredentialConfig`：JSON 文本，使用项目 `common` JSON 包处理
- `TokenState`：JSON 文本，保存 access token、refresh token、过期时间、版本号
- `Status`：启用、禁用、过期、限流、临时不可调度
- `Priority`
- `Weight` 或 `LoadFactor`
- `MaxConcurrency`
- `ProxyID`
- `SupportedModels`
- `ModelMapping`
- `LastUsedAt`
- `RateLimitedUntil`
- `TempDisabledUntil`
- `TempDisabledReason`

`Status` 只保存持久状态，例如启用、禁用、过期。限流、过载、临时不可调度等瞬态状态由 `RateLimitedUntil`、`TempDisabledUntil`、并发槽和运行时统计在调度时派生，避免状态字段和时间字段漂移。

### `AccountPoolProxy`

账号级代理。

关键字段：

- `Name`
- `Protocol`
- `Host`
- `Port`
- `Username`
- `Password`
- `Status`
- `FallbackProxyID`

### `AccountPoolChannelBinding`

本地渠道到账号池的绑定。

关键字段：

- `ChannelID`
- `PoolID`
- `AccountFilterConfig`
- `ModelPolicy`
- `SchedulePolicy`
- `AccountRetryTimes`
- `Status`

本地 `Channel` 仍然是 Wynth 外层调度入口。账号池绑定只说明该渠道的运行时凭据来自账号池。

约束：

- `ChannelID` 必须唯一，一个本地渠道只能绑定一个账号池。
- 删除账号池时，绑定渠道必须进入草稿或禁用状态，不能继续被外层调度选中。
- 删除代理时，引用该代理的账号回退到账号池默认代理或无代理。
- `FallbackProxyID` 不允许形成循环。

### 运行时状态

高频调度状态不直接写数据库。

- 单机部署先使用进程内状态保存并发槽、短期失败集合和热路径计数。
- 多节点部署需要 Redis 版本的状态存储。
- 数据库只保存持久配置和周期性聚合指标。
- `LastUsedAt`、延迟、成功率、缓存率等指标可以批量 flush，不能每个请求都做热写。

## 调度流程

### 外层渠道选择

外层不变：

1. middleware 根据用户分组、模型、渠道状态、优先级和权重选择本地渠道。
2. controller / relay 初始化渠道上下文。
3. 如果渠道没有账号池绑定，继续走现有 relay 流程。
4. 如果渠道有账号池绑定，进入账号池调度。

Phase 1 只允许创建草稿绑定或强制禁用的账号池渠道。在 Phase 2 relay 运行时接好前，账号池渠道不得被外层调度选中，避免把空 key / 空 base URL 渠道暴露给真实流量。

### 内层账号选择

账号池调度器输入：

- `ChannelID`
- `PoolID`
- 请求模型
- 上游模型名
- 用户分组
- 请求端点
- 是否流式
- 可选 session hash / previous response id

调度器还必须读取请求级账号池状态：

- 本次请求内已经失败的账号集合。
- 本次请求已经选择过的账号集合。
- 是否已经开始向客户端输出流式内容。

第一阶段账号选择规则：

1. 过滤禁用、过期、限流、临时不可调度账号。
2. 过滤不支持当前模型或能力的账号。
3. 过滤并发已满账号。
4. 在最高账号优先级层内按权重选择。
5. 同一次请求内已经失败的账号不再选择。

失败账号集合必须存放在 Gin 请求上下文中，并贯穿外层渠道重试。即使外层 retry 再次选中同一个账号池渠道，调度器也不能重新选中刚刚在本次请求里失败的账号。

后续阶段再加入：

- session 粘性
- previous response id 粘性
- 首 token 延迟评分
- 缓存率评分
- 动态错误率
- 更复杂的负载均衡

## Relay 接入点

账号池不应影响普通渠道。推荐在 relay 发起上游请求前增加一个账号池解析步骤：

1. handler 调用 `info.InitChannelMeta(c)` 后，已经知道本地渠道、渠道设置和初始上游模型。
2. 在请求转换、`adaptor.Init(info)`、`adaptor.DoRequest`、上游 header 构造之前检查当前渠道是否绑定账号池。
3. 若绑定，调用 `AccountPoolScheduler.SelectAccount`。
4. 将选择结果写入本次 relay 的 channel meta：
   - 实际 access token / API key
   - 实际 upstream base URL
   - 实际 proxy 配置
   - 实际上游模型名
   - account id / pool id 追踪字段
5. 后续模型映射、请求转换和 header 构造只能读取已经注入后的 channel meta。
6. 上游请求结束后释放并发槽并记录结果。

对于文本、responses、embedding、image、audio 等 handler，账号池 hook 必须放在各 handler 内 `info.InitChannelMeta(c)` 之后、任何 `ModelMappedHelper` 或 request conversion 之前。这样 token、base URL、proxy 和账号级上游模型名都能参与实际请求。普通渠道在这个 hook 中直接 no-op。

预扣费仍然在 controller 里按客户端请求模型执行一次。账号级重试不得重复预扣。账号映射只改写 `ChannelMeta.UpstreamModelName` 和实际请求体模型，不得改变本次请求的计费模型、`PriceData` 或预扣费记录。若现有流程中某些 helper 会改写 `OriginModelName`，账号池实现必须引入不可变的 billing model 字段或在进入账号池 hook 前冻结计费上下文，避免后结算使用错误模型。

如果请求尚未开始流式输出，账号失败可以在同一账号池渠道内按 `AccountRetryTimes` 再选另一个账号。若账号池耗尽，再把错误交给外层渠道重试逻辑。若流式输出已经开始，则不做账号级重试，只记录失败。

流式请求需要一个明确的 `streamStarted` 标记。该标记应在第一次向客户端 flush header 或第一段 chunk 时设置。账号级重试和外层渠道重试都必须检查该标记，已经开始输出后不能重放请求。

账号并发槽必须用 `defer` 绑定到单次账号尝试，并覆盖成功、上游错误、panic 和客户端断开。释放必须发生在控制权回到外层 retry 选择下一个渠道之前。

### 模型映射顺序

模型解析顺序固定为：

1. 客户端请求模型：用于渠道选择、价格计算和日志主模型。
2. 渠道 `ModelMapping`：把请求模型映射为渠道默认上游模型。
3. 账号池渠道绑定 `ModelPolicy`：过滤或限制该渠道允许的模型范围。
4. 账号 `SupportedModels`：过滤账号是否支持当前上游模型。
5. 账号 `ModelMapping`：把渠道上游模型映射为该账号实际调用的上游模型。

第 5 步之后的模型名只用于上游请求和审计字段，不影响用户计费模型。

## Token 和凭据

第一阶段需要抽象 `TokenProvider`，避免把 OAuth 刷新逻辑写进调度器。

接口职责：

- 读取账号凭据。
- 判断 access token 是否可用。
- 必要时刷新 token。
- 刷新失败时返回可分类错误。
- 更新账号 token 状态和临时不可调度状态。

OAuth 刷新必须支持并发保护：

- 同一账号同一时间只允许一个刷新流程。
- 进程内使用 singleflight 或账号级锁。
- 数据库写回使用 token version 乐观校验，拒绝旧版本覆盖新 token。
- refresh token 会轮换的 provider 必须在测试中覆盖并发刷新。

OpenAI / ChatGPT 账号池先支持：

- 静态 token 或 API key 类型。
- OAuth refresh token 类型。

Claude / Anthropic 账号后续新增独立 provider。

## 错误处理

账号级错误必须和渠道级错误分开。

账号级处理：

- token 过期且无法刷新：禁用或标记过期账号。
- 429 / rate limit：设置 `RateLimitedUntil`。
- 网络代理错误：记录账号代理错误，必要时临时不可调度。
- 认证错误：标记账号不可用并触发 token 刷新或人工处理。
- 模型不支持：更新账号模型能力或记录映射错误。

渠道级处理：

- 账号池无可用账号：渠道本次失败，交给外层渠道重试。
- 账号池配置错误：渠道失败并在后台显示配置异常。
- 上游协议错误：按现有 relay 错误路径返回。

账号池健康不等于单账号健康。单个账号失败只能影响该账号的临时状态和账号池聚合指标，不能立即把整个本地渠道降权或禁用。账号池渠道的健康聚合规则后续再接入自动优先级，但 Phase 1 必须预留“池内至少一个账号可调度才视为渠道可调度”的合约。

## 监控和指标

第一阶段只预留字段和接口，不把全部监控能力一次性迁入。

需要记录的账号级指标：

- 成功次数
- 失败次数
- 最近错误
- 响应延迟
- 首 token 延迟
- 输入 token
- 输出 token
- 缓存 token
- 缓存率
- 最近使用时间

渠道级监控继续汇总展示。账号池渠道的渠道监控结果可以由账号记录聚合而来，也可以保留渠道级探测任务。

账号池渠道的探测默认是合成探测：检查是否至少存在一个可调度账号，而不是消耗真实账号 quota 去请求上游。真实上游探测可作为后续可选策略，但必须由管理员显式开启。

## 数据库和兼容性

实现必须兼容 SQLite、MySQL 和 PostgreSQL。

- 使用 GORM 迁移。
- JSON 配置字段使用 `TEXT` 存储。
- Go 业务代码使用 `common.Marshal` / `common.Unmarshal` 等项目包装函数。
- 不使用数据库特有 JSON 查询语法。
- 敏感凭据后续应接入现有加密或密钥管理方式；第一阶段不得明文展示完整凭据。
- `SupportedModels`、`ModelMapping`、`CredentialConfig` 等 JSON 文本字段在 Go 内存中解析过滤，不使用数据库 JSON 查询语法。
- 候选账号查询至少需要 `(PoolID, Status)` 索引。
- `RateLimitedUntil`、`TempDisabledUntil` 等时间判断在 Go 里用 UTC 时间完成，不依赖数据库 `NOW()`。
- 热路径调度计数不做每请求数据库写入，避免 SQLite 单写锁和跨库性能问题。

敏感字段从 Phase 1 开始就必须有加密封装。最小实现可以是统一 `SecretEnvelope`：

- 写入数据库前加密账号凭据、token 状态和代理密码。
- 列表 API 只返回掩码，不返回可还原明文。
- 更新 API 把凭据字段视为 write-only。
- 日志和 `RelayInfo.ToString()` 不得序列化账号 token、refresh token、代理密码或完整账号标识。

## UI 边界

第一阶段 UI 只做最小闭环：

- 账号池列表
- 账号列表
- 代理列表
- 账号池渠道绑定
- 账号状态和最近错误

高级可视化、批量导入、账号质量评分、复杂策略编辑器后置。

## 分阶段路线

### 阶段 1：基础模型和管理面

- 新增账号池、账号、代理、渠道绑定模型。
- 新增最小 CRUD API。
- 允许创建账号池绑定渠道，但绑定渠道只能是草稿或强制禁用状态，不能进入外层调度。
- 实现敏感字段加密封装和前端掩码显示。
- 明确模型映射顺序、状态派生规则和运行时状态存储接口。
- 为后续 relay 接入打测试基线。

### 阶段 2A：Relay 生命周期接线

- 用静态单账号 stub 接入 relay。
- 验证 hook 位置、账号失败集合、流式开始标记、并发槽释放和外层 retry 的关系。
- 不引入 OAuth 刷新和复杂调度，先把生命周期问题测透。

### 阶段 2B：OpenAI / ChatGPT 账号池运行时

- 实现 OpenAI 账号调度器。
- 实现 token provider 和并发刷新保护。
- 在 relay 中为账号池渠道注入实际 token、代理和模型映射。
- 支持账号级失败重试和权重选择。
- 使用一个真实 ChatGPT 测试账号做协议 spike，确认标准 OpenAI adapter 是否足够；如果不够，先设计专用 account runtime，再继续迁移。

### 阶段 3：反代能力和协议适配

- 迁移 sub2api 中 OpenAI / ChatGPT 账号反代相关逻辑。
- 支持需要特殊会话或非标准上游接口的账号类型。
- 接入模型映射检测。

### 阶段 4：高级调度和指标闭环

- 引入 session 粘性。
- 引入首 token 延迟、缓存率、错误率和动态可用性评分。
- 将账号池指标反馈到渠道监控和自动优先级系统。

## 测试策略

后端测试优先覆盖真实行为：

- GORM 模型迁移在 SQLite 下可用，并避免数据库专属语法。
- 账号筛选顺序：状态、模型、并发、优先级、权重。
- 同一次请求不会重复选择失败账号。
- token provider 正确处理 token 可用、刷新成功、刷新失败、过期无 refresh token。
- relay 只在账号池渠道中注入账号凭据，普通渠道不受影响。
- 账号池耗尽时外层渠道重试仍然可用。

前端测试只覆盖关键交互：

- 创建账号池。
- 新增账号。
- 绑定本地渠道。
- 显示账号状态和错误。

## 风险

- sub2api 的账号反代逻辑和 Wynth relay 结构差异较大，直接复制会导致隐性协议错误。
- 账号级重试如果和外层渠道重试混在一起，可能破坏严格优先级调度。
- OAuth token 和代理配置属于敏感数据，必须避免日志泄露和前端明文展示。
- 账号池调度会引入并发槽释放问题，需要保证请求成功、失败、panic、客户端断开时都能释放。

## 结论

账号池应作为独立运行时能力接入 Wynth，而不是把账号降级成普通渠道 key。外层继续由 Wynth 渠道调度负责多上游聚合，内层由账号池调度负责账号生命周期和反代细节。第一阶段先建立模型、接口和 OpenAI 账号池运行时边界，避免一次性迁移 sub2api 全部复杂逻辑。
