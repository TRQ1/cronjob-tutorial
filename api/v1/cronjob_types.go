/*
Copyright 2022.

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
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CronJobSpec defines the desired state of CronJob
type CronJobSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Cron 포멧을 사용하는 Schedule 입니다. reference: https://en.wikipedia.org/wiki/Cron.
	Schedule string `json:"schedule"`

	// 동시 실행을 처리하는 방법을 지정 합니다.
	// 아래와 같은 방법이 있습니다.
	// - "Allow" (default): CronJobs가 동시에 실행되도록 허용 합니다.
	// - "Forbid": 동시 실행을 금지 하고, 이전 실행이 아직 완료 되지 않는 경우는 다음 실행을 skip 합니다.
	// - "Replace": 현재 실행중인 작업을 취소하고 새 작업으로 교체 합니다.
	// +optional
	ConcurrencyPolicy ConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`

	// 스케쥴일 누락 되면 초단위로 작업을 시작하는 optional deadline 입니다.
	// 어떤 이유로든 누락된 스케쥴 실행은 실패한 것으로 계산 합니다.
	// +optional
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`

	// 이 플러그는 컨트롤러에 후속실행을 중단하도록 합니다.
	// 기본값은 false이고, 이미 시작된 실행에는 적용 하지 않습니다.
	// +optional
	Suspend *bool `json:"suspend,omitempty"`

	// 유지하기위 위해 성공적으로 완료된 job의 수 입니다.
	// 0과 지정되지 않는 값을 구별하기 위한 포인터
	// +optional
	SuccessfulJobsHistoryLimit *int32 `json:"successfulJobsHistoryLimit,omitempty"`

	// 유지하기위 위해 실패가 완료된 job의 수 입니다.
	// 0과 지정되지 않는 값을 구별하기 위한 포인터
	// +optional
	FailedJobsHistoryLimit *int32 `json:"failedJobsHistoryLimit,omitempty"`

	JobTemplate batchv1beta1.JobTemplateSpec `json:"jobTemplate"`
}

// ConcurrencyPolicy 작업이 처리되는 방식을 설명 합니다.
// 다음 동시 정책중의 하나만 지정 할 수 있습니다.
// 다음 정책이 지정되지 않은경우 기본값은 AllowConcurrent.
// +kubebuilder:validation:Enum=Allow;Forbid;Replace
type ConcurrencyPolicy string

const (
	// AllowConcurrent CronJobs가 동시에 실행되도록 허용 합니다.
	AllowConcurrent ConcurrencyPolicy = "Allow"

	// ForbidConcurrent 동시 실행을 금지 하고, 이전 실행이 아직 완료 되지 않는 경우는 다음 실행을 skip 합니다.
	// hasn't finished yet.
	ForbidConcurrent ConcurrencyPolicy = "Forbid"

	// ReplaceConcurrent 현재 실행중인 작업을 취소하고 새 작업으로 교체 합니다.
	ReplaceConcurrent ConcurrencyPolicy = "Replace"
)

// CronJobStatus defines the observed state of CronJob
type CronJobStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// 최근 실행중인 작업에 대한 포인터 리스트
	// +optional
	Active []corev1.ObjectReference `json:"active,omitempty"`

	// +optional
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// CronJob is the Schema for the cronjobs API
type CronJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CronJobSpec   `json:"spec,omitempty"`
	Status CronJobStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CronJobList contains a list of CronJob
type CronJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CronJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CronJob{}, &CronJobList{})
}
