# APC

Aggregate Public Components — Go 微服务公共组件库。

```text
go get github.com/ethereal3x/apc
```

要求 Go **1.24+**。配置在进程启动时一次性加载 YAML，**不支持热更新**。

## 包一览

| 包 | 职责 |
|---|---|
| [`config`](#配置加载) | YAML 配置加载与全局访问 |
| [`server`](#server-grpc--gateway) | gRPC + grpc-gateway、CORS、Recovery、优雅退出 |
| [`logger`](#logger) | Zap 日志，支持 Trace/Span 字段 |
| [`tracing`](#tracingotlp-http) | OpenTelemetry OTLP HTTP 上报 |
| [`errs`](#rpc-错误处理) | 业务错误码与 gRPC handler 收敛 |
| [`orm`](#orm) | GORM 初始化（MySQL / Postgres，读写分离） |
| [`cache`](#cache) | Redis 封装与分布式锁 |
| [`ratelimit`](#ratelimit) | 基于 Redis GCRA 的分布式限流 |
| [`message`](#message) | Redis Streams 发布 / 消费 |
| [`storage`](#storage) | S3 兼容对象存储（MinIO / RustFS） |
| [`pool`](#pool--scheduler) | 协程池 |
| [`scheduler`](#pool--scheduler) | 秒级 Cron 调度 |
| [`tool`](#tool--structure) | HTTP Client、Snowflake、随机数 |
| [`structure`](#tool--structure) | 泛型链表 / 队列 / 栈 |

---

## 配置加载

路径解析优先级：

1. `Load(LoadOptions{Path: "..."})` 显式指定
2. 环境变量 `APC_CONFIG_PATH`
3. 默认相对路径 `config.yaml`

```go
// 推荐：main 启动时显式加载
config.MustLoad(config.LoadOptions{Path: os.Getenv("APC_CONFIG_PATH")})

// 兼容：首次 GetConf() 时懒加载（失败会 fatal）
cfg := config.GetConf()
```

### 配置结构

```yaml
server:
  grpc_addr: ":9090"
  gateway_addr: ":8080"
  env: dev

client:
  - name: mysql
    addr: "user:pass@tcp(127.0.0.1:3306)/db?parseTime=true&debug=true&max_idle=10&max_active=50"
    slave:
      - "user:pass@tcp(127.0.0.1:3307)/db?parseTime=true"
  - name: redis
    addr: "127.0.0.1:6379"

plugin:
  log:
    level: info          # debug / info / warn / error
    format: console      # console / json
    logfile: ""          # 空则 stdout
  tracing:
    service_name: my-service
    sampler:
      type: ratio        # const | ratio
      param: 1.0
    reporter:
      collector_endpoint: http://localhost:4318/v1/traces
  minio:
    endpoint: "127.0.0.1:9000"
    access_key: "..."
    secret_key: "..."
    bucket_name: "bucket"
    use_ssl: false
    region: us-east-1
  rustfs:
    endpoint: "..."
    access_key: "..."
    secret_key: "..."
    bucket_name: "bucket"
    use_ssl: true
```

| 环境 | 配置来源 | 说明 |
|---|---|---|
| 本地开发 | 工作目录 `config.yaml` | 直接 `go run` |
| Docker Compose | volume 挂载固定路径 | 启动前 `MustLoad` |
| Kubernetes | initContainer 从 Consul KV wget 到共享 volume | `APC_CONFIG_PATH=/etc/app/config.yaml` |

库内不集成 Consul SDK；外部存储仅作静态文件托管。业务侧需保证配置文件在进程启动前就绪。

DSN query 中可带连接池参数（`debug` / `max_idle` / `max_active` / `max_lifetime` / `driver`），`GenGormConfig` 会剥离后再建连。Postgres 支持 URL 或 `host=...` 键值 DSN。

---

## Server（gRPC + Gateway）

```go
config.MustLoad(config.LoadOptions{})
logger.SetLogger(logger.NewLogger(&config.GetConf().Plugin.Log))

grpcServer := server.NewRpcServer(config.GetConf().Server.GrpcAddr)
grpcServer.SetRegisterFunc(func(s grpc.ServiceRegistrar) {
    pb.RegisterUserServiceServer(s, userSvc)
})

httpServer := server.NewHttpServer(config.GetConf().Server.GatewayAddr)
httpServer.SetRegisterFunc(func(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
    return pb.RegisterUserServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
})
_ = httpServer.SetCORSConfig(server.CORSConfig{
    Enabled:        true,
    AllowedOrigins: []string{"https://app.example.com"},
    AllowCredentials: true,
})

server.RunGrpcGatewayService(grpcServer, httpServer)
```

`RunGrpcGatewayService` / `RunGrpcGatewayServiceContext` 会：

- 按配置初始化 / 关闭 Tracing Provider
- 写入 PID 文件（`/data/tmp/<binary>.pid`，非 Windows；存在时对旧进程发 SIGTERM）
- 并行启动 gRPC 与 HTTP；任一失败则取消其余并优雅退出

空地址的服务会被跳过。HTTP 处理链（外→内）：**Tracing → Recovery → 业务 Middleware → CORS → gateway mux**。

Gateway 默认：proto JSON 字段名、emit defaults；响应头写入 `Tracing-Id`；默认 write timeout 15s（`SetWriteTimeout(0)` 可关闭，适合流式）。

### CORS

新服务应使用 `SetCORSConfig`。`Enabled=false` 时不写跨域头，也不拦截 `OPTIONS`。

```go
err := httpServer.SetCORSConfig(server.CORSConfig{
    Enabled:          true,
    AllowedOrigins:   []string{"https://app.example.com"},
    AllowedMethods:   []string{"GET", "POST"},
    AllowedHeaders:   []string{"Content-Type", "Authorization", "X-CSRF-Token"},
    ExposedHeaders:   []string{"X-Request-ID"},
    AllowCredentials: true,
    MaxAge:           10 * time.Minute,
})
```

`AllowCredentials=true` 时禁止通配 Origin。精确白名单会回写请求 Origin 并设置 `Vary: Origin`。

`SetCORSAllowedHeaders` 仅为旧服务保留：有 Origin 时回显具体 Origin 并允许 credentials，不再返回 `*`。生产环境请改用 `SetCORSConfig`。

### Panic Recovery

HTTP panic 默认返回固定 JSON 500；gRPC panic 默认返回 `codes.Internal`。panic 值与堆栈只进服务端日志，不进客户端响应。可通过 `SetRecoveryHandler` 自定义，响应中同样不得泄漏内部细节。

---

## RPC 错误处理

```go
return errs.Handle(&userpb.GetUserResponse{}, func(rsp *userpb.GetUserResponse) error {
    user, err := logic.GetUser(ctx, req.GetId())
    if err != nil {
        return err
    }
    rsp.User = dto.ToProto(user)
    return nil
})
```

- `BizError`（`errs.New` / 预定义错误）写入 reply 的 `Code` / `Message`（实现 `ErrorReply` 或反射同名字段）
- 非业务错误由 `GenProtoReply` 作为 gRPC error 向上传递
- `HandleValue` 适合「先取结果再填 reply」；`GenProtoReply` 仍可用

---

## Logger

```go
log := logger.NewLogger(&config.GetConf().Plugin.Log)
logger.SetLogger(log) // 包级 Context* / orm SQL 日志依赖此全局实例

logger.ContextInfo(ctx, "hello", zap.String("k", "v"))
```

未 `SetLogger` 时调用包级 `L()` / `Context*` 会 panic。YAML 字段 `logfile` 对应输出路径；空则控制台 stdout。

---

## Tracing（OTLP HTTP）

`collector_endpoint` 为空时不初始化 exporter，服务可正常启动。使用 `RunGrpcGatewayService*` 时会自动 `InitProvider`；也可手动：

```go
shutdown, err := tracing.InitProvider(config.GetConf().Plugin.Tracing)
if err != nil {
    return err
}
defer shutdown(context.Background())

ctx, span := tracing.Start(ctx, "op")
defer span.End()
```

### 本地开发

```yaml
plugin:
  tracing:
    service_name: my-service
    sampler:
      type: ratio
      param: 1.0
    reporter:
      collector_endpoint: http://localhost:4318/v1/traces
```

### HTTPS + Basic Auth

```yaml
plugin:
  tracing:
    service_name: mint-server
    sampler:
      type: ratio
      param: 1.0
    reporter:
      collector_endpoint: otel.example.com:443
      url_path: /v1/traces
      insecure: false
      tls_skip_verify: true   # 自签证书场景；与 insecure 不同
      auth:
        username: otel
        password: "${OTEL_AUTH_PASSWORD}"
```

也支持完整 URL：`collector_endpoint: https://otel.example.com/v1/traces`。

| 字段 | 说明 |
|---|---|
| `collector_endpoint` | `host:port` 或完整 URL |
| `url_path` | 仅 `host:port` 模式，默认 `/v1/traces` |
| `insecure` | HTTP 明文；`host:port` 默认 `false`（HTTPS） |
| `tls_skip_verify` | HTTPS 跳过证书校验 |
| `headers` | 自定义 HTTP 头 |
| `auth` | Basic 认证；与 `headers.Authorization` 同时存在时优先 `auth` |

传播：B3 + W3C。业务可用 `tracing.TraceID` / `SpanID` / `RecordError`。

---

## ORM

```go
db, err := orm.NewGormInstance(config.GenGormConfig(config.GetClientConf("mysql")))
```

支持 MySQL / Postgres；配置了 `slave` 时启用读写分离（随机策略）。DSN `debug=true` 会打开 SQL Info 日志并 `db.Debug()`。请先 `logger.SetLogger`，否则 SQL 日志依赖的全局 logger 可能未就绪。`GenGormConfig` 在 client 为 nil 或 DSN 非法时会 panic。

---

## Cache

```go
rdb := cache.NewRedisClient(redis.NewClient(&redis.Options{Addr: addr}))
defer rdb.Close()

val, err := rdb.Get(ctx, "key") // 缺失 key 返回 ""，不是 redis.Nil

lock := cache.NewRedisLock(rdb, "lock:order:1")
err = lock.Run(ctx, func(ctx context.Context) error {
    // 临界区；默认 TTL 30s，Run 内自动续约
    return nil
})
```

提供 KV / Hash / Set / ZSet / List / Stream 等常用封装与 `Pipeline`。分布式锁优先用 `Run`；`TryLock` 仅兼容保留。

---

## Ratelimit

基于 `go-redis/redis_rate` 的 GCRA 漏桶，依赖 `cache.RedisClient`。

```go
limiter := ratelimit.NewLimiter(rdb)
result, err := limiter.Allow(ctx, "user:42", ratelimit.LimitConfig{
    Rate: 100, Period: time.Minute, Burst: 100,
})
if !result.Allowed {
    // result.RetryAfter
}

group := ratelimit.NewRuleGroup(limiter, []ratelimit.Rule{
    {Name: "ip", Key: func(ctx context.Context) string { return ip }, Config: ...},
    {Name: "user", Key: func(ctx context.Context) string { return userID }, Config: ...},
})
denied, err := group.Check(ctx) // 返回首个拒绝规则名；全过则为 ""
```

---

## Message

Redis Streams 发布 / 消费组消费。

```go
pub := message.NewPublisher(redisClient)
_ = pub.Publish(ctx, "topic", message.NewEnvelope(payloadJSON))

consumer := message.NewConsumer(redisClient, message.ConsumerConfig{
    Group: "svc", Topic: "topic", Consumer: "pod-a",
}, handler)
go message.Consume(ctx, consumer) // 或 consumer.Run(ctx)；用 ctx 取消停止
```

Handler 成功才 `XAck`；失败不 ACK，可被重新投递。启动时 `XAutoClaim` 认领超时 pending。消息体放在 stream 字段 `data`。

---

## Storage

S3 兼容对象存储，抽象为 `ObjectStorage`（上传 / 下载 / 删除 / 公共 URL / 预签名 / 分片）。

```go
client, err := storage.NewS3Client(ctx, storage.NewS3ClientParams{
    Provider: storage.STORAGE_PROVIDER_MINIO, // 或 STORAGE_PROVIDER_RUSTFS
    Config:   &config.GetConf().Plugin.Minio,
})
```

初始化时校验 bucket 存在；默认 region `us-east-1`。配置对应 `plugin.minio` / `plugin.rustfs`。

---

## Pool / Scheduler

```go
exec := pool.NewExecutor(4, 100) // worker, queue；queue 省略则等于 worker 数
defer exec.Close()
_ = exec.Submit(ctx, func(ctx context.Context) { /* ... */ })
exec.Wait()

sched := scheduler.New()
_ = sched.AddFunc(ctx, "0 */5 * * * *", func(ctx context.Context) { /* 6 段 cron，含秒 */ })
sched.Start()
sched.StopWithContext(ctx) // ctx 取消时停止
```

协程池会隔离任务 panic；`Close` 会排空已入队任务。

---

## Tool / Structure

**tool**

```go
resp, err := tool.NewHttpClient(ctx).
    SetMethod(http.MethodGet).
    SetUrl(url).
    Do()
var out Foo
_ = tool.UnmarshalBody(resp, &out) // 默认 5s 超时，带 OTel span（URL 脱敏）

tool.InitSnowflake(1) // 进程内一次，workerID 0–1023
id := tool.GenSnowflakeID()
```

**structure**：泛型 `LinkedList` / `Queue` / `Stack`，进程内数据结构，非并发安全。

---

## 测试

单元测试直接 `go test ./...`。依赖外部资源的集成测试使用 `//go:build integration` 标签。
