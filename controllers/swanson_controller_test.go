package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	parksv1alpha1 "github.com/jacobboykin/swansoner/api/v1alpha1"
)

var _ = Describe("Swanson controller", func() {
	Context("Swanson controller test", func() {

		const SwansonName = "test-swanson"

		ctx := context.Background()

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      SwansonName,
				Namespace: SwansonName,
			},
		}

		typeNamespaceName := types.NamespacedName{Name: SwansonName, Namespace: SwansonName}

		BeforeEach(func() {
			By("Creating the Namespace to perform the tests")
			err := k8sClient.Create(ctx, namespace)
			Expect(err).To(Not(HaveOccurred()))
		})

		AfterEach(func() {
			// Be aware of the current delete namespace limitations
			// https://book.kubebuilder.io/reference/envtest.html#testing-considerations
			By("Deleting the Namespace to perform the tests")
			_ = k8sClient.Delete(ctx, namespace)
		})

		It("should successfully reconcile a custom resource for Swanson", func() {
			By("Creating the custom resource for the Kind Swanson")
			swanson := &parksv1alpha1.Swanson{}
			err := k8sClient.Get(ctx, typeNamespaceName, swanson)
			if err != nil && errors.IsNotFound(err) {
				swanson := &parksv1alpha1.Swanson{
					ObjectMeta: metav1.ObjectMeta{
						Name:      SwansonName,
						Namespace: namespace.Name,
					},
					Spec: parksv1alpha1.SwansonSpec{
						Size: 1,
						Kind: "happy",
					},
				}

				err = k8sClient.Create(ctx, swanson)
				Expect(err).To(Not(HaveOccurred()))
			}

			By("Checking if the custom resource was successfully created")
			Eventually(func() error {
				found := &parksv1alpha1.Swanson{}
				return k8sClient.Get(ctx, typeNamespaceName, found)
			}, time.Minute, time.Second).Should(Succeed())

			By("Reconciling the custom resource created")
			swansonReconciler := &SwansonReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err = swansonReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespaceName,
			})
			Expect(err).To(Not(HaveOccurred()))

			By("Checking if Deployment was successfully created in the reconciliation")
			Eventually(func() error {
				found := &appsv1.Deployment{}
				return k8sClient.Get(ctx, typeNamespaceName, found)
			}, time.Minute, time.Second).Should(Succeed())

			By("Checking the latest Status Condition added to the Swanson instance")
			Eventually(func() error {
				if swanson.Status.Conditions != nil && len(swanson.Status.Conditions) != 0 {
					latestStatusCondition := swanson.Status.Conditions[len(swanson.Status.Conditions)-1]
					expectedLatestStatusCondition := metav1.Condition{Type: typeAvailableSwanson,
						Status: metav1.ConditionTrue, Reason: "Reconciling",
						Message: fmt.Sprintf("Swanson %s resources have been created successfully with Size %d and Kind %s",
							swanson.Name, swanson.Spec.Size, swanson.Spec.Kind)}
					if latestStatusCondition != expectedLatestStatusCondition {
						return fmt.Errorf(
							"the latest status condition added to the swanson instance is not as expected",
						)
					}
				}
				return nil
			}, time.Minute, time.Second).Should(Succeed())
		})
	})
})
