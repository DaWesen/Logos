# Logos - K8s 完整学习指南

欢迎来到 Logos 的 K8s 完整学习环境！这里包含了生产级 K8s 的所有核心功能。

---

## 📋 目录

1. [部署方式](#部署方式)
2. [核心概念](#核心概念)
3. [高级功能](#高级功能)
4. [常用命令](#常用命令)
5. [实战练习](#实战练习)
6. [故障排查](#故障排查)

---

## 🚀 部署方式

### 方式一：快速基础部署（无高级功能）

适合刚开始学习 K8s 基础：

```powershell
cd deployment
.\deploy.ps1
```

### 方式二：完整部署（带 Istio，支持所有高级功能）

适合学习服务网格、金丝雀发布等高级功能：

```powershell
cd deployment
.\deploy-istio.ps1
```

---

## 🧱 核心概念

### 1. Pod
最小调度单元，包含一个或多个容器。

```powershell
# 查看 Pod
kubectl get pods -n logos
kubectl get pods -n logos -w  # 实时观察

# 查看 Pod 详情
kubectl describe pod <pod-name> -n logos

# 查看 Pod 日志
kubectl logs <pod-name> -n logos
kubectl logs <pod-name> -n logos -f  # 实时跟踪

# 进入 Pod 执行命令
kubectl exec -it <pod-name> -n logos -- sh
```

### 2. Deployment
管理 Pod 的部署和更新。

```powershell
# 查看 Deployment
kubectl get deployments -n logos

# 更新 Deployment（滚动更新）
kubectl set image deployment/gateway gateway=logos:v2 -n logos

# 回滚
kubectl rollout undo deployment/gateway -n logos

# 查看更新历史
kubectl rollout history deployment/gateway -n logos
```

### 3. Service
服务发现和负载均衡。

```powershell
# 查看 Service
kubectl get svc -n logos

# 查看 Endpoints
kubectl get endpoints -n logos
```

### 4. ConfigMap & Secret
配置管理。

```powershell
# 查看 ConfigMap
kubectl get configmap -n logos
kubectl get configmap logos-config -n logos -o yaml

# 查看 Secret
kubectl get secret -n logos
```

---

## 🔧 高级功能

### ✨ 1. 滚动更新（Rolling Update）

**功能**：零停机更新应用，逐步替换旧版本 Pod。

**使用方式**：
```powershell
# 应用滚动更新配置
kubectl apply -f examples/rolling-update.yaml

# 执行滚动更新
kubectl set image deployment/gateway gateway=logos:v2 -n logos

# 观察更新过程
kubectl rollout status deployment/gateway -n logos

# 暂停更新（金丝雀发布时有用）
kubectl rollout pause deployment/gateway -n logos

# 继续更新
kubectl rollout resume deployment/gateway -n logos

# 回滚
kubectl rollout undo deployment/gateway -n logos
```

**文件**：[examples/rolling-update.yaml](examples/rolling-update.yaml)

---

### 🔵🟢 2. 蓝绿部署（Blue-Green）

**功能**：同时部署两个版本，随时切换流量，回滚最快。

**使用方式**：
```powershell
# 部署 v1（蓝版）
kubectl apply -f examples/blue-green.yaml

# 确认 v1 正常运行后，编辑 blue-green.yaml，取消 v2 的注释并部署
kubectl apply -f examples/blue-green.yaml

# 切换流量到 v2：编辑 Service 的 selector 从 gateway-v1 改成 gateway-v2
kubectl edit svc gateway-svc -n logos

# 确认没问题后，删除 v1
kubectl delete deployment gateway-v1 -n logos
```

**文件**：[examples/blue-green.yaml](examples/blue-green.yaml)

---

### 🐦 3. 金丝雀发布（Canary）

**功能**：先把少量流量引导到新版本，确认没问题再全量发布。

有两种方式：

#### A. 按流量权重
```powershell
# 应用金丝雀配置（10% 流量到 v2）
kubectl apply -f canary-bot.yaml

# 观察流量分布（用 Kiali）
istioctl dashboard kiali

# 逐步增加 v2 流量
kubectl edit virtualservice bot -n logos  # 修改 weight 值
```

#### B. 按 Header
```powershell
# 只让带特定 Header 的请求到 v2
kubectl apply -f canary-bot-header.yaml

# 测试：
curl http://localhost:80/...                    # 到 v1
curl -H "x-canary: true" http://localhost:80/... # 到 v2
```

**文件**：
- [canary-bot.yaml](../canary-bot.yaml)
- [canary-bot-header.yaml](../canary-bot-header.yaml)
- [README_CANARY.md](../README_CANARY.md)

---

### 📈 4. 水平自动扩缩容（HPA）

**功能**：根据负载自动增加或减少 Pod 数量。

**使用方式**：
```powershell
# 启用 metrics-server（HPA 需要）
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml

# 应用 HPA 配置
kubectl apply -f examples/hpa.yaml

# 查看 HPA 状态
kubectl get hpa -n logos
kubectl describe hpa gateway-hpa -n logos

# 测试：手动增加负载
kubectl run -i --tty load-generator --rm --image=busybox:1.28 --restart=Never -n logos -- /bin/sh -c "while sleep 0.01; do wget -q -O- http://gateway-svc:8888/health; done"
```

**文件**：[examples/hpa.yaml](examples/hpa.yaml)

---

### 🔒 5. Pod 中断预算（PDB）

**功能**：确保节点维护等情况下服务高可用。

**使用方式**：
```powershell
# 应用 PDB
kubectl apply -f examples/pdb.yaml

# 查看 PDB
kubectl get pdb -n logos

# 测试：尝试驱逐节点上的 Pod
kubectl drain <node-name> --ignore-daemonsets
```

**文件**：[examples/pdb.yaml](examples/pdb.yaml)

---

### 💾 6. 持久化存储（PVC）

**功能**：数据持久化，Pod 重启数据不丢。

**使用方式**：
```powershell
# 应用 PVC 配置
kubectl apply -f examples/pvc.yaml

# 查看 PVC
kubectl get pvc -n logos

# 查看 PV
kubectl get pv
```

**文件**：[examples/pvc.yaml](examples/pvc.yaml)

---

### 🎯 7. 节点亲和/反亲和

**功能**：精确控制 Pod 调度位置，高可用优化。

**使用方式**：
```powershell
# 应用亲和配置
kubectl apply -f examples/affinity.yaml

# 查看 Pod 分布
kubectl get pods -n logos -o wide

# 给节点打标签
kubectl label nodes kind-worker disktype=ssd
```

**文件**：[examples/affinity.yaml](examples/affinity.yaml)

---

### 🔐 8. 安全上下文 & 网络策略

**功能**：安全加固，限制权限和网络访问。

**使用方式**：
```powershell
# 应用安全配置
kubectl apply -f examples/security.yaml

# 查看网络策略
kubectl get networkpolicy -n logos
```

**文件**：[examples/security.yaml](examples/security.yaml)

---

### 🚨 9. Prometheus 告警

**功能**：监控告警，及时发现问题。

**前置**：需要安装 Prometheus Operator。

```powershell
# 应用告警规则
kubectl apply -f examples/prometheus-rules.yaml

# 查看告警
kubectl get prometheusrules -n logos
```

**文件**：[examples/prometheus-rules.yaml](examples/prometheus-rules.yaml)

---

## 💻 常用命令速查

### 资源查看
| 命令 | 说明 |
|-----|------|
| `kubectl get pods -n logos` | 查看 Pod |
| `kubectl get deployments -n logos` | 查看 Deployment |
| `kubectl get svc -n logos` | 查看 Service |
| `kubectl get nodes` | 查看节点 |
| `kubectl get all -n logos` | 查看所有资源 |
| `kubectl describe <resource> <name> -n logos` | 查看资源详情 |

### 日志和调试
| 命令 | 说明 |
|-----|------|
| `kubectl logs <pod-name> -n logos` | 查看 Pod 日志 |
| `kubectl logs <pod-name> -n logos -f` | 实时跟踪日志 |
| `kubectl exec -it <pod-name> -n logos -- sh` | 进入 Pod |
| `kubectl top pods -n logos` | 查看 Pod 资源使用 |
| `kubectl top nodes` | 查看节点资源使用 |

### 部署操作
| 命令 | 说明 |
|-----|------|
| `kubectl apply -f <file.yaml>` | 应用配置 |
| `kubectl delete -f <file.yaml>` | 删除配置 |
| `kubectl set image deployment/<name> <container>=<image> -n logos` | 更新镜像 |
| `kubectl rollout status deployment/<name> -n logos` | 查看更新状态 |
| `kubectl rollout undo deployment/<name> -n logos` | 回滚 |
| `kubectl scale deployment/<name> --replicas=<n> -n logos` | 手动扩缩容 |

### Istio 相关
| 命令 | 说明 |
|-----|------|
| `istioctl install` | 安装 Istio |
| `istioctl dashboard kiali` | 打开 Kiali |
| `istioctl dashboard grafana` | 打开 Grafana |
| `istioctl dashboard jaeger` | 打开 Jaeger |
| `kubectl get virtualservices -n logos` | 查看路由规则 |
| `kubectl get destinationrules -n logos` | 查看目标规则 |

---

## 🎮 实战练习

### 练习 1：基础部署
1. 运行 `.\deploy.ps1` 部署基础环境
2. 用 `kubectl get pods -n logos -w` 观察 Pod 启动
3. 访问 http://localhost:30080 验证服务

### 练习 2：滚动更新
1. 应用 `examples/rolling-update.yaml`
2. 用 `kubectl set image` 部署新版本
3. 用 `kubectl rollout status` 观察更新过程
4. 尝试回滚

### 练习 3：金丝雀发布
1. 运行 `.\deploy-istio.ps1`
2. 部署 Bot 服务的 v1 和 v2 版本
3. 应用 `canary-bot.yaml`
4. 用 Kiali 观察流量分布
5. 逐步调整流量比例
6. 尝试按 Header 的方式

### 练习 4：HPA 自动扩缩容
1. 应用 `examples/hpa.yaml`
2. 启动负载生成器
3. 观察 HPA 自动增加 Pod
4. 停止负载，观察自动缩容

### 练习 5：蓝绿部署
1. 应用 `examples/blue-green.yaml`
2. 部署 v2 版本
3. 切换流量到 v2
4. 确认没问题后删除 v1

### 练习 6：安全加固
1. 应用 `examples/security.yaml`
2. 尝试用 `kubectl exec` 进 Pod，看能否做危险操作
3. 测试网络策略限制

---

## 🔍 故障排查

### Pod 无法启动
```powershell
# 查看 Pod 状态
kubectl describe pod <pod-name> -n logos

# 查看事件
kubectl get events -n logos --sort-by='.lastTimestamp'
```

### 服务无法访问
```powershell
# 检查 Pod 是否就绪
kubectl get pods -n logos

# 检查 Service 和 Endpoints
kubectl get svc -n logos
kubectl get endpoints -n logos

# 用临时 Pod 测试集群内访问
kubectl run -i --tty debug --rm --image=busybox:1.28 --restart=Never -n logos -- sh
wget http://gateway-svc:8888/health
```

### Istio 问题
```powershell
# 查看 Sidecar 状态
istioctl proxy-status

# 查看路由配置
istioctl proxy-config routes <pod-name> -n logos

# 查看 Sidecar 日志
kubectl logs <pod-name> -n logos -c istio-proxy
```

---

## 📚 学习资源

| 资源 | 链接 |
|-----|------|
| K8s 官方文档 | https://kubernetes.io/docs/ |
| Istio 官方文档 | https://istio.io/latest/docs/ |
| K8s by Example | https://kubernetesbyexample.com/ |

---

祝学习愉快！🎉
