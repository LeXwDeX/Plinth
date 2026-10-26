  def nonnegative_integer: type == "number" and . >= 0 and floor == .;
  def complete_disposition:
    ((.archived_count // 0) <= .imported_count) and
    (if .domain == "task_outputs" then .imported_count <= .loaded_count
     elif .domain == "task_cache" or .domain == "task_input_snapshot" then
       .loaded_count == .imported_count and .skipped_count == 0
     else .loaded_count == (.imported_count + .skipped_count) end);
  .format == "art-ai-etl-completeness-manifest-v1" and
  .overall.conclusion == "pass" and
  .overall.failure_count == 0 and
  (.domains | type == "array") and
  ([.domains[].domain] | sort) ==
    (["users", "asset_registry", "usage_ledger", "balance_snapshots",
      "chat_sessions", "chat_messages", "task_cache", "task_input_snapshot",
      "audit_logs", "canvas_projects", "canvas_nodes", "generation_tasks",
      "task_outputs"] | sort) and
  (.domains | all(([.source_count, .loaded_count, .imported_count,
    .target_count, .skipped_count] | all(nonnegative_integer)) and
    ((has("archived_count") | not) or (.archived_count | nonnegative_integer)) and
    .status == "pass" and .read_gap == 0 and
    .unexplained_gap == 0 and .source_count == .loaded_count and
    .imported_count == .target_count and complete_disposition)) and
  .credits.equal == true and
  ([.credits.source_sum, .credits.loaded_sum, .credits.target_sum] | all(nonnegative_integer)) and
  .credits.source_sum == .credits.loaded_sum and
  .credits.source_sum == .credits.target_sum and
  .source_loss.unacknowledged == 0 and
  .cas.sha256_mismatch == 0
