// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import copilot "github.com/github/copilot-sdk/go"

// Skill represents a named group of tools and a specialized system message.
type Skill struct {
	Name            string
	Description     string
	ToolNames       []string
	SystemSuffix    string
	ReasoningEffort string
}

// SkillRegistry holds all available built-in skills.
type SkillRegistry struct {
	skills map[string]*Skill
}

// NewSkillRegistry returns a registry pre-loaded with built-in skills.
func NewSkillRegistry() *SkillRegistry {
	r := &SkillRegistry{skills: make(map[string]*Skill)}

	r.Register(&Skill{
		Name:        "diagnostics",
		Description: "Diagnose unhealthy pods, deployments, and workloads",
		ToolNames: []string{
			"get_pod_diagnostics",
			"get_logs",
			"get_events",
			"describe_resource",
			"get_cluster_health",
			"get_resource",
		},
		SystemSuffix: `
Focus: Root-cause analysis and remediation.
Workflow: Always start by fetching diagnostics/description, then events, then logs (with previous=true for crashes).
Prioritize: CrashLoopBackOff > OOMKilled > ImagePullBackOff > Pending (scheduling) > other.
For each issue found, provide a specific fix (kubectl command or YAML patch).`,
	})

	r.Register(&Skill{
		Name:        "security",
		Description: "RBAC auditing, security posture, and policy analysis",
		ToolNames: []string{
			"check_rbac",
			"get_resource",
			"describe_resource",
			"list_resources",
		},
		SystemSuffix: `
Focus: Security posture and RBAC analysis.
Check for: Overly permissive ClusterRoleBindings, wildcard verbs/resources, secrets mounted unnecessarily, containers running as root, missing network policies, service accounts with excessive permissions.
When auditing RBAC: enumerate role bindings, check for privilege escalation paths, verify least-privilege principle.
Flag any security concerns with severity (Critical/High/Medium/Low).`,
	})

	r.Register(&Skill{
		Name:        "optimization",
		Description: "Resource utilization, cost optimization, and scaling",
		ToolNames: []string{
			"get_cluster_health",
			"list_resources",
			"get_resource",
			"describe_resource",
			"get_pod_diagnostics",
		},
		SystemSuffix: `
Focus: Resource efficiency, cost optimization, and scaling recommendations.
Analyze: CPU/memory requests vs limits, over-provisioned pods, under-utilized nodes, missing resource requests.
Recommend: Right-sized resource requests, HPA configurations, PDB settings, node pool sizing.
Compare actual usage patterns with configured limits when data is available.`,
	})

	return r
}

// Register adds a skill to the registry.
func (r *SkillRegistry) Register(s *Skill) {
	r.skills[s.Name] = s
}

// Get returns a skill by name.
func (r *SkillRegistry) Get(name string) (*Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// List returns all registered skill names.
func (r *SkillRegistry) List() []string {
	names := make([]string, 0, len(r.skills))
	for n := range r.skills {
		names = append(names, n)
	}
	return names
}

// All returns all registered skills.
func (r *SkillRegistry) All() map[string]*Skill {
	return r.skills
}

// FilterTools returns only the tools matching the given skill.
// If skillName is empty, returns all tools unfiltered.
func (r *SkillRegistry) FilterTools(skillName string, allTools []copilot.Tool) []copilot.Tool {
	if skillName == "" {
		return allTools
	}
	skill, ok := r.skills[skillName]
	if !ok {
		return allTools
	}

	allowed := make(map[string]bool, len(skill.ToolNames))
	for _, n := range skill.ToolNames {
		allowed[n] = true
	}

	var filtered []copilot.Tool
	for _, t := range allTools {
		if allowed[t.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// SystemMessageSuffix returns the skill-specific system message suffix.
// Returns empty string if skill is not found or name is empty.
func (r *SkillRegistry) SystemMessageSuffix(skillName string) string {
	if skillName == "" {
		return ""
	}
	skill, ok := r.skills[skillName]
	if !ok {
		return ""
	}
	return skill.SystemSuffix
}
