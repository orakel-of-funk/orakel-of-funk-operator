package namespace

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	oflabels "github.com/orakel-of-funk/orakel-of-funk-operator/internal/labels"
)

func Clone(ctx context.Context, cl client.Client, sourceNamespace, targetNamespace, suffix string) error {
	log := logf.FromContext(ctx).WithName("namespace-cloner").WithValues("targetNamespace", targetNamespace, "suffix", suffix)

	targetNs := &corev1.Namespace{}
	err := cl.Get(ctx, client.ObjectKey{Name: targetNamespace}, targetNs)
	// we expect at least a NotFound error here, otherwise the namespace already exists, and we don't want to override it
	if err == nil {
		return fmt.Errorf("target namespace already exists %s", targetNamespace)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("error fetching namespace: %w", err)
	}

	// only get topLevelResources without ownership
	topLevelResources := GetTopLevelResources(ctx, sourceNamespace)

	// create targetNamespace
	targetNs.Name = targetNamespace
	targetNs.Labels = map[string]string{
		"app.kubernetes.io/name":    targetNamespace,
		oflabels.LabelManagedBy:     oflabels.ManagedByValue,
		oflabels.LabelSourceNamespace: sourceNamespace,
		oflabels.LabelSuffix:        suffix,
	}

	err = cl.Create(ctx, targetNs)
	if err != nil {
		return fmt.Errorf("error creating target namespace: %w", err)
	}

	// First let's clone the clusterRoleBindings, as they are not namespaced
	// and pods might fail if their serviceAccounts are not bound to the correct roles
	clonedClusterRoleBindings, err := cloneClusterRoleBindings(ctx, cl, sourceNamespace, targetNamespace, suffix)
	if err != nil {
		return fmt.Errorf("error cloning cluster role bindings: %w", err)
	}

	// Now we can clone the resources, and be sure that the serviceAccounts have their rolebindings in place
	clonedResourcesCount := 0
	failedToClone := 0
	for _, resource := range topLevelResources {

		// Skip resources which automatically created in each namespace
		if (resource.GetKind() == "ServiceAccount" && resource.GetName() == "default") ||
			(resource.GetKind() == "ConfigMap" && resource.GetName() == "kube-root-ca.crt") {
			continue
		}

		log.V(3).Info(fmt.Sprintf("cloning %s/%s", resource.GetKind(), resource.GetName()))

		// cleanup resource before cloning
		clonedResource := resource.DeepCopy()
		clonedResource.SetResourceVersion("")
		clonedResource.SetSelfLink("")
		clonedResource.SetGeneration(0)
		clonedResource.SetUID("")
		// override namespace!
		clonedResource.SetNamespace(targetNamespace)

		if clonedResource.GetKind() == "PersistentVolumeClaim" {
			// remove all annotations, as the storage controller adds its own which will conflict
			clonedResource.SetAnnotations(map[string]string{})
			// we need to remove the PersistentVolumeClaim's reference to the PersistentVolume
			clonedResource.Object["spec"].(map[string]any)["volumeName"] = ""
		}

		// Remove all IPs from services, otherwise they will conflict with the original namespace
		if clonedResource.GetKind() == "Service" {
			var svc *corev1.Service
			// Convert to service object
			runtime.DefaultUnstructuredConverter.FromUnstructured(clonedResource.Object, &svc)

			svc.Spec.ClusterIP = ""
			svc.Spec.ClusterIPs = []string{}
			svc.Spec.ExternalIPs = []string{}
			svc.Spec.LoadBalancerIP = ""

			// Convert back to unstructured object
			clonedResource.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(svc)
		}

		if clonedResource.GetKind() == "RoleBinding" {
			// RoleBindings are namespaced, so we need to change the namespace of the subjects
			for i, subject := range clonedResource.Object["subjects"].([]interface{}) {
				subjectMap := subject.(map[string]interface{})
				if subjectMap["kind"] == "ServiceAccount" && subjectMap["namespace"] == sourceNamespace {
					subjectMap["namespace"] = targetNamespace
					clonedResource.Object["subjects"].([]interface{})[i] = subjectMap
				}
			}
		}

		err = cl.Create(ctx, clonedResource)
		if err != nil {
			failedToClone++
			log.Error(
				err,
				fmt.Sprintf("error creating %s/%s", resource.GetKind(), resource.GetName()),
				"kind", resource.GetKind(),
				"name", resource.GetName(),
			)
		} else {
			clonedResourcesCount++
			log.V(3).Info(fmt.Sprintf("created %s/%s", resource.GetKind(), resource.GetName()))
		}
	}

	log.Info(
		"namespace cloned",
		"clonedResources", clonedResourcesCount,
		"clondedClusterRoleBindings", clonedClusterRoleBindings,
		"failedResources", failedToClone,
	)

	return nil
}

