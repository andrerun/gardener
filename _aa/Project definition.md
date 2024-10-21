# Scaling Gardener Storage Volumes
## _Project Definition_
___
## Overview
This initiative adds Persistent Volume Autoscaling capability to Gardener. The first phase is limited to scaling
observability components - `prometheus` and `vali` - in two specific application cases:
- as seed system components in seed clusters' `garden` namespace
- as shoot control plane components in shoot namespaces
## Scope
#### Goals:
- Support upper limits for monitoring and logging PVCs in both garden and shoots namespaces
- Ensure minimal interruption of the applications during PVCs resize
- Implement the extension according the Gardener guidelines
- Include extension monitoring
- Deliver operational documentation for Gardener Operators:
  - Provide extension configuration documentation
  - Provide extension operational documentation
- Ensure non disruptive enablement of the Gardener extension
#### Non-goals:
- Scaling components other than `prometheus` and `vali`


Functional scope

pvc-autoscaler component in gardener to manage the PVCs of the
With the development of pvc-autoscaler 12 we have the technical means to increase the retention period for the observability data and maintain operational costs under control in the same time.
