package cmd

// workflowTemplates holds the client-side starter manifests `astro workflow
// init --pattern <p>` scaffolds. Each is a complete, parseable §5.4 manifest
// keyed by WorkflowDefinition.pattern_kind (spec 40 §4.1). They are
// illustrative — slugs/agents are placeholders to edit.

const workflowTemplateHeader = `# workflow.toml — Astrolift workflow manifest (spec 40 §5.4)
# Authored with ` + "`astro workflow init`" + `. Edit, then validate:
#   astro workflow validate workflow.toml            # local shape check
#   astro workflow validate workflow.toml --server   # authoritative
# Skill refs use the spec 39 grammar: "name@ver", "./skills/foo",
# "alias/path@ref".
`

const workflowTemplateChained = workflowTemplateHeader + `
# Pattern: chained — stages run in order, each consuming the prior output.

[workflow]
slug        = "feature-dev"
name        = "Feature Dev"
pattern     = "chained"
description = "Implement a change, then gate on human review."

[[stage]]
kind       = "agent_dispatch"
role       = "implementer"
agent      = "my-coder"          # local agent slug; omit for role-only globals
skills     = ["write-tests"]
on_failure = "retry"
timeout    = 600

[[stage]]
kind      = "human_gate"
role      = "reviewer"
prompt    = "Approve the implementation?"
approvers = ["team:reviewers"]
timeout   = 86400
`

const workflowTemplateSingle = workflowTemplateHeader + `
# Pattern: single — one agent, one dispatch.

[workflow]
slug        = "single-task"
name        = "Single Task"
pattern     = "single"
description = "Run one agent to completion."

[[stage]]
kind       = "agent_dispatch"
role       = "worker"
agent      = "my-agent"
skills     = []
on_failure = "fail"
timeout    = 600
`

const workflowTemplateReviewLoop = workflowTemplateHeader + `
# Pattern: review_loop — author drafts; a human gate sends it back or approves.

[workflow]
slug        = "review-loop"
name        = "Review Loop"
pattern     = "review_loop"
description = "Author produces work; reviewer approves or loops for revision."

[[stage]]
kind       = "agent_dispatch"
role       = "author"
agent      = "my-coder"
on_failure = "retry"
timeout    = 600

[[stage]]
kind      = "human_gate"
role      = "reviewer"
prompt    = "Approve, or send back for revision?"
approvers = ["team:reviewers"]
timeout   = 86400
`

const workflowTemplateFanOut = workflowTemplateHeader + `
# Pattern: fan_out — split work across N parallel workers, then aggregate.

[workflow]
slug        = "fan-out"
name        = "Fan Out"
pattern     = "fan_out"
description = "Dispatch N parallel workers and aggregate their results."

[[stage]]
kind       = "agent_dispatch"
role       = "worker"
agent      = "my-worker"
on_failure = "retry"
timeout    = 600
fan_out    = 3                   # static N; or "dynamic" to derive from prior output

[[stage]]
kind    = "aggregation"
role    = "aggregator"
timeout = 300
`

const workflowTemplateSupervisorWorker = workflowTemplateHeader + `
# Pattern: supervisor_worker — a supervisor plans and dispatches dynamic workers.

[workflow]
slug        = "supervisor-worker"
name        = "Supervisor / Worker"
pattern     = "supervisor_worker"
description = "A supervisor decomposes the task and fans out to workers."

[[stage]]
kind       = "agent_dispatch"
role       = "supervisor"
agent      = "my-supervisor"
on_failure = "retry"
timeout    = 600

[[stage]]
kind       = "agent_dispatch"
role       = "worker"
agent      = "my-worker"
on_failure = "retry"
timeout    = 600
fan_out    = "dynamic"           # derive the worker count from the supervisor's output
`

var workflowTemplates = map[string]string{
	"chained":           workflowTemplateChained,
	"single":            workflowTemplateSingle,
	"review_loop":       workflowTemplateReviewLoop,
	"fan_out":           workflowTemplateFanOut,
	"supervisor_worker": workflowTemplateSupervisorWorker,
}
