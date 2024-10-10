# Gardener Storage Volume Autoscaling<br/>_phase 1: observability volumes_
## _Design Specification_

---
## Introduction
A "one size fits all" strategy is a poor fit for the persistence volumes of Gardener observability workloads.
The initiative outlined in this document aims to provide flexible, dynamic PV resizing where the underlying 
infrastructure supports it.

#### Background:
Logs are vital for understanding and troubleshooting Gardener clusters. Yet we operate under tight resource constraints
and logging stack PVs default to a static size of 30GB, for ALL shoots. To prevent reaching this limit and bringing down
the logging stack, there is a curator sidecar which periodically removes older logs. As a result, in some very large or
busy clusters, this strategy allows retention of logs for only for 2-3 days.

On the other hand, while some clusters experience storage starvation, others operate with just 5-10% of their observability
volumes utilised.

The present initiative is focused on observability storage volumes, and aims to:
- Optimize volume utilisation
- Ensure that high-throughput clusters receive sufficient observability storage space

The goals are achieved by the following means:
- A Gardener controller is introduced to auto-scale PVs (expand only)
- Observability volumes are selectively marked for action by said controller
- Initial volume size is reduced
- Where the storage infrastructure does not support volume resizing, the preexisting, static-size volume sizing strategy is employed

The current initiative is limited to scaling the observability components - _Vali_, _Prometheus_, and _AlertManager_. However,
the long term vision is, once the PV autoscaling implementation matures enough, to also use it to offer PV auto-scaling for customer volumes.

### Goals
- General ability to autoscale PVs
  - Ability is conditional on PVC's storage class supporting resizing
  - Autoscaling is inactive by default
  - Ability to selectively activate autoscaling per PVC
  - Ability to trigger scaling based on relative volume utilisation ratio (used bytes/capacity bytes). Trigger threshold
    ratio configurable via code.
  - Ability to configure absolute upper scaling limit per PVC
  - Autoscaling causes minimal interruption to scaled workloads
- Autoscaling ability activated for observability volumes (`alertmanager`, `prometheus`, and `vali`) in seeds'
  `garden` namespace, and in shoot namespaces.
- Absolute upper scaling limit configured on each observability volume.
- Reduce the initial size of a volume, when autoscaling is activated for that volume, if such reduction is aligned with
  general Gardener goals (cost, reliability).
- Implementation artefacts, directly created to satisfy the requirements of this document, follow Gardener guidelines.
- Some metrics, which represent an overview of the PVC autoscaling state per seed, available through one or more of the seed
  system's Prometheus instances.
- Provide operational documentation for Gardener Operators
  - Provide extension configuration documentation
  - Provide extension operational documentation
- Ability to enable/disable the feature per seed, via feature gate

### Non-Goals
- Ability to activate autoscaling on a group of PVCs, other than specifying each PVC individually. Activate by selector
  or namespace is not required.
- Ability to selectively deactivate autoscaling, e.g. by PVC selector or namespacen
- Ability to scale a PVC if the storage class does not support resizing.
- Ability to deactivate autoscaling for any of the observability volumes specified in the 'Goals' section.

<mark>TBD</mark>

### Notes:
- Shrinking a PVC is not supported. A limitation imposed by the underlying infrastructure, PVC size can only be
  increased, and not decreased.
- The newly introduced auto-scaling mode is predicated on the underlying infrastructure supporting volume resizing.
  This ability to is expressed through the K8s StorageClass type. The respective StorageClass must have
  `allowVolumeExpansion: true`. See Volume expansion documentation for more details.

## PVC Autoscaler - Runtime Structure
The overall runtime structure of the component is outlined in _Fig.1_:

![01-runtime_structure.png](resources%2F01-runtime_structure.png)

_Fig.1: Runtime structure_

For the role of a storage scaling controller, the existing, recently implemented [pvc-autoscaler] is used. It is
integrated as a seed system component, and runs as a deployment in the seed's `garden` namespace.
Its primary driving signal is the PVC metrics from the seed's cache Prometheus instance, which it
periodically examines, and if capacity is found to be near exhaustion, `pvc-autoscaler` takes action by updating the
PVC's storage request.

