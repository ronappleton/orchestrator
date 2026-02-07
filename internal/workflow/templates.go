package workflow

var BuiltinTemplates = []Workflow{
	{
		ID:          "tpl_intake_enrich_decide_act",
		Name:        "intake_enrich_decide_act",
		Description: "Generic intake -> enrich -> decide -> act workflow",
		Steps: []Step{
			{Name: "intake", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/intake", "body": map[string]any{}}},
			{Name: "enrich", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/enrich", "body": map[string]any{}}},
			{Name: "decide", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/decide", "body": map[string]any{}}},
			{Name: "act", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/act", "body": map[string]any{}}},
		},
	},
	{
		ID:          "tpl_detect_classify_route",
		Name:        "detect_classify_route",
		Description: "Generic detect -> classify -> route pattern",
		Steps: []Step{
			{Name: "detect", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/detect", "body": map[string]any{}}},
			{Name: "classify", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/classify", "body": map[string]any{}}},
			{Name: "route", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/route", "body": map[string]any{}}},
		},
	},
	{
		ID:          "tpl_review_approve_execute",
		Name:        "review_approve_execute",
		Description: "Generic review -> approval gate -> execute",
		Steps: []Step{
			{Name: "review", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/review", "body": map[string]any{}}},
			{Name: "approval", Action: "condition", Input: map[string]any{"key": "approved", "equals": true, "on_false": "stop"}, RequiresApproval: true},
			{Name: "execute", Action: "http", Input: map[string]any{"method": "POST", "url": "https://example.com/execute", "body": map[string]any{}}},
		},
	},
	{
		ID:          "tpl_workspace_notify",
		Name:        "workspace_notify",
		Description: "Workspace check followed by notification",
		Steps: []Step{
			{Name: "workspace_check", Action: "workspace.check", Input: map[string]any{"workspace_id": "ws_example"}},
			{Name: "notify_policy", Action: "policy.check", Input: map[string]any{"action": "notify", "channel": "email"}},
			{Name: "notify", Action: "notify", Input: map[string]any{"channel": "email", "to": "user@example.com", "subject": "Workspace ready", "body": "Workspace check passed."}},
		},
	},
}
