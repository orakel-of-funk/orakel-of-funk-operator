# Orakel of Funk Helm Chart

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

Keep your Kubernetes workloads in tune — secure and functional.

The chart installs the Orakel of Funk operator, along a ValKey instance which is uesd by the operator to store its state.

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Mathias Petermann | <mathias.petermann@students.fhnw.ch> | <https://github.com/peschmae> |

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| fullnameOverride | string | `""` | Override the fullname of the chart. |
| global | object | `{"imagePullSecrets":[]}` | Global values are used to set default values for all charts in the deployment. |
| globalAnnotations | object | `{}` | Global annotations applied to all resources. |
| globalLabels | object | `{}` | Global labels applied to all resources. |
| nameOverride | string | `""` | This is to override the chart name. |
| namespaceOverride | string | `"orakel-of-funk-system"` | This is to override the namespace where the chart is deployed. |
| orakel | object | `{"affinity":{},"image":{"pullPolicy":"IfNotPresent","registry":"ghcr.io","repository":"orakel-of-funk/orakel-of-funk-operator","tag":"0.0.1"},"livenessProbe":{"httpGet":{"path":"/healthz","port":8081},"initialDelaySeconds":15,"periodSeconds":20},"metricsService":{"annotations":{},"labels":{},"port":8443,"type":"ClusterIP"},"nodeSelector":{},"podAnnotations":{},"podLabels":{},"podSecurityContext":{"fsGroup":2492,"runAsGroup":2492,"runAsNonRoot":true,"runAsUser":2492,"seccompProfile":{"type":"RuntimeDefault"}},"readinessProbe":{"httpGet":{"path":"/readyz","port":8081},"initialDelaySeconds":5,"periodSeconds":10},"replicaCount":1,"resources":{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}},"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}},"serviceAccount":{"annotations":{},"automount":true,"create":true,"name":""},"tolerations":[],"volumeMounts":[{"mountPath":"/tmp/k8s-webhook-server/serving-certs","name":"cert","readOnly":true}],"volumes":[{"name":"cert","secret":{"defaultMode":420,"secretName":"webhook-server-cert"}}],"webhookService":{"annotations":{},"labels":{},"port":443,"type":"ClusterIP"}}` | The orakel-of-funk controller configuration. |
| orakel.image | object | `{"pullPolicy":"IfNotPresent","registry":"ghcr.io","repository":"orakel-of-funk/orakel-of-funk-operator","tag":"0.0.1"}` | Container image used for the operator |
| orakel.image.registry | string | `"ghcr.io"` | Prefix for the image repository, if you mirror the images to a private registry, you need to set this. |
| orakel.image.tag | string | `"0.0.1"` | Overrides the image tag whose default is the chart appVersion. |
| orakel.livenessProbe | object | `{"httpGet":{"path":"/healthz","port":8081},"initialDelaySeconds":15,"periodSeconds":20}` | Liveness probe using the default /healthz endpoint provided by the controller-runtime library. |
| orakel.metricsService | object | `{"annotations":{},"labels":{},"port":8443,"type":"ClusterIP"}` | The metrics service, this is used to scrape metrics from the operator. |
| orakel.podAnnotations | object | `{}` | Annotations only added to the pod template, not the deployment |
| orakel.podLabels | object | `{}` | Labels only added to the pod template, not the deployment |
| orakel.podSecurityContext | object | `{"fsGroup":2492,"runAsGroup":2492,"runAsNonRoot":true,"runAsUser":2492,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context for the controller pod, this is configured to pass the "restricted" profile of the Pod Security Standards. |
| orakel.readinessProbe | object | `{"httpGet":{"path":"/readyz","port":8081},"initialDelaySeconds":5,"periodSeconds":10}` | Liveness probe using the default /readyz endpoint provided by the controller-runtime library. |
| orakel.resources | object | `{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}}` | The default resource usage of the controller pod, this is set to a low value, so that the controller can run on smaller clusters. |
| orakel.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}` | Pod security context for the controller pod, this is configured to pass the "restricted" profile of the Pod Security Standards. |
| orakel.serviceAccount | object | `{"annotations":{},"automount":true,"create":true,"name":""}` | The operator clones a full namespace, and recreates all resources within so the service account needs to have the permissions to do so |
| orakel.serviceAccount.annotations | object | `{}` | Annotations to add to the service account |
| orakel.serviceAccount.automount | bool | `true` | Automatically mount a ServiceAccount's API credentials? |
| orakel.serviceAccount.create | bool | `true` | Specifies whether a service account should be created |
| orakel.serviceAccount.name | string | `""` | The name of the service account to use. |
| orakel.volumeMounts | list | `[{"mountPath":"/tmp/k8s-webhook-server/serving-certs","name":"cert","readOnly":true}]` | Additional volumeMounts on the output Deployment definition. |
| orakel.volumes | list | `[{"name":"cert","secret":{"defaultMode":420,"secretName":"webhook-server-cert"}}]` | Additional volumes on the output Deployment definition. |
| orakel.webhookService | object | `{"annotations":{},"labels":{},"port":443,"type":"ClusterIP"}` | The operator webhook service, this is used for the admission webhook. |
| valkey | object | `{"env":[{"name":"ALLOW_EMPTY_PASSWORD","value":"yes"}],"image":{"pullPolicy":"Always","registry":"ghcr.io","repository":"valkey-io/valkey","tag":"8.1.3"},"livenessProbe":{"exec":{"command":["sh","-c","valkey-cli ping | grep PONG"]},"initialDelaySeconds":15,"periodSeconds":20},"podSecurityContext":{"fsGroup":1001,"runAsGroup":1001,"runAsNonRoot":true,"runAsUser":1001,"seccompProfile":{"type":"RuntimeDefault"}},"readinessProbe":{"exec":{"command":["sh","-c","valkey-cli ping | grep PONG"]},"initialDelaySeconds":5,"periodSeconds":10},"resources":{"limits":{"cpu":"600m","memory":"1Gi"},"requests":{"cpu":"300m","memory":"512Mi"}},"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}},"service":{"annotations":{},"labels":{},"port":6379,"type":"ClusterIP"},"storage":{"accessModes":["ReadWriteOnce"],"annotations":{},"enabled":true,"labels":{},"name":"valkey-pvc","size":"1Gi","storageClassName":""}}` | Valkey is used to store the baseline and check results before analyzing them, if it is configured without a persistent volume, the data will be lost on pod restart, which could cause the analysis to fail. |
| valkey.env | list | `[{"name":"ALLOW_EMPTY_PASSWORD","value":"yes"}]` | Environment variables for Valkey |
| valkey.image | object | `{"pullPolicy":"Always","registry":"ghcr.io","repository":"valkey-io/valkey","tag":"8.1.3"}` | Image configuration for Valkey |
| valkey.livenessProbe | object | `{"exec":{"command":["sh","-c","valkey-cli ping | grep PONG"]},"initialDelaySeconds":15,"periodSeconds":20}` | Liveness probe for Valkey, these are used to check if the Valkey container is running and ready to accept requests. |
| valkey.podSecurityContext | object | `{"fsGroup":1001,"runAsGroup":1001,"runAsNonRoot":true,"runAsUser":1001,"seccompProfile":{"type":"RuntimeDefault"}}` | PodSecurityContext for the Valkey pod, this is configured to pass the "restricted" profile of the Pod Security Standards. |
| valkey.readinessProbe | object | `{"exec":{"command":["sh","-c","valkey-cli ping | grep PONG"]},"initialDelaySeconds":5,"periodSeconds":10}` | Readiness probes for Valkey, these are used to check if the Valkey container is running and ready to accept requests. |
| valkey.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}` | SecurityContext for the Valkey container, this is configured to pass the "restricted" profile of the Pod Security Standards. |
| valkey.service | object | `{"annotations":{},"labels":{},"port":6379,"type":"ClusterIP"}` | The valKey service is only used from the operator so it is not exposed outside the cluster. |
| valkey.storage | object | `{"accessModes":["ReadWriteOnce"],"annotations":{},"enabled":true,"labels":{},"name":"valkey-pvc","size":"1Gi","storageClassName":""}` | Storage configuration for Valkey, this is used to store the data of Valkey, if this is disabled an emptyDir is used, which means that the data will be lost if the pod is scheduled to another node |
