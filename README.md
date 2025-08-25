# PVC Timing Webhook

这是一个Kubernetes webhook，用于监控PVC（PersistentVolumeClaim）的状态变化并记录时间信息，同时当PVC状态完成时自动添加注解来触发更新。

## 功能特性

### 1. PVC状态时间记录
- 自动记录PVC各个阶段的状态变化时间
- 支持CREATE、UPDATE、DELETE操作
- 使用RFC3339格式记录时间戳

### 2. PVC状态完成监听
- 实时监听PVC状态变化
- 当PVC状态变为`ClaimBound`时自动添加`webhook-trigger`注解
- 注解值使用Unix时间戳格式

### 3. 双重触发机制
- **Webhook触发**: 通过mutating webhook在PVC创建/更新时记录时间
- **控制器触发**: 通过PVC控制器监听状态变化并添加注解

## 部署要求

### 环境变量
```bash
# TLS证书文件路径
export TLS_CERT_FILE=/path/to/tls.crt
export TLS_KEY_FILE=/path/to/tls.key

# 可选：Kubeconfig文件路径（集群外部署时使用）
export KUBECONFIG=/path/to/kubeconfig
```

### 权限要求
控制器需要以下RBAC权限：
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pvc-timing-controller
rules:
- apiGroups: [""]
  resources: ["persistentvolumeclaims"]
  verbs: ["get", "list", "watch", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pvc-timing-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: pvc-timing-controller
subjects:
- kind: ServiceAccount
  name: pvc-timing-webhook
  namespace: default
```

## 使用方法

### 1. 启动服务
```bash
go run src/main.go
```

### 2. 创建PVC
```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nginx-pvc-1
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
EOF
```

### 3. 查看PVC状态和注解
```bash
# 查看PVC状态
kubectl get pvc nginx-pvc-1

# 查看PVC详细信息（包括时间记录）
kubectl describe pvc nginx-pvc-1

# 查看PVC的注解
kubectl get pvc nginx-pvc-1 -o jsonpath='{.metadata.annotations}'
```

### 4. 手动触发更新（可选）
```bash
# 手动添加webhook-trigger注解
kubectl annotate pvc nginx-pvc-1 webhook-trigger=$(date +%s) --overwrite
```

## 注解说明

### pvc-timing 注解
记录PVC各个阶段的时间信息：
```json
{
  "currentPhase": "Bound",
  "timings": [
    {
      "phase": "Pending",
      "time": "2024-01-01T10:00:00Z"
    },
    {
      "phase": "Bound",
      "time": "2024-01-01T10:05:00Z"
    }
  ]
}
```

### webhook-trigger 注解
当PVC状态变为`ClaimBound`时自动添加，值为Unix时间戳：
```
webhook-trigger: "1704110400"
```

## 架构说明

### 组件结构
1. **Mutating Webhook**: 处理PVC的创建/更新请求，记录时间信息
2. **PVC Controller**: 监听PVC状态变化，在完成时添加注解
3. **Kubernetes Client**: 与Kubernetes API交互

### 工作流程
1. PVC创建/更新时，webhook记录时间信息
2. 控制器持续监听PVC状态变化
3. 当PVC状态变为`ClaimBound`时，自动添加`webhook-trigger`注解
4. 注解值使用当前Unix时间戳，可用于触发其他工作流

## 故障排除

### 常见问题
1. **权限不足**: 确保ServiceAccount有足够的RBAC权限
2. **证书问题**: 检查TLS证书文件路径和有效性
3. **网络连接**: 确保能够连接到Kubernetes API服务器

### 日志查看
```bash
# 查看webhook日志
kubectl logs -f deployment/pvc-timing-webhook

# 查看控制器日志
kubectl logs -f deployment/pvc-timing-webhook | grep "PVC controller"
```
