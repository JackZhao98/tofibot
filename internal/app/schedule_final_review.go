package app

// A receipt is still mandatory for scheduled success. This reminder offers
// one in-history repair opportunity; it never confirms the result itself or
// reruns the original task. The agent enforces the single-repair budget.
const scheduleFinalReminder = `This scheduled execution has no successful completion receipt yet. Review the user's requested deliverable, fields, and categories against the actual tool results already in this conversation before ending.
If a requested item remains unresolved, continue the remaining authorized work with relevant available and permitted tools while capacity remains. Give partial results only when a concrete blocker prevents completion. If the requested result is supported by the evidence, call complete_scheduled_task with that result and its material sources, then return the same concise answer. Do not describe a category, source, or action as searched, checked, completed, unavailable, or verified unless the actual results support that statement. A final answer format requested by the user applies to the final answer, not to the required internal completion call.
If evidence is still missing after a source failure, consider another relevant, available and permitted capability, including browser access when applicable, before declaring the task blocked. Do not repeat unchanged failed calls, invent sources, or infer success from an attempted action. Do not replay actions with uncertain outcomes; inspect their state first. Respect the user's chosen source, scope and permissions.
If no permitted method can complete the task, explain the concrete blocker without calling complete_scheduled_task. This is a single final review, not permission to broaden the task.`

func scheduledFinalReview(kind string, confirmed func() (bool, error)) func(string) (string, error) {
	if kind != runKindSchedule {
		return nil
	}
	return func(_ string) (string, error) {
		ok, err := confirmed()
		if err != nil || ok {
			return "", err
		}
		return scheduleFinalReminder, nil
	}
}
