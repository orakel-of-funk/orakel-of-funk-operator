package namespace

import (
	"context"
	"fmt"
	"slices"
	"strings"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var dynamicClient *dynamic.DynamicClient
var discoveryClient *discovery.DiscoveryClient
var namespacedResources []*v1.APIResourceList

var resourcesToSkip = []string{
	"binding",
	"bindings",
	"localsubjectaccessreview",
	"localsubjectaccessreviews",
	"endpointslice",
	"endpointslices",
	"endpoint",
	"endpoints",
	"event",
	"events",
	"ingress",
	"ingresses",
	"httproute",
	"httproutes",
	"gateway",
	"gateways",
	"podmetric",
	"podmetrics",
	"controllerrevision",
	"controllerrevisions",
	// exclude our own resource to avoid infinite loops
	"workloadhardeningcheck",
	"workloadhardeningchecks",
	"namespacehardeningcheck",
	"namespacehardeningchecks",
}

func init() {
	dynamicClient = dynamic.NewForConfigOrDie(config.GetConfigOrDie())
	discoveryClient = discovery.NewDiscoveryClientForConfigOrDie(config.GetConfigOrDie())

	var err error
	namespacedResources, err = discoveryClient.ServerPreferredNamespacedResources()
	if err != nil {
		// ignore groupDiscoveryFailed error, most likely due to orphaned CRDs
		if !discovery.IsGroupDiscoveryFailedError(err) {
			utilruntime.Must(fmt.Errorf("error fetching namespaced resources: %w", err))
		}
	}

}

func GetTopLevelResources(ctx context.Context, namespace string) []*unstructured.Unstructured {

	allResources := getAllResources(ctx, namespace)

	unownedResources := []*unstructured.Unstructured{}
	for _, resourceList := range allResources {
		for _, resource := range resourceList.Items {
			if len(resource.GetOwnerReferences()) == 0 {
				unownedResources = append(unownedResources, &resource)
			}
		}
	}

	return unownedResources

}

// Get all resources in a namespace, this doesn't filter owned resources
// First we need to use a discovery client to get all namespaced resource types
// Afterwards we can use a dynamic client, to load resources from any type into Unstructred objects
func getAllResources(ctx context.Context, namespace string) map[string]*unstructured.UnstructuredList {
	log := logf.FromContext(ctx).WithName("runner")

	resources := make(map[string]*unstructured.UnstructuredList)
	for _, resourceList := range namespacedResources {
		if len(resourceList.APIResources) == 0 {
			log.V(3).Info("no resources found for group version", "groupVersion", resourceList.GroupVersion)
			continue
		}

		for _, resourceType := range resourceList.APIResources {
			if !resourceType.Namespaced {
				log.V(2).Info("skipping non-namespaced resource", "resourceType", resourceType.Name)
				continue
			}

			// Skip all resources in the metrics group
			if strings.Contains(resourceType.Group, "metrics") {
				continue
			}

			if slices.Contains(resourcesToSkip, strings.ToLower(resourceType.Kind)) || strings.Contains(resourceType.Name, "/") {
				log.V(2).Info("skipping resource", "resourceKind", resourceType.Kind, "resourceName", resourceType.Name)
				continue
			}
			groupVersion, err := schema.ParseGroupVersion(resourceList.GroupVersion)
			if err != nil {
				log.Error(err, "error parsing group version", "groupVersion", resourceList.GroupVersion)
				continue
			}

			gvr := schema.GroupVersionResource{
				Group:    groupVersion.Group,
				Version:  groupVersion.Version,
				Resource: resourceType.Name,
			}

			resourceObjects, err := dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, v1.ListOptions{})

			if err != nil {
				log.Error(err, "error listing resources", "kind", gvr.String())
				continue
			}
			if len(resourceObjects.Items) == 0 {
				log.V(2).Info("no resources found for", "kind", gvr.String())
				continue
			}
			log.V(3).Info(fmt.Sprintf("found %d resources for: %s\n", len(resourceObjects.Items), gvr.String()))

			if _, ok := resources[resourceType.Name]; !ok {
				resources[resourceType.Name] = &unstructured.UnstructuredList{}
				resources[resourceType.Name].SetGroupVersionKind(gvr.GroupVersion().WithKind(resourceType.Kind))
			}
			// Append the items to the existing list
			resources[resourceType.Name].Items = append(resources[resourceType.Name].Items, resourceObjects.Items...)
		}

	}

	return resources
}
