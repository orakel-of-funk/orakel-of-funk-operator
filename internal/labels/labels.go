// Package labels defines shared label constants used across the orakel-of-funk operator
// for identifying and managing cloned resources.
package labels

const (
	// LabelSourceNamespace identifies the original namespace that was cloned.
	LabelSourceNamespace = "orakel.ofunk.org/source-namespace"

	// LabelTargetNamespace identifies the target (cloned) namespace.
	// Used on ClusterRoleBindings to track which namespace they belong to.
	LabelTargetNamespace = "orakel.ofunk.org/target-namespace"

	// LabelSuffix stores the suffix used for this check run's namespaces.
	LabelSuffix = "orakel.ofunk.org/suffix"

	// LabelCheckRunID stores the CheckRunID for a cloned namespace,
	// enabling detect & resume on operator restart.
	LabelCheckRunID = "orakel.ofunk.org/check-run-id"

	// LabelManagedBy is the standard Kubernetes label for identifying the managing component.
	LabelManagedBy = "app.kubernetes.io/managed-by"

	// ManagedByValue is the value used for the app.kubernetes.io/managed-by label.
	ManagedByValue = "orakel-of-funk"

	// FinalizerCleanup is the finalizer used to ensure cleanup of cloned namespaces
	// and ClusterRoleBindings when a WorkloadHardeningCheck or NamespaceHardeningCheck is deleted.
	FinalizerCleanup = "orakel.ofunk.org/cleanup"
)
