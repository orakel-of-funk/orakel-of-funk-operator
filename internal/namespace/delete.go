package namespace

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	oflabels "github.com/orakel-of-funk/orakel-of-funk-operator/internal/labels"
)

func Delete(ctx context.Context, cl client.Client, targetNamespace string) error {
	log := logf.FromContext(ctx).WithName("namespace-deleter").WithValues("targetNamespace", targetNamespace)

	targetNs := &corev1.Namespace{}
	err := cl.Get(ctx, client.ObjectKey{Name: targetNamespace}, targetNs)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Namespace already deleted")
			return nil
		}
		log.Error(err, "Failed to get Namespace for deletion")
		return err
	}
	log.Info("Deleting Namespace")
	err = cl.Delete(ctx, targetNs)
	if err != nil {
		log.Error(err, "Failed to delete Namespace")
		return err
	}

	// Remove all ClusterRoleBindings that are associated with this namespace using a label selector
	clusterRoleBindingList := &rbacv1.ClusterRoleBindingList{}
	err = cl.List(ctx, clusterRoleBindingList, &client.ListOptions{
		LabelSelector: k8slabels.SelectorFromSet(map[string]string{
			oflabels.LabelTargetNamespace: targetNamespace,
		}),
	})
	if err != nil {
		log.Error(err, "Failed to list ClusterRoleBindings for cleanup")
		return err
	}

	for _, clusterRoleBinding := range clusterRoleBindingList.Items {
		log.Info("Deleting ClusterRoleBinding", "name", clusterRoleBinding.Name)
		err = cl.Delete(ctx, &clusterRoleBinding)
		if err != nil {
			log.Error(err, "Failed to delete ClusterRoleBinding", "name", clusterRoleBinding.Name)
		}
	}

	return nil
}
