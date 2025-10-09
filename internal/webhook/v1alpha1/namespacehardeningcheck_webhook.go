package v1alpha1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var namespacehardeningchecklog = logf.Log.WithName("namespacehardeningcheck-resource")

// SetupNamespaceHardeningCheckWebhookWithManager registers the webhook for NamespaceHardeningCheck in the manager.
func SetupNamespaceHardeningCheckWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&checksv1alpha1.NamespaceHardeningCheck{}).
		WithDefaulter(&NamespaceHardeningCheckCustomDefaulter{}).
		WithValidator(&NamespaceHardeningCheckCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-checks-funk-fhnw-ch-v1alpha1-namespacehardeningcheck,mutating=true,failurePolicy=fail,sideEffects=None,groups=checks.funk.fhnw.ch,resources=namespacehardeningchecks,verbs=create;update,versions=v1alpha1,name=mnamespacehardeningcheck-v1alpha1.kb.io,admissionReviewVersions=v1

// NamespaceHardeningCheckCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind NamespaceHardeningCheck when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type NamespaceHardeningCheckCustomDefaulter struct {
}

var _ webhook.CustomDefaulter = &NamespaceHardeningCheckCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind NamespaceHardeningCheck.
func (d *NamespaceHardeningCheckCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	namespaceHardeningCheck, ok := obj.(*checksv1alpha1.NamespaceHardeningCheck)

	if !ok {
		namespacehardeningchecklog.Info("object type mismatch", "expected", "NamespaceHardeningCheck", "got", fmt.Sprintf("%T", obj))
		return fmt.Errorf("expected an NamespaceHardeningCheck object but got %T", obj)
	}
	namespacehardeningchecklog.Info("Defaulting for NamespaceHardeningCheck", "name", namespaceHardeningCheck.GetName())

	// Set suffix as early as possible
	if len(namespaceHardeningCheck.Spec.Suffix) == 0 {
		namespacehardeningchecklog.Info("Setting suffix for NamespaceHardeningCheck", "name", namespaceHardeningCheck.GetName())
		namespaceHardeningCheck.Spec.Suffix = utilrand.String(5) // Generate a random suffix of 8 characters
	} else {
		namespacehardeningchecklog.V(2).Info("Suffix already set", "suffix", namespaceHardeningCheck.Spec.Suffix)
	}

	return nil
}

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-checks-funk-fhnw-ch-v1alpha1-namespacehardeningcheck,mutating=false,failurePolicy=fail,sideEffects=None,groups=checks.funk.fhnw.ch,resources=namespacehardeningchecks,verbs=create;update,versions=v1alpha1,name=vnamespacehardeningcheck-v1alpha1.kb.io,admissionReviewVersions=v1

// NamespaceHardeningCheckCustomValidator struct is responsible for validating the NamespaceHardeningCheck resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type NamespaceHardeningCheckCustomValidator struct {
	client.Client
}

var _ webhook.CustomValidator = &NamespaceHardeningCheckCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type NamespaceHardeningCheck.
func (v *NamespaceHardeningCheckCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	namespacehardeningcheck, ok := obj.(*checksv1alpha1.NamespaceHardeningCheck)
	if !ok {
		namespacehardeningchecklog.Info("object type mismatch", "expected", "NamespaceHardeningCheck", "got", fmt.Sprintf("%T", obj))
		return nil, fmt.Errorf("expected a NamespaceHardeningCheck object but got %T", obj)
	}
	namespacehardeningchecklog.Info("Validation for NamespaceHardeningCheck upon creation", "name", namespacehardeningcheck.GetName())

	if namespacehardeningcheck.Spec.Suffix == "" {
		namespacehardeningchecklog.Info("Suffix is empty during creation, this is not allowed", "name", namespacehardeningcheck.GetName())
		return nil, fmt.Errorf("suffix must be set during creation of NamespaceHardeningCheck")
	}

	// ToDo: Add validation the namespacehardeningcheck.Spec.Namespace exists, requires runtime-client to be injected somehow
	if namespacehardeningcheck.Spec.TargetNamespace == "" {
		namespacehardeningchecklog.Info("Namespace is empty during creation, this is not allowed", "name", namespacehardeningcheck.GetName())
		return nil, fmt.Errorf("namespace must be set during creation of NamespaceHardeningCheck")
	}

	targetNamespace := &corev1.Namespace{}
	if err := v.Get(context.Background(), client.ObjectKey{Name: namespacehardeningcheck.Spec.TargetNamespace}, targetNamespace); err != nil {
		namespacehardeningchecklog.Error(err, "Failed to get target namespace", "name", namespacehardeningcheck.Spec.TargetNamespace)
		return nil, fmt.Errorf("target namespace %s does not exist", namespacehardeningcheck.Spec.TargetNamespace)
	}
	namespacehardeningchecklog.V(2).Info("Target namespace exists", "name", targetNamespace.GetName())

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type NamespaceHardeningCheck.
func (v *NamespaceHardeningCheckCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldNamespaceHardeningCheck, ok := oldObj.(*checksv1alpha1.NamespaceHardeningCheck)
	newNamespaceHardeningCheck, ok2 := newObj.(*checksv1alpha1.NamespaceHardeningCheck)
	if !ok || !ok2 {
		return nil, fmt.Errorf("expected a NamespaceHardeningCheck object for the newObj but got %T", newObj)
	}
	namespacehardeningchecklog.Info("Validation for NamespaceHardeningCheck upon update", "name", newNamespaceHardeningCheck.GetName())

	// Suffix cannot be changed during update
	if oldNamespaceHardeningCheck.Spec.Suffix != newNamespaceHardeningCheck.Spec.Suffix {
		namespacehardeningchecklog.Info("Suffix cannot be changed during update", "oldSuffix", oldNamespaceHardeningCheck.Spec.Suffix, "newSuffix", newNamespaceHardeningCheck.Spec.Suffix)
		return nil, fmt.Errorf("suffix cannot be changed during update of NamespaceHardeningCheck")
	}

	// TargetNamespace cannot be changed during update
	if oldNamespaceHardeningCheck.Spec.TargetNamespace != newNamespaceHardeningCheck.Spec.TargetNamespace {
		namespacehardeningchecklog.Info("TargetNamespace cannot be changed during update", "oldTargetNamespace", oldNamespaceHardeningCheck.Spec.TargetNamespace, "newTargetNamespace", newNamespaceHardeningCheck.Spec.TargetNamespace)
		return nil, fmt.Errorf("targetNamespace cannot be changed during update of NamespaceHardeningCheck")
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type NamespaceHardeningCheck.
// This is just a placeholder to implement the webhook interface.
func (v *NamespaceHardeningCheckCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	namespacehardeningcheck, ok := obj.(*checksv1alpha1.NamespaceHardeningCheck)
	if !ok {
		return nil, fmt.Errorf("expected a NamespaceHardeningCheck object but got %T", obj)
	}
	namespacehardeningchecklog.Info("Validation for NamespaceHardeningCheck upon deletion", "name", namespacehardeningcheck.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
