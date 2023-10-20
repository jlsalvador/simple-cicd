package v1alpha1

import "maps"

func GetJobLabels(
	fromJobNamespace string,
	fromJobName string,
	fromWorkflowNamespace string,
	fromWorkflowName string,
	fromWorkflowWebhookNamespace string,
	fromWorkflowWebhookName string,
	fromWorkflowWebhookRequestNamespace string,
	fromWorkflowWebhookRequestName string,
) map[string]string {
	labels := map[string]string{
		LabelWorkflowWebhookRequestNamespace: fromWorkflowWebhookRequestNamespace,
		LabelWorkflowWebhookRequestName:      fromWorkflowWebhookRequestName,
		LabelWorkflowWebhookNamespace:        fromWorkflowWebhookNamespace,
		LabelWorkflowWebhookName:             fromWorkflowWebhookName,
		LabelWorkFlowNamespace:               fromWorkflowNamespace,
		LabelWorkFlowName:                    fromWorkflowName,
		LabelJobNamespace:                    fromJobNamespace,
		LabelJobName:                         fromJobName,
	}
	maps.Copy(labels, getBaseLabels())
	return labels
}
