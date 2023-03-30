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

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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
	logger   logr.Logger
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
func (r *SwansonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx)
	r.logger.Info("Reconciling object")
	defer func() {
		r.logger.Info("Finished reconciling object")
	}()

	// =========================================================================
	// Fetch the Swanson object

	swanson := &parksv1alpha1.Swanson{}
	err := r.Get(ctx, req.NamespacedName, swanson)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found; it was likely deleted
			r.logger.Info("Swanson resource not found. Ignoring, as the object was likely deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object; requeue the request
		r.logger.Error(err, "Failed to get Swanson resource")
		return ctrl.Result{}, err
	}

	// =========================================================================
	// Check if the Swanson object is being deleted

	isSwansonMarkedToBeDeleted := swanson.GetDeletionTimestamp() != nil
	if isSwansonMarkedToBeDeleted {
		r.Recorder.Event(swanson, "Warning", "Deleting",
			fmt.Sprintf("Swanson object %s is being deleted from the namespace %s",
				swanson.Name,
				swanson.Namespace))
		return ctrl.Result{}, nil
	}

	// =========================================================================
	// Check if the Swanson Deployment already exists, and create a new
	// Deployment if necessary

	foundDep := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: swanson.Name, Namespace: swanson.Namespace}, foundDep)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new Deployment
		dep, depErr := r.deploymentForSwanson(swanson)
		if depErr != nil {
			msg := "Failed to define new Deployment resource for Swanson"
			r.logger.Error(depErr, msg)
			return r.handleFailure(ctx, swanson, msg, depErr)
		}

		// Create a new Deployment
		r.logger.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if createErr := r.Create(ctx, dep); createErr != nil {
			msg := "Failed to create new Deployment"
			r.logger.Error(createErr, msg,
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return r.handleFailure(ctx, swanson, msg, createErr)
		}

		// =========================================================================
		// The Deployment has been created successfully
		// Requeue to ensure state and move forward with next operations

		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		msg := "Failed to get Deployment"
		r.logger.Error(err, msg)
		return r.handleFailure(ctx, swanson, msg, err)
	}

	// =========================================================================
	// Ensure the desired state of the Swanson "Kind" defined in our CRD.
	// The "swanson" application references an environment variable to determine
	// the GIF to serve.

	// Safely grab the container ID
	containerIdx := slices.IndexFunc(foundDep.Spec.Template.Spec.Containers,
		func(c corev1.Container) bool { return c.Name == deploymentSpecContainerName },
	)
	if containerIdx == -1 {
		msg := "Failed to get the container ID for the Deployment"
		containerNotFoundErr := fmt.Errorf("container ID does not exist for container %s",
			deploymentSpecContainerName)

		r.logger.Error(containerNotFoundErr, msg)
		return r.handleFailure(ctx, swanson, msg, containerNotFoundErr)
	}

	// Safely grab the env var ID
	envVarIdx := slices.IndexFunc(foundDep.Spec.Template.Spec.Containers[containerIdx].Env,
		func(e corev1.EnvVar) bool { return e.Name == envVarSwansonKind },
	)
	if envVarIdx == -1 {
		msg := "Failed to get the env var ID for the Deployment"
		envVarNotFoundErr := fmt.Errorf("env var ID does not exist for container env var %s", envVarSwansonKind)

		r.logger.Error(envVarNotFoundErr, msg)
		return r.handleFailure(ctx, swanson, msg, envVarNotFoundErr)
	}

	// Compare the desired ".Spec.Kind" state to the actual state
	kind := swanson.Spec.Kind
	if foundDep.Spec.Template.Spec.Containers[containerIdx].Env[envVarIdx].Value != kind {
		foundDep.Spec.Template.Spec.Containers[containerIdx].Env[envVarIdx].Value = kind
		if err = r.Update(ctx, foundDep); err != nil {
			msg := "Failed to update the Swanson Kind for the Deployment"
			r.logger.Error(err, msg,
				"Deployment.Namespace", foundDep.Namespace, "Deployment.Name", foundDep.Name)
			return r.handleFailure(ctx, swanson, msg, err)
		}

		// =========================================================================
		// The Swanson Kind has been updated successfully
		// Requeue to ensure state and move forward with next operations

		return ctrl.Result{Requeue: true}, nil
	}

	// =========================================================================
	// Ensure the desired state of the Swanson "Size" defined in our CRD.

	size := swanson.Spec.Size
	if *foundDep.Spec.Replicas != size {
		foundDep.Spec.Replicas = &size
		if err = r.Update(ctx, foundDep); err != nil {
			msg := "Failed to update the Swanson Size for the Deployment"
			r.logger.Error(err, msg,
				"Deployment.Namespace", foundDep.Namespace, "Deployment.Name", foundDep.Name)
			return r.handleFailure(ctx, swanson, msg, err)
		}

		// =========================================================================
		// The Swanson Size has been updated successfully
		// Requeue to ensure state and move forward with next operations

		return ctrl.Result{Requeue: true}, nil
	}

	// =========================================================================
	// Check if the Swanson Service already exists, and create a new
	// Service if necessary

	foundSvc := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: swanson.Name, Namespace: swanson.Namespace}, foundSvc)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new Service
		svc, svcErr := r.serviceForSwanson(swanson)
		if svcErr != nil {
			msg := "Failed to define new Service resource for Swanson"
			r.logger.Error(svcErr, msg)
			return r.handleFailure(ctx, swanson, msg, svcErr)
		}

		// Create a new Service
		r.logger.Info("Creating a new Service",
			"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		if createErr := r.Create(ctx, svc); createErr != nil {
			msg := "Failed to create new Deployment"
			r.logger.Error(createErr, msg,
				"Deployment.Namespace", svc.Namespace, "Deployment.Name", svc.Name)
			return r.handleFailure(ctx, swanson, msg, createErr)
		}

		// =========================================================================
		// The Service has been created successfully

	} else if err != nil {
		msg := "Failed to get Service"
		r.logger.Error(err, msg)
		return r.handleFailure(ctx, swanson, msg, err)
	}

	// =========================================================================
	// Reconcile has finished

	msg := fmt.Sprintf("Swanson %s resources have been created successfully with Size %d and Kind %s",
		swanson.Name, size, kind)
	return r.handleSuccess(ctx, swanson, msg)
}

// handleSuccess manages reconcile success.
func (r *SwansonReconciler) handleSuccess(
	ctx context.Context, swanson *parksv1alpha1.Swanson, message string) (ctrl.Result, error) {
	// Update the Status
	meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: message})

	if updateErr := r.Status().Update(ctx, swanson); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			// Requeue without error on Status update conflicts
			return ctrl.Result{Requeue: true}, nil
		}
		r.logger.Error(updateErr, "Failed to update Swanson status")
		return ctrl.Result{}, updateErr
	}

	return ctrl.Result{}, nil
}

// handleFailure manages reconcile failure.
func (r *SwansonReconciler) handleFailure(
	ctx context.Context, swanson *parksv1alpha1.Swanson, message string, err error) (ctrl.Result, error) {
	// Update the Status
	meta.SetStatusCondition(&swanson.Status.Conditions, metav1.Condition{Type: typeAvailableSwanson,
		Status: metav1.ConditionFalse, Reason: "Reconciling",
		Message: fmt.Sprintf("%s: %s", message, err)})

	if updateErr := r.Status().Update(ctx, swanson); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			// Requeue on without error Status update conflicts
			return ctrl.Result{Requeue: true}, nil
		}
		r.logger.Error(updateErr, "Failed to update Swanson status")
		return ctrl.Result{}, updateErr
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *SwansonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&parksv1alpha1.Swanson{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
