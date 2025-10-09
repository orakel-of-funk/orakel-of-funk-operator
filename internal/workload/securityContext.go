package workload

import (
	"context"
	"fmt"
	"maps"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

func mergePodSecurityContexts(ctx context.Context, base, extends *corev1.PodSecurityContext) *corev1.PodSecurityContext {
	log := logf.FromContext(ctx)

	if base == nil {
		log.Info("Base security context is nil, using override")
		return extends
	}

	if extends == nil {
		log.Info("Override security context is nil, using base")
		return base
	}

	merged := base.DeepCopy()
	if merged.RunAsUser == nil && extends.RunAsUser != nil {
		log.V(3).Info("Merging RunAsUser from override into base")
		merged.RunAsUser = extends.RunAsUser
	}
	if merged.RunAsGroup == nil && extends.RunAsGroup != nil {
		log.V(3).Info("Merging RunAsGroup from override into base")
		merged.RunAsGroup = extends.RunAsGroup
	}
	if merged.FSGroup == nil && extends.FSGroup != nil {
		log.V(3).Info("Merging FSGroup from override into base")
		merged.FSGroup = extends.FSGroup
	}
	if merged.RunAsNonRoot == nil && extends.RunAsNonRoot != nil {
		log.V(3).Info("Merging RunAsNonRoot from override into base")
		merged.RunAsNonRoot = extends.RunAsNonRoot
	}
	if merged.SeccompProfile == nil && extends.SeccompProfile != nil {
		log.V(3).Info("Merging SeccompProfile from override into base")
		merged.SeccompProfile = extends.SeccompProfile
	}
	if merged.SELinuxOptions == nil && extends.SELinuxOptions != nil {
		log.V(3).Info("Merging SELinuxOptions from override into base")
		merged.SELinuxOptions = extends.SELinuxOptions
	}
	if merged.SupplementalGroups == nil && extends.SupplementalGroups != nil {
		log.V(3).Info("Merging SupplementalGroups from override into base")
		merged.SupplementalGroups = extends.SupplementalGroups
	}
	if merged.SupplementalGroupsPolicy == nil && extends.SupplementalGroupsPolicy != nil {
		log.V(3).Info("Merging SupplementalGroupsPolicy from override into base")
		merged.SupplementalGroupsPolicy = extends.SupplementalGroupsPolicy
	}

	return merged
}

func mergeContainerSecurityContexts(ctx context.Context, base, extends *corev1.SecurityContext) *corev1.SecurityContext {
	log := logf.FromContext(ctx)

	if base == nil {
		log.Info("Base security context is nil, using override")
		return extends
	}

	if extends == nil {
		log.Info("Override security context is nil, using base")
		return base
	}

	merged := base.DeepCopy()
	if merged.RunAsUser == nil && extends.RunAsUser != nil {
		log.V(3).Info("Merging RunAsUser from override into base")
		merged.RunAsUser = extends.RunAsUser
	}
	if merged.RunAsGroup == nil && extends.RunAsGroup != nil {
		log.V(3).Info("Merging RunAsGroup from override into base")
		merged.RunAsGroup = extends.RunAsGroup
	}
	if merged.Capabilities == nil && extends.Capabilities != nil {
		log.V(3).Info("Merging Capabilities from override into base")
		merged.Capabilities = extends.Capabilities
	}

	if merged.Capabilities != nil && extends.Capabilities != nil {

		if merged.Capabilities.Drop == nil && extends.Capabilities.Drop != nil {
			merged.Capabilities.Drop = extends.Capabilities.Drop
		}

		if merged.Capabilities.Drop != nil && extends.Capabilities.Drop != nil {
			// Merge drop capabilities
			capabilities := append(merged.Capabilities.Drop, extends.Capabilities.Drop...)
			capabilityMap := make(map[corev1.Capability]bool)
			for _, cap := range capabilities {
				capabilityMap[cap] = true
			}
			merged.Capabilities.Drop = slices.Collect(maps.Keys(capabilityMap))
		}

		// Do we need to avoid conflicts with the add capabilities?
	}

	if merged.Privileged == nil && extends.Privileged != nil {
		log.V(3).Info("Merging Privileged from override into base")
		merged.Privileged = extends.Privileged
	}
	if merged.AllowPrivilegeEscalation == nil && extends.AllowPrivilegeEscalation != nil {
		log.V(3).Info("Merging AllowPrivilegeEscalation from override into base")
		merged.AllowPrivilegeEscalation = extends.AllowPrivilegeEscalation
	}
	if merged.ReadOnlyRootFilesystem == nil && extends.ReadOnlyRootFilesystem != nil {
		log.V(3).Info("Merging ReadOnlyRootFilesystem from override into base")
		merged.ReadOnlyRootFilesystem = extends.ReadOnlyRootFilesystem
	}
	if merged.SeccompProfile == nil && extends.SeccompProfile != nil {
		log.V(3).Info("Merging SeccompProfile from override into base")
		merged.SeccompProfile = extends.SeccompProfile
	}
	if merged.SELinuxOptions == nil && extends.SELinuxOptions != nil {
		log.V(3).Info("Merging SELinuxOptions from override into base")
		merged.SELinuxOptions = extends.SELinuxOptions
	}

	return merged
}

func ApplyCheckSecurityContext(ctx context.Context, workloadUnderTest *client.Object, containerSecurityContext *checksv1alpha1.ContainerSecurityContextDefaults, podSecurityContext *checksv1alpha1.PodSecurityContextDefaults) error {
	var podSpecTemplate *corev1.PodSpec
	switch v := (*workloadUnderTest).(type) {
	case *appsv1.Deployment:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.StatefulSet:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.DaemonSet:
		podSpecTemplate = &v.Spec.Template.Spec
	}

	if podSpecTemplate != nil {
		applySecurityContext(ctx, podSpecTemplate, containerSecurityContext, podSecurityContext)
		return nil
	}

	return fmt.Errorf("kind of workloadUnderTest not supported")
}

func applySecurityContext(ctx context.Context, podSpec *corev1.PodSpec, containerSecurityContext *checksv1alpha1.ContainerSecurityContextDefaults, podSecurityContext *checksv1alpha1.PodSecurityContextDefaults) {
	if podSpec.SecurityContext == nil {
		podSpec.SecurityContext = podSecurityContext.ToK8sSecurityContext()
	} else {
		podSpec.SecurityContext = mergePodSecurityContexts(ctx, podSpec.SecurityContext, podSecurityContext.ToK8sSecurityContext())
	}

	for i := range podSpec.Containers {
		if podSpec.Containers[i].SecurityContext == nil {
			podSpec.Containers[i].SecurityContext = containerSecurityContext.ToK8sSecurityContext()
		} else {
			podSpec.Containers[i].SecurityContext = mergeContainerSecurityContexts(ctx, podSpec.Containers[i].SecurityContext, containerSecurityContext.ToK8sSecurityContext())
		}
	}

	for i := range podSpec.InitContainers {
		if podSpec.InitContainers[i].SecurityContext == nil {
			podSpec.InitContainers[i].SecurityContext = containerSecurityContext.ToK8sSecurityContext()
		} else {
			podSpec.InitContainers[i].SecurityContext = mergeContainerSecurityContexts(ctx, podSpec.InitContainers[i].SecurityContext, containerSecurityContext.ToK8sSecurityContext())
		}
	}
}

func ApplySecurityContext(ctx context.Context, workloadUnderTest *client.Object, containerSecurityContext *corev1.SecurityContext, podSecurityContext *corev1.PodSecurityContext) error {

	var podSpecTemplate *corev1.PodSpec
	switch v := (*workloadUnderTest).(type) {
	case *appsv1.Deployment:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.StatefulSet:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.DaemonSet:
		podSpecTemplate = &v.Spec.Template.Spec
	}

	if podSpecTemplate != nil {
		if podSpecTemplate.SecurityContext == nil {
			podSpecTemplate.SecurityContext = podSecurityContext
		} else {
			podSpecTemplate.SecurityContext = mergePodSecurityContexts(ctx, podSpecTemplate.SecurityContext, podSecurityContext)
		}

		for i := range podSpecTemplate.Containers {
			if podSpecTemplate.Containers[i].SecurityContext == nil {
				podSpecTemplate.Containers[i].SecurityContext = containerSecurityContext
			} else {
				podSpecTemplate.Containers[i].SecurityContext = mergeContainerSecurityContexts(ctx, podSpecTemplate.Containers[i].SecurityContext, containerSecurityContext)
			}
		}

		for i := range podSpecTemplate.InitContainers {
			if podSpecTemplate.InitContainers[i].SecurityContext == nil {
				podSpecTemplate.InitContainers[i].SecurityContext = containerSecurityContext
			} else {
				podSpecTemplate.InitContainers[i].SecurityContext = mergeContainerSecurityContexts(ctx, podSpecTemplate.InitContainers[i].SecurityContext, containerSecurityContext)
			}
		}
		return nil
	}

	return fmt.Errorf("supported workload kind, with empty podSpecTemplate")
}
