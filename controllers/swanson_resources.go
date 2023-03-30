package controllers

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"

	parksv1alpha1 "github.com/jacobboykin/swansonator/api/v1alpha1"
)

const (
	containerImage              = "jacobdevtime/swanson:v1"
	deploymentSpecContainerName = "swanson"
	envVarSwansonKind           = "SWANSON_KIND"
	servicePort                 = 8080
)

// serviceForSwanson returns a Swanson Service object.
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
					Port: servicePort,
					TargetPort: intstr.IntOrString{
						IntVal: servicePort,
					},
				},
			},
			Selector: labelsForSwanson(swanson.Name),
		},
	}

	// Set the ownerRef for the Service
	if err := ctrl.SetControllerReference(swanson, svc, r.Scheme); err != nil {
		return nil, err
	}

	return svc, nil
}

// deploymentForSwanson returns a Swanson Deployment object.
func (r *SwansonReconciler) deploymentForSwanson(swanson *parksv1alpha1.Swanson) (*appsv1.Deployment, error) {
	ls := labelsForSwanson(swanson.Name)
	replicas := swanson.Spec.Size

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
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           containerImage,
						Name:            deploymentSpecContainerName,
						ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             &[]bool{true}[0],
							RunAsUser:                &[]int64{65532}[0],
							AllowPrivilegeEscalation: &[]bool{false}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: servicePort,
							Name:          deploymentSpecContainerName,
						}},
						Env: []corev1.EnvVar{
							{
								Name:  envVarSwansonKind,
								Value: swanson.Spec.Kind,
							},
						},
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	if err := ctrl.SetControllerReference(swanson, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// labelsForSwanson returns the labels for selecting the resources.
func labelsForSwanson(name string) map[string]string {
	imageTag := strings.Split(containerImage, ":")[1]

	return map[string]string{"app.kubernetes.io/name": "Swanson",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/part-of":    "swansonator",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}
