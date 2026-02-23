package tools

// This file registers noop implementations for tools marked missing in tools-missing.md.

func init() {
	toolNames := []string{
		"apply_patch",
		"search_files",
		"move_file",
		"delete_file",
		"process_spawn",
		"process_list",
		"process_kill",
		"process_read",
		"extract_links",
		"download_file",

		"memory_search",
		"memory_read",
		"memory_list",
		"memory_delete",

		"kg_search",
		"kg_add_node",
		"kg_add_edge",
		"kg_get_node",
		"kg_delete_node",

		"sessions_list",
		"sessions_history",
		"sessions_send",
		"sessions_status",
		"agents_list",

		"message_send",
		"notify",

		"image_analyze",
		"screenshot",
		"pdf_read",
		"audio_transcribe",

		"cron_create",
		"cron_list",
		"cron_delete",
		"cron_run_now",

		"git_status",
		"git_diff",
		"git_commit",
		"git_log",
		"git_branch",

		"agent_status",
		"cost_report",
		"tools_list",
		"config_read",
		"loop_guard",

		"json_query",
		"csv_read",
		"template_render",
	}

	for _, n := range toolNames {
		// ignore registration errors during init to allow tests to re-register
		_ = RegisterTool(n, NoopTool(n))
	}
}
