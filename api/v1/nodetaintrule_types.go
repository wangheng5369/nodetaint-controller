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

package v1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type ThresholdConfig struct {
	// 磁盘使用率阈值百分比，超过就打污点
	DiskUsagePercent int `json:"diskUsagePercent"`
	// CPU使用率阈值百分比
	CpuUsagePercent int `json:"cpuUsagePercent"`
	// 内存使用率阈值百分比
	MemUsagePercent int `json:"memUsagePercent"`
}

// NodeTaintRuleSpec defines the desired state of NodeTaintRule
type NodeTaintRuleSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// 节点标签筛选，只对匹配标签的node生效
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// 阈值配置
	Threshold ThresholdConfig `json:"threshold"`
	// 需要添加的污点
	Taint v1.Taint `json:"taint"`
}

// NodeTaintRuleStatus defines the observed state of NodeTaintRule.
type NodeTaintRuleStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the NodeTaintRule resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +optional
	// 记录已经被打上污点的节点列表
	AffectedNodes []string `json:"affectedNodes,omitempty"`
	// 最近一次检测时间
	LastCheckTime metav1.Time `json:"lastCheckTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NodeTaintRule is the Schema for the nodetaintrules API
type NodeTaintRule struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NodeTaintRule
	// +required
	Spec NodeTaintRuleSpec `json:"spec"`

	// status defines the observed state of NodeTaintRule
	// +optional
	Status NodeTaintRuleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodeTaintRuleList contains a list of NodeTaintRule
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
