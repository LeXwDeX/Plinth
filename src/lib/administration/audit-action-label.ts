/**
 * Audit action keys such as `download_asset` are display-localized by the
 * Product Schema `audit_actions` list (zh and en come with the locale). Only a
 * key the Schema does not know falls back to the raw value.
 */
export function auditActionLabel(
  labels: Readonly<Record<string, string>>,
  action: string,
): string {
  return labels[action] || action;
}

export function auditActionLabels(
  actions: ReadonlyArray<{ value: string; label: string }>,
): Record<string, string> {
  return Object.fromEntries(actions.map((item) => [item.value, item.label]));
}
