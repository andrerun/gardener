# Scaling Gardener Storage Volumes
## _Design Specification_
### Introduction

TODO: Use those to build a description of the initiative. The big description goes here, the one in the project definition will only be as long as necessary to reflect goals and scope.

"Same size fits ALL" strategy is not optimal for the persistence volume sizes of Gardener observability workloads.
This initiative aims at providing a more flexible and dynamic resizing of the PVs on infrastructures where the
corresponding Storage Classes support the resize operation. The resizing is only in increasing dimensions. Shrinking PV usually is not directly supported by the infrastructure providers.

Background:
Logs are vital for understanding and troubleshooting Gardener clusters, and yet we operate under tight resource boundaries. For example the PV in the logging stack are 30GB in size by default, for ALL clusters. To prevent reaching this limit and hence bringing down the logging stack, there is a curator side car removing older logs before exhausting the entire volumes. In some cases this strategy allows retention of logs only for 2, 3 days in very busy or large clusters.

In the very same time, while we have a space starvation in some clusters, in others we observe quite the opposite. There are 5 to 10% only utilisation of the disk spaces.

This initiative introduces a Gardener controller that can optionally resize PVs of the gardener application using PVs is storage classes allow it. In such way we can start small by default and then have the ability to grow the needed space up to a given boundary.


The initial targets for PVC autoscaler are the observability components such as Vali, Prometheus and AlertManager, but once this component matures enough it should be possible to use it elsewhere.

In order to reduce the amount of manual work when resizing PVCs once they reach a certain threshold and also to potentially reduce the initial size of PVCs, we need to implement dynamic resize support for persistent volumes.

Note, that shrinking a PVC is not supported. A PVC size can only be increased, and not decreased.

[pvc-autoscaler] is a Kubernetes controller which periodically monitors persistent volumes and resizes them, if the
available space or number of inodes drops below a certain threshold.

Storage Classes MUST have allowVolumeExpansion: true. See Volume expansion documentation for more details.


### PVC Autoscaler - Runtime Structure
The overall runtime structure of the component is outlined in _Fig.1_:

![01-runtime_structure.png](resources%2F01-runtime_structure.png)
_Fig.1: Runtime structure_

The existing [pvc-autoscaler] application is deployed as a seed system component. It runs as a deployment in the seed's
`garden` namespace. Its primary driving signal are the PVC metrics from the seed's cache Prometheus instance, which it
periodically examines, and if capacity is found to be near exhaustion, `pvc-autoscaler` takes action by updating the
PVC's storage request.

In this initial iteration, `pvc-autoscaler` scales two categories of `vali` and `prometheus` volumes: those in shoot
namespaces, and those in the seed's `garden` namespace. 

`pvc-autoscaler` publishes Prometheus metrics to anonymous scrapers via a `/metrics` HTTP endpoint. A `ServiceMonitor`
object is created in the `garden` namespace, and drives the seed's `prometheus-operator` to configure a scrape on that
endpoint by `prometheus-seed`.

`pvc-autoscaler` runs in active/passive replication mode, based on the standard leader election mechanism, supplied by
the K8s controller runtime library. Only the active replica is shown in _Fig.1_.

### PVC Autoscaler - Deployer Structure
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

#### Initial volume size and MaxAllowed

| Component type | Component            | Old size | Initial size | Max size |
|----------------|----------------------|----------|--------------|----------|
| Shoot          | prometheus           | 20Gi     | 1Gi          | 40Gi     |
| Shoot          | vali                 | 30Gi     | 1Gi          | 60Gi     |
| Seed           | prometheus-aggregate | 20Gi     | 2Gi          | 40Gi     |
| Seed           | prometheus-cache     | 10Gi     | 2Gi          | 20Gi     |
| Seed           | prometheus-seed      | 100Gi    | 5Gi          | 200Gi    |
| Seed           | vali                 | 100Gi    | 5Gi          | 200Gi    | # TODO!!!!!!!!!!!!!!! Not implemented

### Fugure Enhancements
#### Metrics authorization
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
