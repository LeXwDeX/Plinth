import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const chatColumn = readFileSync(new URL('./chat-column.tsx', import.meta.url), 'utf8');
const placement = readFileSync(new URL('./asset-list-placement.ts', import.meta.url), 'utf8');
const loader = readFileSync(new URL('./asset-list-card-loader.tsx', import.meta.url), 'utf8');

// #327: the conversation shows each production list as a card under the
// assistant message that proposed it, and any other list at the conversation's
// end. The card reads its own detail and a decision refreshes both the detail
// and the session listing.
describe('chat asset list wiring', () => {
  it('reads the session lists and builds the client from the Schema', () => {
    expect(chatColumn).toMatch(/import \{ useSessionAssetLists \} from '@\/hooks\/use-session-asset-lists';/);
    expect(chatColumn).toContain('const sessionAssetLists = useSessionAssetLists(activeSessionId ?? null);');
    expect(chatColumn).toContain('const assetListsClient = useMemo(() => assetListClient(schema), [schema]);');
  });

  it('places lists by origin message and renders the two containers', () => {
    expect(chatColumn).toMatch(/import \{ assetListPlacement \} from '@\/components\/studio\/asset-list-placement';/);
    expect(chatColumn).toMatch(
      /const assetListCards = useMemo\(\s*\(\) => assetListPlacement\(sessionAssetLists\.lists, displayMessages\.map\(\(message\) => message\.id\)\),\s*\[sessionAssetLists\.lists, displayMessages\],\s*\);/,
    );
    expect(placement).toContain('origin_message_id');
    expect(chatColumn).toContain('data-testid="chat-asset-list-cards"');
    expect(chatColumn).toContain('data-testid="chat-asset-list-loose"');
    expect(chatColumn).toMatch(/assetListCards\.byMessage\.get\(item\.message\.id\)\?\.length/);
    expect(chatColumn).toMatch(/assetListCards\.loose\.length > 0/);
  });

  it('renders the loader in both containers and refreshes the session listing after a decision', () => {
    expect(chatColumn).toMatch(/import \{ AssetListCardLoader \} from '@\/components\/studio\/asset-list-card-loader';/);
    const matches = chatColumn.match(/<AssetListCardLoader/g) ?? [];
    expect(matches).toHaveLength(2);
    expect(chatColumn.match(/variant="chat"/g)?.length).toBe(2);
    expect(chatColumn.match(/onDecided=\{sessionAssetLists\.refresh\}/g)?.length).toBe(2);
  });

  it('has the loader re-read its own detail after a decision', () => {
    expect(loader).toContain("['asset-list-detail', summary.project_id, summary.id]");
    expect(loader).toMatch(/onDecided=\{\(\) => \{\s*void mutate\(\);\s*onDecided\?\.\(\);\s*\}\}/);
  });
});