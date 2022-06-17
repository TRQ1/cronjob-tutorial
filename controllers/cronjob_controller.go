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

package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/robfig/cron"
	kbatch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ref "k8s.io/client-go/tools/reference"
	batchv1 "trq1.crontab/cronjob/api/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type realClock struct{}

// clock mock 하기 위해 time.Now를 호출하여 실제 시간으로 테스트 한다.
func (_ realClock) Now() time.Time { return time.Now() }

type Clock interface {
	Now() time.Time
}

// CronJobReconciler reconciles a CronJob object
type CronJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Clock
}

// sechdule 시간에 대한 Annotation
var (
	scheduledTimeAnnotation = "batch.tutorial.kubebuilder.io/scheduled-at"
)

// RBAC markers 정의
//+kubebuilder:rbac:groups=batch.trq1.cronjob,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch.trq1.cronjob,resources=cronjobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=batch.trq1.cronjob,resources=cronjobs/finalizers,verbs=update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CronJob object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *CronJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	var cronJob batchv1.CronJob

	if err := r.Get(ctx, req.NamespacedName, &cronJob); err != nil {
		l.Error(err, "unable to fetch CronJob")
		// 즉시 수정 불가능 하기때문에, 발견되지 않은 에러를 무시합니다.
		// 삭제 요청에 대해서 requeue(새로운 알람을 기달려야 합니다.)하고, 얻을 수 있습니다
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var childJobs kbatch.JobList
	// 해당 Cronjob이 도는 namespace에서 모든 child jobs의 상태를 업데이트 해야합니다.
	// List method를 사용하여 child jobs를 확인 하며, namespace 에서 jobOnwerKey 매칭 필드로 확인 합니다.
	if err := r.List(ctx, &childJobs, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name}); err != nil {
		l.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}

	// 활성화된 job의 목록을 찾는 변수
	var activeJobs []*kbatch.Job
	var successfulJobs []*kbatch.Job
	var failedJobs []*kbatch.Job
	var mostRecentTime *time.Time // find the last run so we can update the status

	// 완료/실패 조건이 True가 되는 경우 작업을 완료 로 간주하며,
	// 상태 조건을 사용하면, 확장 가능한 상태 정보를 객체에 추가 가
	isJobFinished := func(job *kbatch.Job) (bool, kbatch.JobConditionType) {
		for _, c := range job.Status.Conditions {
			if (c.Type == kbatch.JobComplete || c.Type == kbatch.JobFailed) && c.Status == corev1.ConditionTrue {
				return true, c.Type
			}
		}

		return false, ""
	}
	// +kubebuilder:docs-gen:collapse=isJobFinished

	// 작업 생성중에 추가된 annotation에서 예약된 시간을 축출 합니다.
	getScheduledTimeForJob := func(job *kbatch.Job) (*time.Time, error) {
		timeRaw := job.Annotations[scheduledTimeAnnotation]
		if len(timeRaw) == 0 {
			return nil, nil
		}

		timeParsed, err := time.Parse(time.RFC3339, timeRaw)
		if err != nil {
			return nil, err
		}
		return &timeParsed, nil
	}

	// +kubebuilder:docs-gen:collapse=getScheduledTimeForJob
	for i, job := range childJobs.Items {
		_, finishedType := isJobFinished(&job)
		switch finishedType {
		case "": // ongoing
			activeJobs = append(activeJobs, &childJobs.Items[i])
		case kbatch.JobFailed:
			failedJobs = append(failedJobs, &childJobs.Items[i])
		case kbatch.JobComplete:
			successfulJobs = append(successfulJobs, &childJobs.Items[i])
		}

		// We'll store the launch time in an annotation, so we'll reconstitute that from
		// the active jobs themselves.
		scheduledTimeForJob, err := getScheduledTimeForJob(&job)
		if err != nil {
			l.Error(err, "unable to parse schedule time for child job", "job", &job)
			continue
		}
		if scheduledTimeForJob != nil {
			if mostRecentTime == nil {
				mostRecentTime = scheduledTimeForJob
			} else if mostRecentTime.Before(*scheduledTimeForJob) {
				mostRecentTime = scheduledTimeForJob
			}
		}
	}

	if mostRecentTime != nil {
		cronJob.Status.LastScheduleTime = &metav1.Time{Time: *mostRecentTime}
	} else {
		cronJob.Status.LastScheduleTime = nil
	}
	cronJob.Status.Active = nil
	for _, activeJob := range activeJobs {
		jobRef, err := ref.GetReference(r.Scheme, activeJob)
		if err != nil {
			l.Error(err, "unable to make reference to active job", "job", activeJob)
			continue
		}
		cronJob.Status.Active = append(cronJob.Status.Active, *jobRef)
	}

	l.V(1).Info("job count", "active jobs", len(activeJobs), "successful jobs", len(successfulJobs), "failed jobs", len(failedJobs))

	// 수집된 데이터를 사용하여 CRD 상태를 업데이트 합니다.
	if err := r.Status().Update(ctx, &cronJob); err != nil {
		l.Error(err, "unable to update CronJob status")
		return ctrl.Result{}, err
	}

	// History Limit에 따른 오래된 jobs을 삭제
	// NB: 특정 항목에서 실패하면, 실패한 항목을 지우는게 최선의 노력입니다.
	// 삭제를 완료하기 위해 대기열에 추가하지 않음
	if cronJob.Spec.FailedJobsHistoryLimit != nil {
		sort.Slice(failedJobs, func(i, j int) bool {
			if failedJobs[i].Status.StartTime == nil {
				return failedJobs[j].Status.StartTime != nil
			}
			return failedJobs[i].Status.StartTime.Before(failedJobs[j].Status.StartTime)
		})
		for i, job := range failedJobs {
			if int32(i) >= int32(len(failedJobs))-*cronJob.Spec.FailedJobsHistoryLimit {
				break
			}
			if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
				l.Error(err, "unable to delete old failed job", "job", job)
			} else {
				l.V(0).Info("deleted old failed job", "job", job)
			}
		}
	}

	if cronJob.Spec.SuccessfulJobsHistoryLimit != nil {
		sort.Slice(successfulJobs, func(i, j int) bool {
			if successfulJobs[i].Status.StartTime == nil {
				return successfulJobs[j].Status.StartTime != nil
			}
			return successfulJobs[i].Status.StartTime.Before(successfulJobs[j].Status.StartTime)
		})
		for i, job := range successfulJobs {
			if int32(i) >= int32(len(successfulJobs))-*cronJob.Spec.SuccessfulJobsHistoryLimit {
				break
			}
			if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); (err) != nil {
				l.Error(err, "unable to delete old successful job", "job", job)
			} else {
				l.V(0).Info("deleted old successful job", "job", job)
			}
		}
	}

	/* 정지되어있는지 확인

	해당 객체가 일시 중지 된 경우, 작업을 실행 하고 싶지 않기 때문에 바로 중지 해야함
	이유는 현재 실행중인 작업에 문제가 있고 조사하기위해 pause를 실행한다.
	pause는 객체를 삭제 하지 않는다.
	*/
	if cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend {
		l.V(1).Info("cronjob suspended, skipping")
		return ctrl.Result{}, nil
	}

	// 다음 예약된 스케쥴을 가져오기
	// 아직 처리 하지 않은 스케쥴이나 다음 예약된 스케쥴을 실행 하기 위해 예약된 스케쥴을 실행을 계산합니다.
	// cron 라이브러리를 사용 하여 예정된 시간을 계산 합니다.
	// 마지막 실행에서 적절한 시간을 계산 하거나 마지막 실행을 찾을 수 없는 경우 Cronjob을 실행합니다.
	getNextSchedule := func(cronJob *batchv1.CronJob, now time.Time) (lastMissed time.Time, next time.Time, err error) {
		sched, err := cron.ParseStandard(cronJob.Spec.Schedule)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("Unparseable schedule %q: %v", cronJob.Spec.Schedule, err)
		}

		// 최적화를 위해 마지막으로 관찰한 런타임 부터 실행합니다. -> 약간의 치트?
		// 여기에서 재구성이 가능하지만 방금 업데이트 하였기때문에 의미 없습니다.
		var earliestTime time.Time
		if cronJob.Status.LastScheduleTime != nil {
			earliestTime = cronJob.Status.LastScheduleTime.Time
		} else {
			earliestTime = cronJob.ObjectMeta.CreationTimestamp.Time
		}
		if cronJob.Spec.StartingDeadlineSeconds != nil {
			// Controller는 이시점 이후 부터는 일정을 계획하지 않습니다.
			schedulingDeadline := now.Add(-time.Second * time.Duration(*cronJob.Spec.StartingDeadlineSeconds))

			if schedulingDeadline.After(earliestTime) {
				earliestTime = schedulingDeadline
			}
		}
		if earliestTime.After(now) {
			return time.Time{}, sched.Next(now), nil
		}

		starts := 0
		for t := sched.Next(earliestTime); !t.After(now); t = sched.Next(t) {
			lastMissed = t
			/*
				객채는 여러 시작을 놓칠수 있습니다.
				ex) 컨트롤러가 금요일 오후 5시 1분에 모두가 집에 들어가고,
					누군가가 화요일 오전에 와서 문제를 발견하고 컨트롤러를 다시 시작 한다면,
					한 시간당 80개 이상의 작업이 예정된 작업 같은 모든 시간별 작업을 추가
					개입 없이 실행 할 수 있어야 합니다.

				하지만 버그가 있을수도 있고 컨트롤러의 서버 또는 API 서버의 시간이 잘못
				설정되어 있는 경우는 시작 시간을 놓칠 수 있고 놓친 스케쥴이 많은 경우에는
				모든 CPU를 소모 할 수 있습니다.

				해당 로직은 놓친 시작 시간을 모두 나열 하지 않을려고합니다. 최소 100개 미만으로
			*/
			starts++
			if starts > 100 {
				// 가장 최근 시간을 가져올수 없으므로 나머지 빈 조각은 반
				return time.Time{}, time.Time{}, fmt.Errorf("Too many missed start times (> 100). Set or decrease .spec.startingDeadlineSeconds or check clock skew.")
			}
		}
		return lastMissed, sched.Next(now), nil
	}
	// +kubebuilder:docs-gen:collapse=getNextSchedule

	// 다음 작업을 생성해야 할 시간을 파악합니다. -> 아니면 놓친 스케쥴 등
	missedRun, nextRun, err := getNextSchedule(&cronJob, r.Now())
	if err != nil {
		l.Error(err, "unable to figure out CronJob schedule")

		// 일정을 수정하는 작업을 받을때 까지 requeuing에 대해 신경 쓰지 않으므로 오류를 반환 하지 않습니다.
		return ctrl.Result{}, nil
	}

	/*
		실제로 실행해야하는 경우에 다음 작업까지 대기열에 추가하라는 최종 요청을 준비 합니다.
	*/
	scheduledResult := ctrl.Result{RequeueAfter: nextRun.Sub(r.Now())} // save this so we can re-use it elsewhere
	l = l.WithValues("now", r.Now(), "next run", nextRun)

	if missedRun.IsZero() {
		l.V(1).Info("no upcoming scheduled times, sleeping until next")
		return scheduledResult, nil
	}

	// 실행 시작하기에 늦었는지 확인
	l = l.WithValues("current run", missedRun)
	tooLate := false
	if cronJob.Spec.StartingDeadlineSeconds != nil {
		tooLate = missedRun.Add(time.Duration(*cronJob.Spec.StartingDeadlineSeconds) * time.Second).Before(r.Now())
	}
	if tooLate {
		l.V(1).Info("missed starting deadline for last run, sleeping till next")
		// TODO(directxman12): events
		return scheduledResult, nil
	}

	// 동시성 정책으로 인해 여러 작업을 동시에 실행 하는 것을 금지
	if cronJob.Spec.ConcurrencyPolicy == batchv1.ForbidConcurrent && len(activeJobs) > 0 {
		l.V(1).Info("concurrency policy blocks concurrent runs, skipping", "num active", len(activeJobs))
		return scheduledResult, nil
	}

	// 아니면 기존것을 교체 하도록 지시
	if cronJob.Spec.ConcurrencyPolicy == batchv1.ReplaceConcurrent {
		for _, activeJob := range activeJobs {
			// job이 이미 지워져있다면 신경 쓰지 않음
			if err := r.Delete(ctx, activeJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
				l.Error(err, "unable to delete active job", "job", activeJob)
				return ctrl.Result{}, err
			}
		}
	}

	/*
		Cronjods job 템플릿 기반으로 작업을 구성하며 해당 템플에 사양을 복사하고 일부 기본 객체 메타를 복사합니다.
		-> 즉 job 스펙에 대한 정의서 입니다.

		LastScheduleTime 필드가 각각 조정 할 수 있도록 scheduled time annotation을 설정 합니다.

		Owner reference를 설정이 필요하며 이를 통해서 Kubernetes garbage collector가 cronjob을 정리 할 수 있으며
		컨트롤러 런타임에서 작업 변경(삭제, 추가, 완료등) 조정이 되는 경우 cronjob에서 파악 할수 있습니다.
	*/
	constructJobForCronJob := func(cronJob *batchv1.CronJob, scheduledTime time.Time) (*kbatch.Job, error) {
		// 동일한 작업이 두번 생성된 것을 방지하기 위해 주어진 시작시간의 결정된 job 이름을 가지도록 한다.
		name := fmt.Sprintf("%s-%d", cronJob.Name, scheduledTime.Unix())

		job := &kbatch.Job{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
				Name:        name,
				Namespace:   cronJob.Namespace,
			},
			Spec: *cronJob.Spec.JobTemplate.Spec.DeepCopy(),
		}
		for k, v := range cronJob.Spec.JobTemplate.Annotations {
			job.Annotations[k] = v
		}
		job.Annotations[scheduledTimeAnnotation] = scheduledTime.Format(time.RFC3339)
		for k, v := range cronJob.Spec.JobTemplate.Labels {
			job.Labels[k] = v
		}
		if err := ctrl.SetControllerReference(cronJob, job, r.Scheme); err != nil {
			return nil, err
		}

		return job, nil
	}
	// +kubebuilder:docs-gen:collapse=constructJobForCronJob

	// 실제로 job을 생성
	job, err := constructJobForCronJob(&cronJob, missedRun)
	if err != nil {
		l.Error(err, "unable to construct job from template")
		// don't bother requeuing until we get a change to the spec
		return scheduledResult, nil
	}

	// 클러스터 잡을 생성
	if err := r.Create(ctx, job); err != nil {
		l.Error(err, "unable to create Job for CronJob", "job", job)
		return ctrl.Result{}, err
	}

	l.V(1).Info("created Job for CronJob run", "job", job)

	return scheduledResult, nil
}

var (
	jobOwnerKey = ".metadata.controller"
	apiGVStr    = batchv1.GroupVersion.String()
)

// SetupWithManager sets up the controller with the Manager.
func (r *CronJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// 실제 시간을 설정
	if r.Clock == nil {
		r.Clock = realClock{}
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kbatch.Job{}, jobOwnerKey, func(rawObj client.Object) []string {
		// 작업 객체을 확인하고, 소유자를 축출
		job := rawObj.(*kbatch.Job)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// CronJob
		if owner.APIVersion != apiGVStr || owner.Kind != "CronJob" {
			return nil
		}

		// owner name을 반환
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.CronJob{}).
		Owns(&kbatch.Job{}).
		Complete(r)
}
