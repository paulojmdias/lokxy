# lokxy

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/lokxy)](https://artifacthub.io/packages/search?repo=lokxy)

## Usage

[Helm](https://helm.sh) must be installed to use the charts.
Please refer to Helm's [documentation](https://helm.sh/docs/) to get started.

Once Helm is set up properly, add the repo as follows:

```console
helm repo add lokxy https://helm.lokxy.paulodias.xyz
```

You can then run `helm search repo lokxy` to see the charts.

## Version

![Version: 0.0.1](https://img.shields.io/badge/Version-0.0.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.4.0](https://img.shields.io/badge/AppVersion-v0.4.0-informational?style=flat-square)

Lokxy is a powerful log aggregator for Loki

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Paulo Dias | <me@paulodias.xyz> | <https://github.com/paulojmdias> |
| Hélia Barroso |  | <https://github.com/heliapb> |

## Requirements

Kubernetes: `>=1.23.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| config | string | `"server_groups:\n  - name: \"Loki 1\"\n    url: \"http://localhost:3100\"\n    timeout: 30\nlogging:\n  level: \"info\"\n  format: \"json\"\n"` | Raw lokxy.yaml config rendered into the ConfigMap or Secret (depending on configStorageType) |
| configStorageType | string | `"ConfigMap"` | Defines what kind of object stores the configuration, a ConfigMap or a Secret. In order to move sensitive information (such as credentials) from the ConfigMap/Secret to a more secure location (e.g. vault), it is possible to use environment variables in the configuration. Such environment variables can be then stored in a separate Secret and injected via the deployment.extraEnvFrom value. For details about environment injection from a Secret please see [Secrets](https://kubernetes.io/docs/concepts/configuration/secret/#use-case-as-container-environment-variables). |
| deployment.annotations | object | `{}` | Custom deployment annotations |
| deployment.command | list | `["/usr/local/bin/lokxy"]` | Command to run in the container |
| deployment.env | list | `[]` | Environment variables for the container |
| deployment.extraArgs | list | `[]` | Additional CLI arguments passed to the main container |
| deployment.extraEnv | list | `[]` | Common environment variables to add to all pods directly managed by this chart. |
| deployment.extraEnvFrom | list | `[]` | Common source of environment injections to add to all pods directly managed by this chart. For example to inject values from a Secret, use: extraEnvFrom:   - secretRef:       name: mysecret |
| deployment.extraFlags | object | `{}` |  |
| deployment.extraVolumeMounts | list | `[]` | Additional volume mounts for the container |
| deployment.extraVolumes | list | `[]` | Additional volumes to mount |
| deployment.livenessProbe | object | `{"failureThreshold":10,"httpGet":{"path":"/healthy"},"initialDelaySeconds":60,"timeoutSeconds":30}` | liveness probe settings |
| deployment.podAnnotations | object | `{}` | Custom pod annotations |
| deployment.readinessProbe | object | `{"httpGet":{"path":"/ready"}}` | readiness probe settings |
| deployment.replicaCount | int | `2` | Number of Lokxy pods to run |
| deployment.resources | object | `{"limits":{"cpu":1,"memory":"512Mi"},"requests":{"cpu":0.1,"memory":"128Mi"}}` | Kubernetes resource requests and limits |
| deployment.revisionHistoryLimit | int | `10` | Deployment revision history limit |
| global.annotations | object | `{}` | Common labels and annotations for all resources |
| global.labels | object | `{}` | Common labels for all resources |
| horizontalPodAutoscaler | object | `{"annotations":{},"behavior":{},"enabled":false,"labels":{},"maxReplicas":5,"minReplicas":2,"targetCPUUtilizationPercentage":75,"targetMemoryUtilizationPercentage":null}` | Horizontal Pod Autoscaler (HPA) configuration for automatically scaling Lokxy based on resource usage |
| horizontalPodAutoscaler.annotations | object | `{}` | Custom HorizontalPodAutoscaler annotations |
| horizontalPodAutoscaler.behavior | object | `{}` | Advanced scaling behavior configuration for HPA (e.g., scaleUp policies) See: <https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/#configurable-scaling-behavior> |
| horizontalPodAutoscaler.enabled | bool | `false` | Enable HorizontalPodAutoscaler for the Lokxy Deployment |
| horizontalPodAutoscaler.labels | object | `{}` | Custom HorizontalPodAutoscaler labels |
| horizontalPodAutoscaler.maxReplicas | int | `5` | Maximum number of pods to scale up to |
| horizontalPodAutoscaler.minReplicas | int | `2` | Minimum number of pods to scale down to |
| horizontalPodAutoscaler.targetCPUUtilizationPercentage | int | `75` | Target average CPU utilization percentage across pods |
| horizontalPodAutoscaler.targetMemoryUtilizationPercentage | string | `nil` | Target average memory utilization percentage across pods (optional) |
| image | object | `{"pullPolicy":"IfNotPresent","repository":"lokxy/lokxy","tag":"v0.4.0"}` | Docker image configuration |
| ingress | object | `{"annotations":{},"className":"","enabled":false,"hosts":[{"host":"lokxy.local","paths":[{"path":"/","pathType":"ImplementationSpecific"}]}],"labels":{},"tls":[]}` | Ingress configuration for exposing Lokxy externally over HTTP/S |
| ingress.annotations | object | `{}` | Annotations to add to the Ingress resource (e.g., cert-manager, NGINX settings) |
| ingress.className | string | `""` | Ingress class name (e.g., nginx, traefik) |
| ingress.enabled | bool | `false` | Whether to create an Ingress resource for Lokxy |
| ingress.hosts | list | `[{"host":"lokxy.local","paths":[{"path":"/","pathType":"ImplementationSpecific"}]}]` | Host rules for the Ingress resource |
| ingress.labels | object | `{}` | Custom Ingress labels |
| ingress.tls | list | `[]` | TLS configuration for secure HTTPS access Example: tls:   - secretName: lokxy-tls     hosts:       - lokxy.example.com |
| podDisruptionBudget | object | `{"annotations":{},"enabled":true,"labels":{},"maxUnavailable":null,"minAvailable":1}` | PodDisruptionBudget configuration to ensure a minimum number of Lokxy pods are always available during voluntary disruptions |
| podDisruptionBudget.annotations | object | `{}` | Custom podDisruptionBudget annotations |
| podDisruptionBudget.enabled | bool | `true` | Whether to create a PodDisruptionBudget for the Lokxy Deployment |
| podDisruptionBudget.labels | object | `{}` | Custom podDisruptionBudget labels |
| podDisruptionBudget.maxUnavailable | string | `nil` | Maximum number of pods that can be unavailable during a voluntary disruption Set either `maxUnavailable` or `minAvailable`, not both. Example: 1 (absolute value) or "50%" (percentage) |
| podDisruptionBudget.minAvailable | int | `1` | Minimum number of pods that must be available during a voluntary disruption Set either `minAvailable` or `maxUnavailable`, not both. Example: 1 (absolute value) or "50%" (percentage) |
| ports | object | `{"metrics":3101,"service":3100}` | Container ports used by Lokxy |
| route | object | `{"main":{"additionalRules":[],"annotations":{},"apiVersion":"gateway.networking.k8s.io/v1","enabled":false,"filters":[],"hostnames":[],"kind":"HTTPRoute","labels":{},"matches":[{"path":{"type":"PathPrefix","value":"/"}}],"parentRefs":[]}}` | BETA: Configure the gateway routes for the chart here. More routes can be added by adding a dictionary key like the 'main' route. Be aware that this is an early beta of this feature, Being BETA this can/will change in the future without notice, do not use unless you want to take that risk [[ref]](<https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io%2fv1alpha2>) |
| route.main.annotations | object | `{}` | Annotations to add to the route resource (e.g., cert-manager, NGINX settings) |
| route.main.apiVersion | string | `"gateway.networking.k8s.io/v1"` | Set the route apiVersion, e.g. gateway.networking.k8s.io/v1 or gateway.networking.k8s.io/v1alpha2 |
| route.main.enabled | bool | `false` | Enables or disables the route |
| route.main.kind | string | `"HTTPRoute"` | Set the route kind Valid options are GRPCRoute, HTTPRoute, TCPRoute, TLSRoute, UDPRoute |
| route.main.labels | object | `{}` | Custom Route labels |
| service | object | `{"annotations":{},"enabled":true,"type":"ClusterIP"}` | Kubernetes Service configuration for exposing the Lokxy application |
| service.annotations | object | `{}` | Additional annotations to add to the Service metadata |
| service.enabled | bool | `true` | Whether to create a Kubernetes Service for Lokxy |
| service.type | string | `"ClusterIP"` | Kubernetes Service type (e.g., ClusterIP, NodePort, LoadBalancer) |
| verticalPodAutoscaler | object | `{"annotations":{},"enabled":false,"labels":{},"resourcePolicy":{},"updatePolicy":{"updateMode":"Auto"}}` | Vertical Pod Autoscaler (VPA) configuration for resource recommendation and automatic resizing |
| verticalPodAutoscaler.annotations | object | `{}` | Custom VerticalPodAutoscaler annotations |
| verticalPodAutoscaler.enabled | bool | `false` | Enable VerticalPodAutoscaler for the Lokxy Deployment |
| verticalPodAutoscaler.labels | object | `{}` | Custom VerticalPodAutoscaler labels |
| verticalPodAutoscaler.resourcePolicy | object | `{}` | Fine-grained resource policy to exclude or limit certain containers (optional) See: <https://cloud.google.com/kubernetes-engine/docs/concepts/verticalpodautoscaler#resource-policy> |
| verticalPodAutoscaler.updatePolicy | object | `{"updateMode":"Auto"}` | VPA update policy: "Auto", "Initial", or "Off" |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.11.0](https://github.com/norwoodj/helm-docs/releases/v1.11.0)
