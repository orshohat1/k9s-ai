# Optimization Skill

> Resource efficiency, cost optimization, and scaling recommendations for Kubernetes workloads.

Use these playbooks to identify waste, right-size workloads, and recommend scaling strategies.

---

## Resource Right-Sizing

1. `list_resources` for deployments — get all workloads
2. For each workload, check:
   - Are requests set? (if not, scheduling is unpredictable)
   - Are limits set? (if not, pods can consume unbounded resources)
   - Is `requests.cpu` much lower than `limits.cpu`? (burst risk)
   - Is `requests.memory` close to `limits.memory`? (good practice)
3. Look for containers with very high limits but low actual usage
4. Suggest QoS class optimization:
   - **Guaranteed** (requests == limits) for critical workloads
   - **Burstable** for most workloads
   - **BestEffort** only for batch/disposable jobs

---

## Scaling Recommendations

1. Check current replica count vs pod resource usage
2. Look for HPA (HorizontalPodAutoscaler) — is one configured?
3. If no HPA, recommend one based on CPU/memory patterns
4. Check for PDB (PodDisruptionBudget) — important for availability
5. For StatefulSets, check if volumeClaimTemplates are appropriately sized

---

## Cost Optimization

1. Identify over-provisioned workloads (high requests, low actual usage)
2. Find idle deployments (scale-to-zero candidates)
3. Check for unused PVCs consuming storage
4. Look for completed Jobs that haven't been cleaned up
5. Identify pods spread across too many nodes (consolidation opportunity)
