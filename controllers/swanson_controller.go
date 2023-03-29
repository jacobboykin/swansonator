/*
Copyright 2023.

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

// TODO: https://medium.com/@gallettilance/10-things-you-should-know-before-writing-a-kubernetes-controller-83de8f86d659

package controllers

import (
	"context"
	"fmt"
	"golang.org/x/exp/slices"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	parksv1alpha1 "github.com/jacobboykin/swansoner/api/v1alpha1"
)

const (
	// typeAvailableSwanson represents the status of the Deployment reconciliation
	typeAvailableSwanson = "Available"
)

// SwansonReconciler reconciles a Swanson object
type SwansonReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=parks.jacobboykin.com,resources=swansons,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=parks.jacobboykin.com,resources=swansons/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=parks.jacobboykin.com,resources=swansons/finalizers,verbs=update

//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *SwansonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling object", "Name", req.NamespacedName)

	// Fetch the Swanson instance
	swanson := &parksv1alpha1.Swanson{}
	err := r.Get(ctx, req.NamespacedName, swanson)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, it was likely deleted
			logger.Info("Swanson resource not found. Ignoring, as the object was likely deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object; requeue the request
		logger.Error(err, "Failed to get swanson")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status are available
	if swanson.Status.Conditions == nil || len(swanson.Status.Conditions) == 0 {
		meta.SetStatusCondition(&swanson.Status.Conditions,
			metav1.Condition{
				Type:    typeAvailableSwanson,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation"},
		)
		if updateErr := r.Status().Update(ctx, swanson); updateErr != nil {
			logger.Error(updateErr, "Failed to update Swanson status. Requeued")
			return ctrl.Result{RequeueAfter: time.Second * 3}, updateErr
		}

		// Let's re-fetch the Swanson Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if getErr := r.Get(ctx, req.NamespacedName, swanson); getErr != nil {
			logger.Error(getErr, "Failed to re-fetch swanson")
			return ctrl.Result{}, getErr
		}
	}

	// Check if the Swanson instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isSwansonMarkedToBeDeleted := swanson.GetDeletionTimestamp() != nil
	if isSwansonMarkedToBeDeleted {
		r.Recorder.Event(swanson, "Warning", "Deleting",
			fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s",
				swanson.Name,
				swanson.Namespace))
		return ctrl.Result{}, nil
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: swanson.Name, Namespace: swanson.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		dep, err := r.deploymentForSwanson(swanson)
		if err != nil {
			logger.Error(err, "Failed to define new Deployment resource for Swanson")

			// The following implementation will update the status
			meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", swanson.Name, err)})

			if err := r.Status().Update(ctx, swanson); err != nil {
				logger.Error(err, "Failed to update Swanson status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		logger.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			logger.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	} else if err != nil {
		logger.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// Check if the service already exists, if not create a new one
	foundService := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: swanson.Name, Namespace: swanson.Namespace}, foundService)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		svc, err := r.serviceForSwanson(swanson)
		if err != nil {
			logger.Error(err, "Failed to define new Service resource for Swanson")

			// The following implementation will update the status
			meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Service for the custom resource (%s): (%s)", swanson.Name, err)})

			if err := r.Status().Update(ctx, swanson); err != nil {
				logger.Error(err, "Failed to update Swanson status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		logger.Info("Creating a new Service",
			"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		if err = r.Create(ctx, svc); err != nil {
			logger.Error(err, "Failed to create new Service",
				"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	} else if err != nil {
		logger.Error(err, "Failed to get Service")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	kind := swanson.Spec.Kind
	containerIdx := slices.IndexFunc(found.Spec.Template.Spec.Containers,
		func(c corev1.Container) bool { return c.Name == "swanson" },
	)
	envVarIdx := slices.IndexFunc(found.Spec.Template.Spec.Containers[containerIdx].Env,
		func(e corev1.EnvVar) bool { return e.Name == "SWANSON_KIND" },
	)
	if found.Spec.Template.Spec.Containers[containerIdx].Env[envVarIdx].Value != kind {
		found.Spec.Template.Spec.Containers[containerIdx].Env[envVarIdx].Value = kind
		if err = r.Update(ctx, found); err != nil {
			logger.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the Swanson Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, swanson); err != nil {
				logger.Error(err, "Failed to re-fetch Swanson")
				return ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
				Status: metav1.ConditionFalse, Reason: "Updating Kind",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", swanson.Name, err)})

			if err := r.Status().Update(ctx, swanson); err != nil {
				logger.Error(err, "Failed to update Swanson status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{Requeue: true}, nil
	}

	// The CRD API is defining that the Swanson type, have a SwansonSpec.Size field
	// to set the quantity of Deployment instances is the desired state on the cluster.
	// Therefore, the following code will ensure the Deployment size is the same as defined
	// via the Size spec of the Custom Resource which we are reconciling.
	size := swanson.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		if err = r.Update(ctx, found); err != nil {
			logger.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the Swanson Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, swanson); err != nil {
				logger.Error(err, "Failed to re-fetch Swanson")
				return ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
				Status: metav1.ConditionFalse, Reason: "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", swanson.Name, err)})

			if err := r.Status().Update(ctx, swanson); err != nil {
				logger.Error(err, "Failed to update Swanson status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{Requeue: true}, nil
	}

	// The following implementation will update the status
	meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Deployment for custom resource (%s) with %d replicas created successfully", swanson.Name, size)})

	if err := r.Status().Update(ctx, swanson); err != nil {
		logger.Error(err, "Failed to update Swanson status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *SwansonReconciler) serviceForSwanson(swanson *parksv1alpha1.Swanson) (*corev1.Service, error) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      swanson.Name,
			Namespace: swanson.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
					TargetPort: intstr.IntOrString{
						Type:   0,
						IntVal: 8080,
						StrVal: "",
					},
				},
			},
			Selector: labelsForSwanson(swanson.Name),
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(swanson, svc, r.Scheme); err != nil {
		return nil, err
	}

	return svc, nil
}

// deploymentForSwanson returns a Swanson Deployment object
func (r *SwansonReconciler) deploymentForSwanson(swanson *parksv1alpha1.Swanson) (*appsv1.Deployment, error) {
	ls := labelsForSwanson(swanson.Name)
	replicas := swanson.Spec.Size

	// Get the Operand image
	image, err := imageForSwanson()
	if err != nil {
		return nil, err
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      swanson.Name,
			Namespace: swanson.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					//Affinity: &corev1.Affinity{
					//	NodeAffinity: &corev1.NodeAffinity{
					//		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				{
					//					MatchExpressions: []corev1.NodeSelectorRequirement{
					//						{
					//							Key:      "kubernetes.io/arch",
					//							Operator: "In",
					//							Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						},
					//						{
					//							Key:      "kubernetes.io/os",
					//							Operator: "In",
					//							Values:   []string{"linux"},
					//						},
					//					},
					//				},
					//			},
					//		},
					//	},
					//},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "swanson",
						ImagePullPolicy: corev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &corev1.SecurityContext{
							// WARNING: Ensure that the image used defines an UserID in the Dockerfile
							// otherwise the Pod will not run and will fail with "container has runAsNonRoot and image has non-numeric user"".
							// If you want your workloads admitted in namespaces enforced with the restricted mode in OpenShift/OKD vendors
							// then, you MUST ensure that the Dockerfile defines a User ID OR you MUST leave the "RunAsNonRoot" and
							// "RunAsUser" fields empty.
							RunAsNonRoot: &[]bool{true}[0],
							// The memcached image does not use a non-zero numeric user as the default user.
							// Due to RunAsNonRoot field being set to true, we need to force the user in the
							// container to a non-zero numeric user. We do this using the RunAsUser field.
							// However, if you are looking to provide solution for K8s vendors like OpenShift
							// be aware that you cannot run under its restricted-v2 SCC if you set this value.
							RunAsUser:                &[]int64{1001}[0],
							AllowPrivilegeEscalation: &[]bool{false}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8080,
							Name:          "swanson",
						}},
						Env: []corev1.EnvVar{
							{
								Name:  "SWANSON_KIND",
								Value: swanson.Spec.Kind,
							},
						},
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(swanson, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// labelsForSwanson returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForSwanson(name string) map[string]string {
	var imageTag string
	image, err := imageForSwanson()
	if err == nil {
		imageTag = strings.Split(image, ":")[1]
	}
	return map[string]string{"app.kubernetes.io/name": "Swanson",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/part-of":    "swansoner",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}

// imageForSwanson gets the Operand image which is managed by this controller
// from the Swanson_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForSwanson() (string, error) {
	return "jacobdevtime/swanson:v1", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SwansonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&parksv1alpha1.Swanson{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
