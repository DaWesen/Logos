# Logos 金丝雀发布操作指南

## 前置准备

### 1. 安装 Istio

首次部署前需要安装 Istio：

```powershell
cd deployment
.\deploy-istio.ps1
```

这个脚本会：
- 创建 kind 集群
- 安装 Istio
- 部署所有 Logos 服务
- 配置 Istio 网关

### 2. 访问监控面板

部署完成后可以访问：
- **Kiali** (可观测性): http://localhost:20001
- **Grafana** (指标): http://localhost:3000
- **Gateway**: http://localhost:80

## 金丝雀发布流程（以 Bot 服务为例）

### 步骤 1: 准备 v2 版本的镜像

首先构建包含新功能的 Bot 服务镜像，假设我们用 `logos:v2-canary` 标签：

```powershell
# 在 Dockerfile 或代码中添加新功能后
docker build -t logos:v2-canary ..
kind load docker-image logos:v2-canary --name logos
```

### 步骤 2: 部署 v2 版本

更新 [services/bot-canary.yaml](services/bot-canary.yaml)，修改 bot-v2 的镜像：

```yaml
containers:
  - name: bot
    image: logos:v2-canary  # 这里改成新镜像
```

然后部署 v1 和 v2 两个版本：

```powershell
kubectl apply -f services/bot-canary.yaml
```

验证两个版本的 Pod 都在运行：

```powershell
kubectl get pods -n logos -l app=bot
```

### 步骤 3: 启用金丝雀发布（按权重分流）

有两种方式进行金丝雀发布：

#### 方式 A: 按流量权重分流（推荐）

90% 流量到 v1（稳定版），10% 流量到 v2（金丝雀版）：

```powershell
kubectl apply -f canary-bot.yaml
```

查看当前路由规则：

```powershell
kubectl get virtualservice bot -n logos -o yaml
```

#### 方式 B: 按 Header 分流（仅特定用户测试）

只有 Header `x-canary: true` 的请求才会被路由到 v2：

```powershell
kubectl apply -f canary-bot-header.yaml
```

测试方式：
```powershell
# 普通请求 -> v1
curl http://localhost:80/api/v1/bot/...

# 测试请求 -> v2
curl -H "x-canary: true" http://localhost:80/api/v1/bot/...
```

### 步骤 4: 逐步增加 v2 流量

如果 v2 运行正常，可以逐步增加其流量：

**编辑** [canary-bot.yaml](canary-bot.yaml)，修改 `weight` 值：

```yaml
- destination:
    host: bot-svc
    subset: v1
  weight: 50    # 90 -> 50
- destination:
    host: bot-svc
    subset: v2
  weight: 50    # 10 -> 50
```

应用变更：
```powershell
kubectl apply -f canary-bot.yaml
```

继续观察，最终可以将 v2 权重调整到 100%。

### 步骤 5: 完全切换到 v2（或回滚）

#### 完全切换到 v2

更新 [canary-bot.yaml](canary-bot.yaml):
```yaml
- destination:
    host: bot-svc
    subset: v1
  weight: 0
- destination:
    host: bot-svc
    subset: v2
  weight: 100
```

```powershell
kubectl apply -f canary-bot.yaml
```

确认没有问题后，可以删除 v1 的 deployment：
```powershell
kubectl delete deployment bot-v1 -n logos
```

#### 回滚到 v1

如果 v2 有问题，快速回滚：
```yaml
- destination:
    host: bot-svc
    subset: v1
  weight: 100
- destination:
    host: bot-svc
    subset: v2
  weight: 0
```

```powershell
kubectl apply -f canary-bot.yaml
```

### 步骤 6: 监控金丝雀发布

使用 Kiali 观察流量分布和服务健康：
```powershell
istioctl dashboard kiali
```

或者使用 Grafana 查看指标：
```powershell
istioctl dashboard grafana
```

## 其他服务的金丝雀发布

为其他服务添加金丝雀发布的步骤类似：

1. 创建类似 `canary-<service>.yaml` 的 VirtualService + DestinationRule
2. 创建 `<service>-canary.yaml` 的两个 Deployment（v1 和 v2）
3. 按照上述步骤操作

## 常用命令

| 命令 | 说明 |
|-----|-----|
| `kubectl get virtualservices -n logos` | 查看所有路由规则 |
| `kubectl get destinationrules -n logos` | 查看目标规则 |
| `istioctl proxy-status` | 查看 Sidecar 状态 |
| `istioctl dashboard kiali` | 打开 Kiali 面板 |
| `kubectl logs <pod-name> -c istio-proxy -n logos` | 查看 Istio Sidecar 日志 |

## 清理金丝雀配置

如果不需要金丝雀发布了，可以删除配置：

```powershell
kubectl delete -f canary-bot.yaml
kubectl delete -f canary-bot-header.yaml
```

恢复单一版本部署：
```powershell
kubectl apply -f services/bot.yaml
```
