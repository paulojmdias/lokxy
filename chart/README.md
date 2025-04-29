# lokxy

![Version: 0.0.1](https://img.shields.io/badge/Version-0.0.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.3.0](https://img.shields.io/badge/AppVersion-v0.3.0-informational?style=flat-square)

Lokxy is a lightweight log aggregator for Grafana Loki.
It acts as a unified ingestion gateway, routing and transforming multiple log formats into a Loki-compatible stream.

This chart bootstraps a Lokxy deployment on a Kubernetes cluster using the Helm package manager.

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| paulojmdias | <me@paulodias.xyz> | <https://github.com/paulojmdias> |

## Requirements

Kubernetes: `>=1.19.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| config | string | `"server_groups:\n  - name: \"Loki 1\"\n    url: \"http://localhost:3100\"\n    timeout: 30\nlogging:\n  level: \"info\"\n  format: \"json\"\n"` | Raw lokxy.yaml config rendered into the ConfigMap |
| deployment.annotations | object | `{}` | Custom deployment annotations |
| deployment.command | list | `["/usr/local/bin/lokxy"]` | Command to run in the container |
| deployment.env | list | `[]` | Environment variables for the container |
| deployment.extraArgs | list | `[]` | Additional CLI arguments passed to the main container |
| deployment.extraFlags | object | `{}` |  |
| deployment.extraVolumeMounts | list | `[]` | Additional volume mounts for the container |
| deployment.extraVolumes | list | `[]` | Additional volumes to mount |
| deployment.podAnnotations | object | `{}` | Custom pod annotations |
| deployment.replicaCount | int | `2` | Number of Lokxy pods to run |
| deployment.resources | object | `{"limits":{"cpu":1,"memory":"512Mi"},"requests":{"cpu":0.1,"memory":"128Mi"}}` | Kubernetes resource requests and limits |
| deployment.revisionHistoryLimit | int | `10` | Deployment revision history limit |
| horizontalPodAutoscaler | object | `{"behavior":{},"enabled":false,"maxReplicas":5,"minReplicas":2,"targetCPUUtilizationPercentage":75,"targetMemoryUtilizationPercentage":null}` | Horizontal Pod Autoscaler (HPA) configuration for automatically scaling Lokxy based on resource usage |
| horizontalPodAutoscaler.behavior | object | `{}` | Advanced scaling behavior configuration for HPA (e.g., scaleUp policies) See: https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/#configurable-scaling-behavior |
| horizontalPodAutoscaler.enabled | bool | `false` | Enable HorizontalPodAutoscaler for the Lokxy Deployment |
| horizontalPodAutoscaler.maxReplicas | int | `5` | Maximum number of pods to scale up to |
| horizontalPodAutoscaler.minReplicas | int | `2` | Minimum number of pods to scale down to |
| horizontalPodAutoscaler.targetCPUUtilizationPercentage | int | `75` | Target average CPU utilization percentage across pods |
| horizontalPodAutoscaler.targetMemoryUtilizationPercentage | string | `nil` | Target average memory utilization percentage across pods (optional) |
| image | object | `{"pullPolicy":"IfNotPresent","repository":"lokxy/lokxy","tag":"v0.3.0"}` | Docker image configuration |
| ingress | object | `{"annotations":{},"enabled":false,"hosts":[{"host":"lokxy.local","paths":[{"path":"/","pathType":"ImplementationSpecific"}]}],"tls":[]}` | Ingress configuration for exposing Lokxy externally over HTTP/S |
| ingress.annotations | object | `{}` | Annotations to add to the Ingress resource (e.g., cert-manager, NGINX settings) |
| ingress.enabled | bool | `false` | Whether to create an Ingress resource for Lokxy |
| ingress.hosts | list | `[{"host":"lokxy.local","paths":[{"path":"/","pathType":"ImplementationSpecific"}]}]` | Host rules for the Ingress resource |
| ingress.tls | list | `[]` | TLS configuration for secure HTTPS access Example: tls:   - secretName: lokxy-tls     hosts:       - lokxy.example.com |
| podDisruptionBudget | object | `{"enabled":true,"maxUnavailable":null,"minAvailable":1}` | PodDisruptionBudget configuration to ensure a minimum number of Lokxy pods are always available during voluntary disruptions |
| podDisruptionBudget.enabled | bool | `true` | Whether to create a PodDisruptionBudget for the Lokxy Deployment |
| podDisruptionBudget.maxUnavailable | string | `nil` | Maximum number of pods that can be unavailable during a voluntary disruption Set either `maxUnavailable` or `minAvailable`, not both. Example: 1 (absolute value) or "50%" (percentage) |
| podDisruptionBudget.minAvailable | int | `1` | Minimum number of pods that must be available during a voluntary disruption Set either `minAvailable` or `maxUnavailable`, not both. Example: 1 (absolute value) or "50%" (percentage) |
| ports | object | `{"metrics":3101,"service":3100}` | Container ports used by Lokxy |
| service | object | `{"annotations":{},"enabled":true,"type":"ClusterIP"}` | Kubernetes Service configuration for exposing the Lokxy application |
| service.annotations | object | `{}` | Additional annotations to add to the Service metadata |
| service.enabled | bool | `true` | Whether to create a Kubernetes Service for Lokxy |
| service.type | string | `"ClusterIP"` | Kubernetes Service type (e.g., ClusterIP, NodePort, LoadBalancer) |
| verticalPodAutoscaler | object | `{"enabled":false,"resourcePolicy":{},"updatePolicy":{"updateMode":"Auto"}}` | Vertical Pod Autoscaler (VPA) configuration for resource recommendation and automatic resizing |
| verticalPodAutoscaler.enabled | bool | `false` | Enable VerticalPodAutoscaler for the Lokxy Deployment |
| verticalPodAutoscaler.resourcePolicy | object | `{}` | Fine-grained resource policy to exclude or limit certain containers (optional) See: https://cloud.google.com/kubernetes-engine/docs/concepts/verticalpodautoscaler#resource-policy |
| verticalPodAutoscaler.updatePolicy | object | `{"updateMode":"Auto"}` | VPA update policy: "Auto", "Initial", or "Off" |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.11.0](https://github.com/norwoodj/helm-docs/releases/v1.11.0)
