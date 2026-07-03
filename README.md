## APC
Aggregate Public Components

### 配置加载

APC 在启动时一次性读取 YAML 配置文件，**不支持热更新**。Consul 等外部存储仅作为静态文件托管（如 K8s initContainer wget），库内不集成 Consul SDK。

#### 路径解析优先级

1. `Load(LoadOptions{Path: "..."})` 显式指定路径
2. 环境变量 `APC_CONFIG_PATH`
3. 默认相对路径 `config.yaml`

#### 使用方式

```go
// 方式一：main 启动时显式加载（推荐）
config.MustLoad(config.LoadOptions{Path: os.Getenv("APC_CONFIG_PATH")})

// 方式二：保持向后兼容，首次 GetConf() 时懒加载
cfg := config.GetConf()
```

#### 多环境部署约定

| 环境 | 配置来源 | 说明 |
|---|---|---|
| 本地开发 | 工作目录 `config.yaml` | 直接 `go run` |
| Docker Compose | volume 挂载到容器内固定路径 | 启动前 `MustLoad` 指定挂载路径 |
| Kubernetes | initContainer 从 Consul KV wget 到共享 volume | 主容器 `APC_CONFIG_PATH=/etc/app/config.yaml` |

三种环境最终都汇聚为同一次 `os.ReadFile`，业务侧负责保证文件在进程启动前就绪。

### RPC 错误处理

`errs.Handle` 用于收敛 gRPC handler 中的 `GenProtoReply` 样板：

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

`GenProtoReply` 保持可用，新旧 API 语义一致。
