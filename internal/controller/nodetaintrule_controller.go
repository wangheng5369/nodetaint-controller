/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	nodeopsv1 "github.com/wangheng5369/nodetaint-controller/api/v1"
)

// NodeTaintRuleReconciler reconciles a NodeTaintRule object
type NodeTaintRuleReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=nodeops.node.io,resources=nodetaintrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nodeops.node.io,resources=nodetaintrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nodeops.node.io,resources=nodetaintrules/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NodeTaintRule object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/reconcile
func (r *NodeTaintRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// 1. 获取所有 NodeTaintRule CR
	var ruleList nodeopsv1.NodeTaintRuleList
	if err := r.List(ctx, &ruleList); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list NodeTaintRule: %w", err)
	}
	// 2. 获取所有 node
	var nodeList v1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	// 3. 遍历每条规则（外层循环）
	for i := range ruleList.Items {
		ruleItem := &ruleList.Items[i] // 用指针，后续更新 status
		affectedNodes := []string{}    // 记录被该规则影响（需要打污点）的节点

		// 4. 遍历所有 node，找匹配当前规则的
		for _, node := range nodeList.Items {
			// 判断 node 是否匹配 spec.NodeSelector
			if !isNodeMatched(&node, ruleItem.Spec.NodeSelector) {
				continue
			}

			// 根据规则关心的指标类型，决定获取哪个资源使用率，并判断是否需要打污点
			needTaint, err := r.checkAndApplyTaint(ctx, &node, ruleItem)
			if err != nil {
				logf.Log.Error(err, "check taint failed", "node", node.Name, "rule", ruleItem.Name)
				continue
			}

			if needTaint {
				affectedNodes = append(affectedNodes, node.Name)
			}
		}

		// 5. 更新该规则的 status
		ruleItem.Status.AffectedNodes = affectedNodes
		ruleItem.Status.LastCheckTime = metav1.Now()
		if err := r.Status().Update(ctx, ruleItem); err != nil {
			logf.Log.Error(err, "failed to update rule status", "rule", ruleItem.Name)
		}
	}

	return ctrl.Result{}, nil
}

// 工具函数：判断node是否包含目标taint
func hasTaint(node *v1.Node, targetTaint v1.Taint) bool {
	for _, t := range node.Spec.Taints {
		if t.Key == targetTaint.Key && t.Value == targetTaint.Value && t.Effect == targetTaint.Effect {
			return true
		}
	}
	return false
}

// 工具函数：删除指定taint
func removeTaint(node *v1.Node, targetTaint v1.Taint) []v1.Taint {
	var newTaints []v1.Taint
	for _, t := range node.Spec.Taints {
		if !(t.Key == targetTaint.Key && t.Value == targetTaint.Value && t.Effect == targetTaint.Effect) {
			newTaints = append(newTaints, t)
		}
	}
	return newTaints
}

// 工具函数：判断节点是否匹配 label selector
// selector 中的每一个 key-value，节点都必须有且值相同
func isNodeMatched(node *v1.Node, selector map[string]string) bool {
	for k, v := range selector {
		if nodeValue, exists := node.Labels[k]; !exists || nodeValue != v {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeTaintRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// 主资源：监听我们自定义CR NodeTaintRule，CR增删改触发Reconcile
		For(&nodeopsv1.NodeTaintRule{}).
		// 额外监听：Node资源，Node发生变化（资源使用率、标签变更）触发Reconcile
		Watches(&v1.Node{}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

// checkAndApplyTaint 根据规则配置的阈值类型，获取对应资源使用率，判断是否需要打污点
// 返回 needTaint 表示是否需要打污点
func (r *NodeTaintRuleReconciler) checkAndApplyTaint(ctx context.Context, node *v1.Node, rule *nodeopsv1.NodeTaintRule) (bool, error) {
	threshold := rule.Spec.Threshold
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
		r.Recorder.Event(node, v1.EventTypeWarning, "ResourcePressure", fmt.Sprintf("node %s resource usage exceed threshold, add taint", node.Name))
	} else if !needTaint && hasTaint {
		// 不需要打污点，但当前有，移除
		node.Spec.Taints = removeTaint(node, rule.Spec.Taint)
		if err := r.Update(ctx, node); err != nil {
			return false, fmt.Errorf("remove taint failed: %w", err)
		}
		r.Recorder.Event(node, v1.EventTypeNormal, "ResourceRecover", fmt.Sprintf("node %s resource recovered, remove taint", node.Name))
	}

	return needTaint, nil
}

// resourceUsage 节点资源使用率
type resourceUsage struct {
	cpuPercent  int
	memPercent  int
	diskPercent int
}

// getResourceUsageByType 根据规则配置的阈值类型，获取对应资源的使用率
// 只获取规则关心的指标，不关心的一律返回0
func getResourceUsageByType(ctx context.Context, node *v1.Node, threshold nodeopsv1.ThresholdConfig) (*resourceUsage, error) {
	usage := &resourceUsage{}

	// TODO: 这里后续替换成真实查询 kubelet stats/summary API
	// 目前是 demo 模拟数据
	usage.cpuPercent = 80
	usage.memPercent = 60
	usage.diskPercent = 75

	return usage, nil
}
