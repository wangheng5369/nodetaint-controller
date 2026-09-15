# NodeTaint-Controller 项目完整指南

本文档详细介绍如何使用 Kubebuilder 从零构建一个生产级的 Kubernetes Operator，完整覆盖：CRD 设计 → API 定义 → Reconcile 业务逻辑 → CRD 部署 → Controller 启动 → CR 创建 → 验证。

---

## 目录

1. [项目概述](#1-项目概述)
2. [前置条件](#2-前置条件)
3. [CRD 设计](#3-crd-设计)
4. [定义 API 类型](#4-定义-api-类型)
5. [业务逻辑 Reconcile](#5-业务逻辑-reconcile)
6. [生成 CRD YAML](#6-生成-crd-yaml)
7. [安装 CRD 到集群](#7-安装-crd-到集群)
8. [启动 Controller](#8-启动-controller)
9. [创建 CR 并验证](#9-创建-cr-并验证)
10. [常见问题](#10-常见问题)

---

## 1. 项目概述

### 1.1 项目背景

NodeTaint-Controller 是一个基于 Kubebuilder 框架构建的 Kubernetes Operator，用于**根据节点资源使用率自动打污点**。

核心功能：
- 用户通过 CR（Custom Resource）定义规则（阈值 + 污点 + 节点选择器）
- Controller 自动监控所有节点的资源使用情况
- 超阈值自动打污点，低于阈值自动移除污点
- 支持针对不同类型节点配置不同规则

### 1.2 项目结构

```
nodetaint-controller/
├── api/v1/                          # CRD 类型定义
│   ├── nodetaintrule_types.go       # NodeTaintRule CRD 字段定义
│   ├── groupversion_info.go         # GroupVersion 信息
│   └── zz_generated.deepcopy.go     # 自动生成的深拷贝代码
├── internal/controller/              # Controller 业务逻辑
│   └── nodetaintrule_controller.go  # Reconcile 实现
├── cmd/
│   └── main.go                      # Manager 入口
├── config/                          # Kubernetes 配置文件
│   ├── crd/bases/                   # 生成的 CRD YAML
│   ├── rbac/                        # RBAC 配置
│   ├── manager/                     # Manager Deployment
│   └── samples/                     # CR 示例
├── Makefile                         # 构建脚本
└── PROJECT                          # Kubebuilder 项目元数据
```

---

## 2. 前置条件

### 2.1 安装依赖

```bash
# 1. Go 1.21+
go version

# 2. Docker（用于构建镜像）
docker version

# 3. Kubernetes 集群（kind/minikube/k3s）
kubectl version

# 4. Kubebuilder（项目初始化用，已通过 make controller-gen 调用）
```

### 2.2 初始化项目（已完成后可跳过）

如果从零创建项目：

```bash
# 创建目录
mkdir nodetaint-controller && cd nodetaint-controller

# 初始化 kubebuilder 项目
kubebuilder init --domain node.io --repo github.com/wangheng5369/nodetaint-controller

# 创建 API（生成 api/v1/ 和 internal/controller/）
kubebuilder create api --group nodeops --version v1 --kind NodeTaintRule --controller --resource --namespaced
```

### 2.3 初始化后的关键文件

| 文件 | 作用 |
|------|------|
| `api/v1/nodetaintrule_types.go` | 定义 CRD 的 Spec/Status 字段 |
| `internal/controller/nodetaintrule_controller.go` | Reconcile 业务逻辑 |
| `config/crd/bases/*.yaml` | 生成的 CRD 清单 |
| `Makefile` | 所有构建/部署命令 |

---

## 3. CRD 设计

### 3.1 CRD 要解决什么问题？

**背景**：Kubernetes 原生的 Taint/Toleration 机制需要手动管理（kubectl taint）。

**痛点**：
- 无法根据节点实时指标自动打污点
- 多节点需要手动一个个操作
- 规则变更需要重新执行命令

**解决方案**：通过 CR 定义规则，让 Controller 自动执行。

### 3.2 CRD 字段设计

```yaml
apiVersion: nodeops.node.io/v1
kind: NodeTaintRule
metadata:
  name: cpu-high-rule
spec:
  # 通过 label 选择要管理的节点
  nodeSelector:
    node-role: worker

  # 资源使用率阈值配置
  # 任意一项超过阈值就触发打污点
  threshold:
    cpuUsagePercent: 80    # CPU 使用率阈值（0 表示不关心）
    memUsagePercent: 80    # 内存使用率阈值
    diskUsagePercent: 70   # 磁盘使用率阈值

  # 要添加的污点
  taint:
    key: node.cpu.high
    value: "true"
    effect: NoSchedule

status:
  # 当前被该规则打上污点的节点列表
  affectedNodes:
    - node-1
    - node-2
  # 最近一次检测时间
  lastCheckTime: "2026-09-15T10:00:00Z"
```

### 3.3 设计思路

#### 为什么 `threshold` 的各项可以设为 0？

0 表示"不关心该指标"，只有 > 0 才参与判断。这样一条规则可以只关注特定资源：

| 场景 | cpuUsagePercent | memUsagePercent | diskUsagePercent |
|------|----------------|----------------|-----------------|
| 只关心 CPU | 80 | 0 | 0 |
| 只关心内存 | 0 | 80 | 0 |
| 只关心磁盘 | 0 | 0 | 70 |
| 同时关心 CPU 和内存 | 80 | 80 | 0 |

#### 为什么需要 `nodeSelector`？

实现差异化规则。同一个集群可以有多种规则：

```yaml
# 规则1：GPU 节点（磁盘敏感）
nodeSelector: { node-type: gpu }
threshold.diskUsagePercent: 70

# 规则2：普通 Worker（整体阈值高）
nodeSelector: { node-role: worker }
threshold.diskUsagePercent: 90
```

---

## 4. 定义 API 类型

### 4.1 修改 `api/v1/nodetaintrule_types.go`

```go
package v1

import (
    v1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
)

// ThresholdConfig 资源使用率阈值配置
// 任意一项超过阈值就触发打污点，0 表示不关心
type ThresholdConfig struct {
    DiskUsagePercent int `json:"diskUsagePercent"`
    CpuUsagePercent  int `json:"cpuUsagePercent"`
    MemUsagePercent  int `json:"memUsagePercent"`
}

// NodeTaintRuleSpec 定义期望状态
type NodeTaintRuleSpec struct {
    // 节点标签筛选，只对匹配标签的 node 生效
    // +optional
    NodeSelector map[string]string `json:"nodeSelector,omitempty"`

    // 阈值配置
    Threshold ThresholdConfig `json:"threshold"`

    // 需要添加的污点
    Taint v1.Taint `json:"taint"`
}

// NodeTaintRuleStatus 定义观测状态
type NodeTaintRuleStatus struct {
    // 记录已经被打上污点的节点列表
    // +optional
    AffectedNodes []string `json:"affectedNodes,omitempty"`

    // 最近一次检测时间
    // +optional
    LastCheckTime metav1.Time `json:"lastCheckTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NodeTaintRule 是 CRD 的主类型
type NodeTaintRule struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitzero"`

    Spec   NodeTaintRuleSpec   `json:"spec"`
    Status NodeTaintRuleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodeTaintRuleList 用于列出所有 NodeTaintRule
type NodeTaintRuleList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitzero"`
    Items           []NodeTaintRule `json:"items"`
}

func init() {
    SchemeBuilder.Register(func(s *runtime.Scheme) error {
        s.AddKnownTypes(SchemeGroupVersion, &NodeTaintRule{}, &NodeTaintRuleList{})
        return nil
    })
}
```

### 4.2 关键注解说明

| 注解 | 作用 |
|------|------|
| `+kubebuilder:object:root=true` | 生成 deepcopy 方法 |
| `+kubebuilder:subresource:status` | 启用 status 子资源，可以单独更新 status |
| `json:"..."` | JSON 序列化 tag，必须有 |
| `+optional` | 字段可选，生成 CRD 时标记为 nullable |
| `omitempty` | 空值时不序列化到 JSON |

### 4.3 重新生成代码

修改完类型后，运行以下命令生成深拷贝和其他辅助代码：

```bash
# 生成 DeepCopy 方法
make generate

# 或者手动运行
./bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
```

---

## 5. 业务逻辑 Reconcile

### 5.1 Reconcile 的触发时机

Controller-runtime 的 Reconcile 函数在以下情况被调用：

1. **CR 变更**：NodeTaintRule 被创建/修改/删除
2. **Node 变更**：集群中任意 Node 被创建/修改/删除
3. **定时触发**：通过返回 `RequeueAfter` 实现定期检查

### 5.2 完整 Reconcile 流程

```
Reconcile 被触发
  │
  ├─ 1. List 所有 NodeTaintRule CR
  │     命令: r.List(ctx, &ruleList)
  │
  ├─ 2. List 所有 Kubernetes Node
  │     命令: r.List(ctx, &nodeList)
  │
  ├─ 3. 外层：遍历每条规则
  │     for i := range ruleList.Items {
  │         ruleItem := &ruleList.Items[i]
  │         affectedNodes := []string{}
  │
  │         ├─ 4. 内层：遍历每个 Node
  │         │     for _, node := range nodeList.Items {
  │         │
  │         │     ├─ 5. NodeSelector 匹配
  │         │     │     isNodeMatched(&node, ruleItem.Spec.NodeSelector)
  │         │     │
  │         │     ├─ 6. 获取节点资源使用率
  │         │     │     getResourceUsageByType(ctx, &node, threshold)
  │         │     │
  │         │     ├─ 7. 对比阈值，判断是否需要打污点
  │         │     │     if usage.cpuPercent >= threshold.CpuUsagePercent
  │         │     │
  │         │     ├─ 8. 执行打污点或移除污点
  │         │     │     r.Update(ctx, node)  // 追加/删除 taint
  │         │     │
  │         │     └─ 9. 收集受影响的节点
  │         │           if needTaint { affectedNodes = append(...) }
  │         │     }
  │         │
  │         └─ 10. 更新规则的 Status
  │               ruleItem.Status.AffectedNodes = affectedNodes
  │               ruleItem.Status.LastCheckTime = metav1.Now()
  │               r.Status().Update(ctx, ruleItem)
  │     }
  │
  └─ 11. 返回结果
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
```

### 5.3 核心代码实现

#### 5.3.1 Reconcile 主函数

```go
func (r *NodeTaintRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    _ = logf.FromContext(ctx)

    // 1. 获取所有 NodeTaintRule CR
    var ruleList nodeopsv1.NodeTaintRuleList
    if err := r.List(ctx, &ruleList); err != nil {
        return ctrl.Result{}, fmt.Errorf("failed to list NodeTaintRule: %w", err)
    }

    // 2. 获取所有 Node
    var nodeList v1.NodeList
    if err := r.List(ctx, &nodeList); err != nil {
        return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
    }

    // 3. 遍历每条规则（外层循环）
    for i := range ruleList.Items {
        ruleItem := &ruleList.Items[i] // 用指针，后续更新 status
        affectedNodes := []string{}

        // 4. 遍历所有 Node，找匹配当前规则的
        for _, node := range nodeList.Items {
            // 判断 Node 是否匹配 spec.NodeSelector
            if !isNodeMatched(&node, ruleItem.Spec.NodeSelector) {
                continue
            }

            // 5. 判断并执行打污点/移除污点
            needTaint, err := r.checkAndApplyTaint(ctx, &node, ruleItem)
            if err != nil {
                logf.Log.Error(err, "check taint failed", "node", node.Name, "rule", ruleItem.Name)
                continue
            }

            if needTaint {
                affectedNodes = append(affectedNodes, node.Name)
            }
        }

        // 6. 更新规则的 Status
        ruleItem.Status.AffectedNodes = affectedNodes
        ruleItem.Status.LastCheckTime = metav1.Now()
        if err := r.Status().Update(ctx, ruleItem); err != nil {
            logf.Log.Error(err, "failed to update rule status", "rule", ruleItem.Name)
        }
    }

    // 7. 定时检查（30秒一次）
    return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}
```

#### 5.3.2 NodeSelector 匹配

```go
// 判断节点是否匹配 label selector
// selector 中的每一个 key-value，节点都必须有且值相同
func isNodeMatched(node *v1.Node, selector map[string]string) bool {
    // selector 为空表示匹配所有节点
    if len(selector) == 0 {
        return true
    }

    for k, v := range selector {
        if nodeValue, exists := node.Labels[k]; !exists || nodeValue != v {
            return false
        }
    }
    return true
}
```

#### 5.3.3 阈值判断与污点操作

```go
// checkAndApplyTaint 根据规则配置的阈值类型，获取对应资源使用率，判断是否需要打污点
func (r *NodeTaintRuleReconciler) checkAndApplyTaint(ctx context.Context, node *v1.Node, rule *nodeopsv1.NodeTaintRule) (bool, error) {
    threshold := rule.Spec.Threshold

    // 获取节点资源使用率
    usage, err := getResourceUsageByType(ctx, node, threshold)
    if err != nil {
        return false, err
    }

    // 判断是否超出阈值（只有 > 0 才说明关心这个指标）
    needTaint := false
    if threshold.CpuUsagePercent > 0 && usage.cpuPercent >= threshold.CpuUsagePercent {
        needTaint = true
    }
    if threshold.MemUsagePercent > 0 && usage.memPercent >= threshold.MemUsagePercent {
        needTaint = true
    }
    if threshold.DiskUsagePercent > 0 && usage.diskPercent >= threshold.DiskUsagePercent {
        needTaint = true
    }

    // 检查污点是否已存在
    hasTaint := hasTaint(node, rule.Spec.Taint)

    if needTaint && !hasTaint {
        // 需要打污点，且当前没有
        node.Spec.Taints = append(node.Spec.Taints, rule.Spec.Taint)
        if err := r.Update(ctx, node); err != nil {
            return false, fmt.Errorf("add taint failed: %w", err)
        }
        r.Recorder.Event(node, v1.EventTypeWarning, "ResourcePressure",
            fmt.Sprintf("node %s resource usage exceed threshold, add taint", node.Name))
    } else if !needTaint && hasTaint {
        // 不需要打污点，但当前有，移除
        node.Spec.Taints = removeTaint(node, rule.Spec.Taint)
        if err := r.Update(ctx, node); err != nil {
            return false, fmt.Errorf("remove taint failed: %w", err)
        }
        r.Recorder.Event(node, v1.EventTypeNormal, "ResourceRecover",
            fmt.Sprintf("node %s resource recovered, remove taint", node.Name))
    }

    return needTaint, nil
}
```

#### 5.3.4 获取节点资源使用率

```go
// resourceUsage 节点资源使用率
type resourceUsage struct {
    cpuPercent  int
    memPercent  int
    diskPercent int
}

// getResourceUsageByType 根据规则配置的阈值类型，获取对应资源的使用率
// TODO: 这里后续替换成真实查询 kubelet stats/summary API
func getResourceUsageByType(ctx context.Context, node *v1.Node, threshold nodeopsv1.ThresholdConfig) (*resourceUsage, error) {
    usage := &resourceUsage{}

    // TODO: 调用 kubelet API 获取真实数据
    // 目前是 demo 模拟数据
    usage.cpuPercent = 80
    usage.memPercent = 60
    usage.diskPercent = 75

    return usage, nil
}
```

#### 5.3.5 污点操作工具函数

```go
// 判断 node 是否包含目标污点
func hasTaint(node *v1.Node, targetTaint v1.Taint) bool {
    for _, t := range node.Spec.Taints {
        if t.Key == targetTaint.Key && t.Value == targetTaint.Value && t.Effect == targetTaint.Effect {
            return true
        }
    }
    return false
}

// 删除指定污点
func removeTaint(node *v1.Node, targetTaint v1.Taint) []v1.Taint {
    var newTaints []v1.Taint
    for _, t := range node.Spec.Taints {
        if !(t.Key == targetTaint.Key && t.Value == targetTaint.Value && t.Effect == targetTaint.Effect) {
            newTaints = append(newTaints, t)
        }
    }
    return newTaints
}
```

### 5.4 Watch Node 资源

Controller 默认只 Watch NodeTaintRule。如果需要在 Node 变更时也触发 Reconcile，需要在 `SetupWithManager` 中添加对 Node 的 Watch：

```go
func (r *NodeTaintRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&nodeopsv1.NodeTaintRule{}).
        // 添加对 Node 的 Watch
        Watches(
            &source.Kind{Type: &corev1.Node{}},
            &handler.EnqueueRequestForObject{},
        ).
        Named("nodetaintrule").
        Complete(r)
}
```

### 5.5 RBAC 权限

Controller 需要以下 RBAC 权限（通过 marker 声明）：

```go
// +kubebuilder:rbac:groups=nodeops.node.io,resources=nodetaintrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nodeops.node.io,resources=nodetaintrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nodeops.node.io,resources=nodetaintrules/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
```

运行 `make manifests` 会根据这些 marker 自动生成 RBAC 配置。

---

## 6. 生成 CRD YAML

### 6.1 运行 make manifests

```bash
make manifests
```

这会：
1. 调用 `controller-gen` 根据 `api/v1/nodetaintrule_types.go` 生成 CRD 定义
2. 更新 `config/crd/bases/nodeops.node.io_nodetaintrules.yaml`
3. 根据 RBAC marker 更新 `config/rbac/role.yaml`
4. 生成 Webhook 配置（如果定义了）

### 6.2 生成的 CRD 文件

`config/crd/bases/nodeops.node.io_nodetaintrules.yaml` 关键部分：

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    controller-gen.kubebuilder.io/version: v0.15.0
  name: nodetaintrules.nodeops.node.io
spec:
  group: nodeops.node.io
  names:
    kind: NodeTaintRule
    listKind: NodeTaintRuleList
    plural: nodetaintrules
    singular: nodetaintrule
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          spec:
            type: object
            properties:
              spec:
                type: object
                properties:
                  nodeSelector:
                    type: object
                    additionalProperties:
                      type: string
                  threshold:
                    type: object
                    properties:
                      cpuUsagePercent:
                        type: integer
                      diskUsagePercent:
                        type: integer
                      memUsagePercent:
                        type: integer
                  taint:
                    type: object
                    properties:
                      key:
                        type: string
                      value:
                        type: string
                      effect:
                        type: string
              status:
                type: object
                properties:
                  affectedNodes:
                    type: array
                    items:
                      type: string
                  lastCheckTime:
                    type: string
                    format: date-time
```

### 6.3 查看生成的 CRD

```bash
cat config/crd/bases/nodeops.node.io_nodetaintrules.yaml
```

---

## 7. 安装 CRD 到集群

### 7.1 确保集群可访问

```bash
# 检查集群连接
kubectl cluster-info

# 查看当前上下文
kubectl config current-context
```

### 7.2 安装 CRD

```bash
make install
```

这会：
1. 调用 `kustomize build config/crd` 生成合并后的 YAML
2. 调用 `kubectl apply` 将 CRD 安装到集群

### 7.3 验证 CRD 已安装

```bash
# 查看 CRD 是否存在
kubectl get crd | grep nodetaintrules

# 查看 CRD 详情
kubectl describe crd nodetaintrules.nodeops.node.io
```

### 7.4 卸载 CRD

```bash
make uninstall
```

---

## 8. 启动 Controller

### 8.1 本地快速启动（开发用）

```bash
make run
```

Controller 直接在终端前台运行，连接 `~/.kube/config` 中的集群。适合开发调试。

**停止**：在终端按 `Ctrl + C`

### 8.2 部署到集群

#### 方式一：使用 Kustomize 部署

```bash
# 1. 构建镜像
make docker-build IMG=your-registry/nodetaint-controller:latest

# 2. 推送镜像（如果需要）
make docker-push IMG=your-registry/nodetaint-controller:latest

# 3. 部署到集群
make deploy IMG=your-registry/nodetaint-controller:latest
```

#### 方式二：Kind 集群

```bash
# 1. 构建镜像
make docker-build IMG=kind.local/nodetaint-controller:latest

# 2. 加载镜像到 kind
kind load docker-image kind.local/nodetaint-controller:latest --name kind-cluster

# 3. 部署
make deploy IMG=kind.local/nodetaint-controller:latest
```

### 8.3 查看 Controller 日志

```bash
# 如果用 make run 启动，输出直接显示在终端

# 如果用 make deploy 部署
kubectl logs -n nodetaint-controller-system -l control-plane=controller-manager -f
```

### 8.4 停止部署的 Controller

```bash
make undeploy
```

---

## 9. 创建 CR 并验证

### 9.1 创建示例 CR

#### 方案一：只关心 CPU

```yaml
# config/samples/cpu-rule.yaml
apiVersion: nodeops.node.io/v1
kind: NodeTaintRule
metadata:
  name: cpu-high-rule
spec:
  nodeSelector:
    cce: test          # 匹配有该 label 的节点
  threshold:
    cpuUsagePercent: 70  # CPU 超 70% 就打污点
    memUsagePercent: 0
    diskUsagePercent: 0
  taint:
    key: node.cpu.high
    value: "true"
    effect: NoSchedule
```

#### 方案二：同时关心 CPU 和内存

```yaml
apiVersion: nodeops.node.io/v1
kind: NodeTaintRule
metadata:
  name: resource-high-rule
spec:
  nodeSelector:
    cce: test
  threshold:
    cpuUsagePercent: 80
    memUsagePercent: 80
    diskUsagePercent: 0
  taint:
    key: node.resource.high
    value: "true"
    effect: NoSchedule
```

### 9.2 给 Node 打标签

```bash
# 查看现有节点
kubectl get nodes

# 给节点打标签
kubectl label node <node-name> cce=test

# 验证标签
kubectl get node <node-name> --show-labels
```

### 9.3 应用 CR

```bash
kubectl apply -f config/samples/nodeops_v1_nodetaintrule.yaml
```

### 9.4 验证

#### 查看 CR 状态

```bash
kubectl get nodetaintrule nodetaintrule-sample -o yaml
```

预期输出（部分）：

```yaml
apiVersion: nodeops.node.io/v1
kind: NodeTaintRule
metadata:
  name: nodetaintrule-sample
spec:
  nodeSelector:
    cce: test
  threshold:
    cpuUsagePercent: 70
  taint:
    key: node.cpu.high
    effect: NoSchedule
status:
  affectedNodes:
    - your-node-name
  lastCheckTime: "2026-09-15T10:00:00Z"
```

#### 查看 Node 污点

```bash
# 查看所有污点
kubectl get node <node-name> -o jsonpath='{.spec.taints}'

# 格式化输出
kubectl describe node <node-name> | grep -A 10 "Taints"
```

预期输出：

```
Taints: node.cpu.high=true:NoSchedule
```

#### 查看 Controller 日志

```bash
kubectl logs -n nodetaint-controller-system -l control-plane=controller-manager -f
```

预期日志：

```
INFO controllers.NodeTaintRule checkAndApplyTaint node=your-node-name rule=nodetaintrule-sample
INFO node your-node-name resource usage exceed threshold, add taint
```

---

## 10. 常见问题

### 10.1 CRD 校验错误

**错误**：`spec.validation.openAPIV3Schema... Invalid value: "string": must be object if parent array's x-kubernetes-list-type is map`

**原因**：`+listType=map` 和 `+listMapKey=type` 只适用于对象数组，`AffectedNodes` 是 `[]string`。

**解决**：移除这两个 marker：

```go
// 错误
// +listType=map
// +listMapKey=type
AffectedNodes []string `json:"affectedNodes,omitempty"`

// 正确（直接删掉这两个注解）
AffectedNodes []string `json:"affectedNodes,omitempty"`
```

### 10.2 连接集群失败

**错误**：`error validating data: failed to download openapi... connection refused`

**原因**：`make install` 需要连接集群，但集群未运行或无法访问。

**解决**：

```bash
# 检查集群状态
kubectl cluster-info

# 如果用 kind
kind get clusters
kind create cluster  # 如果没有
```

### 10.3 Controller 启动报错 RBAC 权限不足

**错误**：`nodes is forbidden: User "system:serviceaccount..." cannot list`

**原因**：缺少 nodes 的 RBAC 权限。

**解决**：在 controller 文件添加 RBAC marker：

```go
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
```

然后重新生成：

```bash
make manifests
make install  # 或 make deploy
```

### 10.4 污点未生效

**原因**：`getResourceUsageByType` 返回的是硬编码的模拟值。

**解决**：修改函数实现，调用真实的 kubelet API：

```go
func getResourceUsageByType(...) (*resourceUsage, error) {
    // 调用 kubelet stats/summary API
    // GET https://<node-ip>:10250/stats/summary
    // 解析返回的 JSON 获取真实使用率
}
```

### 10.5 节点未匹配规则

**排查**：

```bash
# 查看节点标签
kubectl get node <node-name> --show-labels

# 查看 CR 的 nodeSelector
kubectl get nodetaintrule <name> -o jsonpath='{.spec.nodeSelector}'

# 手动验证匹配逻辑
kubectl get nodes -l 'cce=test'  # 如果 nodeSelector 是 {cce: test}
```

### 10.6 Status 未更新

**排查**：

1. 确认 `+kubebuilder:subresource:status` 注解存在
2. 确认使用 `r.Status().Update(ctx, ruleItem)` 而不是 `r.Update(ctx, ruleItem)`
3. 查看 controller 日志是否有错误

---

## 附录：Makefile 常用命令

| 命令 | 作用 |
|------|------|
| `make help` | 查看所有可用命令 |
| `make manifests` | 生成/更新 CRD YAML 和 RBAC 配置 |
| `make generate` | 生成深拷贝代码 |
| `make install` | 将 CRD 安装到集群 |
| `make uninstall` | 从集群卸载 CRD |
| `make deploy` | 将 Controller 部署到集群 |
| `make undeploy` | 从集群删除 Controller |
| `make run` | 本地运行 Controller（开发用）|
| `make docker-build` | 构建 Docker 镜像 |
| `make docker-push` | 推送镜像到仓库 |
| `make test` | 运行单元测试 |
| `make test-e2e` | 运行端到端测试 |

---

*文档版本：v1.0.0*
*最后更新：2026-09-15*
