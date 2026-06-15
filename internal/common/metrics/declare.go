package metrics

var workflowOutcomeCounters = []string{
	WorkflowCompletedCounter,
	WorkflowCanceledCounter,
	WorkflowFailedCounter,
	WorkflowContinueAsNewCounter,
}

// DeclareRegisteredWorkflowOutcomeCounters initializes workflow outcome counters to
// zero for each provided workflow type so they appear in metric scrapes before the
// first corresponding event.
func DeclareRegisteredWorkflowOutcomeCounters(handler Handler, workflowTypes []string) {
	if handler == NopHandler || len(workflowTypes) == 0 {
		return
	}
	for _, wfType := range workflowTypes {
		tagged := handler.WithTags(WorkflowTags(wfType))
		for _, counter := range workflowOutcomeCounters {
			tagged.Counter(counter).Inc(0)
		}
	}
}