func cloneClusterRoleBindings(ctx context.Context, cl client.Client, sourceNamespace, targetNamespace, suffix string) (int, error) {
	log := logf.FromContext(ctx).WithName("namespace-cloner").WithValues("targetNamespace", targetNamespace, "suffix", suffix)

	clusterRoleBindingList := &rbacv1.ClusterRoleBindingList{}
	cl.List(ctx, clusterRoleBindingList)

	clonedClusterRoleBindings := 0
	if len(clusterRoleBindingList.Items) > 0 {
		for _, clusterRoleBinding := range clusterRoleBindingList.Items {
			needsToBeCloned := false
			if clusterRoleBinding.Subjects != nil {
			Subjects:
				for _, subject := range clusterRoleBinding.Subjects {
					if subject.Kind == "ServiceAccount" && subject.Namespace == sourceNamespace {
						log.V(1).Info("found cluster role binding for service account in source namespace", "name", clusterRoleBinding.Name, "serviceAccount", subject.Name)
						needsToBeCloned = true
						break Subjects
					}
				}
			}

			if needsToBeCloned {
				clonedClusterRoleBindings++
				// we need to clone the cluster role binding to the target namespace
				clonedClusterRoleBinding := clusterRoleBinding.DeepCopy()
				clonedClusterRoleBinding.SetResourceVersion("")
				clonedClusterRoleBinding.SetSelfLink("")
				clonedClusterRoleBinding.SetUID("")
				clonedClusterRoleBinding.SetGeneration(0)

				roleBindingName := clusterRoleBinding.Name + "-" + targetNamespace
				// If the composite name is too long, we need to shorten it, and to avoid conflicts, we add a random suffix
				if len(roleBindingName) > 250 {
					suffixLength := 10
					if len(clusterRoleBinding.Name) > 245 {
						suffixLength = 253 - len(clusterRoleBinding.Name)
					}
					roleBindingName = clusterRoleBinding.Name + "-" + utilrand.String(suffixLength)
					if len(roleBindingName) > 253 {
						roleBindingName = roleBindingName[:253]
					}
				}
				clonedClusterRoleBinding.SetName(roleBindingName)

				// Add labels to the cloned ClusterRoleBinding
				if clonedClusterRoleBinding.Labels == nil {
					clonedClusterRoleBinding.Labels = make(map[string]string)
				}
				clonedClusterRoleBinding.Labels[oflabels.LabelManagedBy] = oflabels.ManagedByValue
				clonedClusterRoleBinding.Labels[oflabels.LabelSourceNamespace] = sourceNamespace
				clonedClusterRoleBinding.Labels[oflabels.LabelTargetNamespace] = targetNamespace
				clonedClusterRoleBinding.Labels[oflabels.LabelSuffix] = suffix

				// override namespace!
				for i, subject := range clonedClusterRoleBinding.Subjects {
					if subject.Kind == "ServiceAccount" && subject.Namespace == sourceNamespace {
						// change the namespace to the target namespace
						clonedClusterRoleBinding.Subjects[i].Namespace = targetNamespace
					}
				}

				err := cl.Create(ctx, clonedClusterRoleBinding)
				if err != nil {
					return clonedClusterRoleBindings, fmt.Errorf("error creating cluster role binding %s: %w", clusterRoleBinding.Name, err)
				}
			}

		}
	}

	return clonedClusterRoleBindings, nil
}
