import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// #291: a streamed turn must redraw the streaming message, not the whole conversation, version strip and studio.
// Measured with scripts/measure-image-heavy-workspace.mjs; these checks keep the wiring that made the difference.
const source = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');
const shell = source('./studio-shell.tsx');
const column = source('./chat-column.tsx');
const message = source('../chat/chat-message.tsx');
const strip = source('./version-strip.tsx');
const versions = source('../../hooks/use-session-versions.ts');

describe('streamed turn render isolation (#291)', () => {
  it('keeps the studio-wide task hooks out of the shell, so a task update does not redraw every region', () => {
    const shellBody = shell.slice(shell.indexOf('export function StudioShell'), shell.indexOf('function StudioEffects'));
    expect(shellBody).not.toMatch(/use(TaskTracker|TaskCompletionNotice|StudioShortcuts)\(\)/);
    expect(shellBody).toContain('<StudioEffects />');
    expect(shell).toMatch(/function StudioEffects\(\) \{\s*useTaskTracker\(\);\s*useTaskCompletionNotice\(\);\s*useStudioShortcuts\(\);\s*return null;/);
  });

  it('memoizes each message and subscribes it to its own tasks only', () => {
    expect(message).toContain('export const ChatMessage = memo(function ChatMessage(');
    expect(message).toContain('useWorkspaceStore(useShallow((state) => state.trackedTasks.filter((task) => taskIds.has(task.taskId))))');
    expect(message).not.toContain('useWorkspaceStore((state) => state.trackedTasks)');
  });

  it('passes every message the same callbacks and data from one chunk to the next', () => {
    const list = column.slice(column.indexOf('<ChatMessage'), column.indexOf('/>', column.indexOf('<ChatMessage')));
    expect(list).toContain('onImageClick={openImageDetail}');
    expect(list).toContain('onPick={pickCandidate}');
    expect(list).toContain('onDelete={item.message.isPending ? undefined : deleteFromList}');
    expect(list).toContain('handleReferences={sessionHandleReferences}');
    // No callback is created inline for the list.
    expect(list).not.toMatch(/=\{(async )?\(/);
    // The click handler reads the messages through a ref instead of depending on them.
    expect(column).toContain('const source = displayMessagesRef.current.find((item) => item.id === messageId);');
    expect(column).toContain('return previous && sameData(previous, next) ? previous : next;');
    // The optimistic user message is one object for the whole turn.
    expect(column).toMatch(/const optimisticUser = useMemo\([\s\S]{0,300}\[schema, streamUserMessage\]\)/);
  });

  it('memoizes version tiles and shares unchanged tiles between task updates', () => {
    expect(strip).toContain('const TileButton = memo(function TileButton(');
    expect(strip).toContain('const Connector = memo(function Connector(');
    expect(strip).toContain('const open = useCallback((tile: VersionTile) => openLatest.current(tile), []);');
    expect(versions).toContain('const tiles = keepUnchanged(previousTiles.current, built.tiles, (tile) => tile.key);');
  });
});