In this initial iteration, `pvc-autoscaler` scales two categories of `vali`, `prometheus`, and `alertmanager` volumes:
those in shoot namespaces, and those in the seed's `garden` namespace. 

`pvc-autoscaler` publishes Prometheus metrics to anonymous scrapers via a `/metrics` HTTP endpoint. A `ServiceMonitor`
object is created in the `garden` namespace, and drives the seed's `prometheus-operator` to configure a scrape on that
endpoint by `prometheus-seed`.

`pvc-autoscaler` runs in active/passive replication mode, based on the standard leader election mechanism, supplied by
the K8s controller runtime library. Only the active replica is shown in _Fig.1_.

## PVC Autoscaler - Deployer Structure
The `pvc-autoscaler` application and its supporting artefacts are deployed by `gardener`, as part of the seed
reconciliation flow. 

The `vali` and `prometheus` deployers, part of `gardenlet`'s shoot reconciliation flow, ensure that the PVC template section in
the respective StatefulSet objects contains the annotations, necessary to instruct `pvc-autuscaler` how to act on the
PVCs, created from those templates.

That `gardenlet` action mechanism, based on PVC templates, only affects PVCs upon creation. Therefore, it has no effect
on existing shoots, and on the observability PVCs of existing seed clusters. To enable PVC autoscaling for existing
shoots, those PVCs will be directly annotated by the operator. Such direct intervention allows gradual transition,
where autoscaling can initially be enabled only for a few PVCs for validation purposes.
Similarly, to enable PVC autoscaling on existing seed clusters' `garden` PVCs, those will be directly annotated at
operators' discretion. 

![02-operator_structure.png](resources%2F02-operator_structure.png)
_Fig.2: Operator structure_

VPA is used to scale `pvc-autoscaler`. For the future security enhancement scenario, where `kube-rbac-proxy` is used,
that container is expected to be excluded from autoscaling, and to run with fixed resource requests. Pod evictions
driven by that lean container are unjustified, as they would only achieve minimal resource savings, and disrupt
the main container.

The feature is deployed behind a dedicated feature gate. When disabled, `pvc-autoscaler` is removed, and related
annotations are removed from the shoot control plane observability StatefulSets, and the seed observability StatefulSets.  

### Initial volume size and MaxAllowed

| Component type | Component            | Old size | Initial size | Max size |
|----------------|----------------------|----------|--------------|----------|
| Shoot          | prometheus           | 20Gi     | 1Gi          | 40Gi     |
| Shoot          | vali                 | 30Gi     | 1Gi          | 60Gi     |
| Seed           | prometheus-aggregate | 20Gi     | 2Gi          | 40Gi     |
| Seed           | prometheus-cache     | 10Gi     | 2Gi          | 20Gi     |
| Seed           | prometheus-seed      | 100Gi    | 5Gi          | 200Gi    |
| Seed           | vali                 | 100Gi    | 5Gi          | 200Gi    | # TODO!!!!!!!!!!!!!!! Not implemented

## Future Enhancements
### Metrics authorization
`pvc-autoscaler` supports access control over its `/metrics` endpoint. This feature will not be utilised by the first
implementation round. It is envisioned as a future enhancement. In that
operation mode, the primary `pvc-autoscaler` publishes unauthorized, plain text metrics only on the loopback interface.
Peer pods are configured to look for `/metrics` at a different port, which is served by a secondary container, running
the [kube-rbac-proxy] application. It serves HTTPS and authenticates and authorizes each request against the runtime
cluster via the [TokenReview] and [SubjectAccessReview] K8s APIs, before forwarding it to the internal, insecure
loopback port.

![03-runtime_structure_secure_metrics.png](resources%2F03-runtime_structure_secure_metrics.png)
_Fig.3: Runtime structure with secure metrics_

[pvc-autoscaler]: https://github.com/gardener/pvc-autoscaler
[kube-rbac-proxy]: https://github.com/brancz/kube-rbac-proxy 
[TokenReview]: https://kubernetes.io/docs/reference/kubernetes-api/authentication-resources/token-review-v1/
[SubjectAccessReview]: https://kubernetes.io/docs/reference/kubernetes-api/authorization-resources/subject-access-review-v1/
