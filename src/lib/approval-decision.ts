/** The part of the product Schema that names the approval decision member. */
export interface ApprovalDecisionSchema {
  chat: { api: { fields: { decision?: string } } };
}

/**
 * #103 / #120: the request member that carries an approval decision is Schema
 * data (`chat.api.fields.decision`), and the server reads the same declaration.
 * Every client of the task-grant and production-plan decision routes builds its
 * body here, so renaming the member in the Catalog keeps both ends in step. A
 * Schema without the member cannot be decided safely: refuse before sending an
 * empty key the server would not understand.
 */
export function approvalDecisionBody(schema: ApprovalDecisionSchema, approve: boolean): Record<string, string> {
  const member = schema.chat.api.fields.decision;
  if (!member) throw new Error('chat.api.fields.decision is required to decide an approval');
  return { [member]: approve ? 'approve' : 'deny' };
}
