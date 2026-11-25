import { describe, expect, it } from 'vitest';

import {
  boardMentionCandidate, buildMentionCandidates, filterMentions, mentionCandidates, mentionIdentity, mentionSections, withBoardMention,
  type MentionSources,
} from './mentions';

// #560: the `@` list in two sections, the pictures on the canvas first, then the rest of the session.
const history = mentionCandidates([
  { attachments: [{ handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' }, { handle: '@model1', kind: 'model', taskId: 't3' }] },
  { attachments: [{ handle: '@img2', kind: 'image', taskId: 't2', assetId: 'a2' }, { handle: '@img4', kind: 'image', assetId: 'in4', url: '/api/v1/assets/in4' }] },
]);

const empty: MentionSources = { history: [], uploads: [], versions: [], layers: [] };
const groups = (list: readonly { handle: string; group?: string }[]) => list.map((item) => `${item.group}:${item.handle}`);

describe('mention groups (#560)', () => {
  it('lists the pictures a visible layer shows under the canvas, everything else under the library', () => {
    const list = buildMentionCandidates({
      ...empty,
      history,
      versions: [{ name: '2D v5', taskId: 't5', assetId: 'a5', url: '/api/v1/tasks/t5/assets/a5' }],
      layers: [{ name: '图层 1', url: '/api/v1/tasks/t1/assets/a1', taskId: 't1' }, { name: '图层 2', url: '/api/v1/tasks/t6/assets/a6', taskId: 't6' }],
    });
    // @img1 is on the canvas (图层 1 shows it); @img2 was taken off, the version never placed, the model is never on the canvas.
    expect(groups(list)).toEqual(['canvas:@img1', 'canvas:@图层2', 'library:@img2', 'library:@img4', 'library:@model1', 'library:@2Dv5']);
  });

  it('keeps a merged picture on the canvas: its sources record the layer next to the history', () => {
    const list = buildMentionCandidates({ ...empty, history, layers: [{ name: '图层 1', url: '/api/v1/tasks/t2/assets/a2?preview=1', taskId: 't2' }] });
    const merged = list.find((item) => item.handle === '@img2');
    expect(merged?.sources).toEqual(['history', 'canvas']);
    expect(merged?.group).toBe('canvas');
    expect(merged?.aliases).toEqual(['图层 1']);
    expect(list[0]).toBe(merged);
  });

  it('merges a layer of an uploaded picture, which only carries its address, with the chat handle of that upload', () => {
    // The layer has no asset ID; the attachment names the input asset by ID. Both are asset in4.
    expect(mentionIdentity({ url: '/api/v1/assets/in4' })).toBe(mentionIdentity({ assetId: 'in4' }));
    expect(mentionIdentity({ url: '/api/assets/in4?preview=1' })).toBe('asset:/in4');
    expect(mentionIdentity({ url: '/api/v1/tasks/t1/assets/a1' })).toBe('asset:t1/a1');
    const list = buildMentionCandidates({ ...empty, history, layers: [{ name: '图层 1', url: '/api/v1/assets/in4' }] });
    expect(list.filter((item) => item.url === '/api/v1/assets/in4').map((item) => item.handle)).toEqual(['@img4']);
    expect(list[0]).toMatchObject({ handle: '@img4', group: 'canvas', sources: ['history', 'canvas'] });
  });

  it('keeps a hidden layer nameable but lists it in the library', () => {
    const list = buildMentionCandidates({
      ...empty,
      layers: [
        { name: '图层 1', url: '/api/v1/tasks/t7/assets/a7', taskId: 't7' },
        { name: '图层 2', url: '/api/v1/tasks/t8/assets/a8', taskId: 't8', hidden: true },
      ],
    });
    expect(groups(list)).toEqual(['canvas:@图层1', 'library:@图层2']);
    expect(filterMentions(list, '图层2').map((item) => item.handle)).toEqual(['@图层2']);
  });

  it('puts a hidden layer of a picture a visible layer also shows on the canvas', () => {
    const list = buildMentionCandidates({
      ...empty,
      history,
      layers: [
        { name: '图层 1', url: '/api/v1/tasks/t1/assets/a1', taskId: 't1' },
        { name: '图层 2', url: '/api/v1/tasks/t1/assets/a1', taskId: 't1', hidden: true },
        { name: '图层 3', url: '/api/v1/tasks/t2/assets/a2', taskId: 't2', hidden: true },
      ],
    });
    expect(groups(list).slice(0, 1)).toEqual(['canvas:@img1']);
    expect(list.find((item) => item.handle === '@img2')?.group).toBe('library');
  });

  it('lists every 3D model in the library', () => {
    const list = buildMentionCandidates({ ...empty, history });
    expect(list.filter((item) => item.kind === 'model').map((item) => item.group)).toEqual(['library']);
  });

  it('never renumbers a handle: grouping only moves entries', () => {
    const flat = buildMentionCandidates({ ...empty, history }).map((item) => item.handle).sort();
    const grouped = buildMentionCandidates({ ...empty, history, layers: [{ name: '图层 1', url: '/api/v1/tasks/t2/assets/a2', taskId: 't2' }] })
      .map((item) => item.handle).sort();
    expect(grouped).toEqual(flat);
    expect(grouped).toEqual(['@img1', '@img2', '@img4', '@model1']);
  });

  it('keeps the canvas entry first, at the head of the canvas section', () => {
    const list = withBoardMention(
      buildMentionCandidates({ ...empty, history, layers: [{ name: '图层 1', url: '/api/v1/tasks/t2/assets/a2', taskId: 't2' }] }),
      boardMentionCandidate('画布'),
    );
    expect(groups(list).slice(0, 2)).toEqual(['canvas:@画布', 'canvas:@img2']);
    // The canvas entry is first even when nothing else is on the canvas.
    const libraryOnly = withBoardMention(buildMentionCandidates({ ...empty, history }), boardMentionCandidate('画布'));
    expect(mentionSections(libraryOnly).map((section) => [section.group, section.entries.map((entry) => entry.candidate.handle)])).toEqual([
      ['canvas', ['@画布']], ['library', ['@img2', '@img4', '@img1', '@model1']],
    ]);
  });

  it('cuts the list into sections that keep the flat positions the keyboard moves through', () => {
    const list = withBoardMention(
      buildMentionCandidates({ ...empty, history, layers: [{ name: '图层 1', url: '/api/v1/tasks/t2/assets/a2', taskId: 't2' }] }),
      boardMentionCandidate('画布'),
    );
    const sections = mentionSections(list);
    expect(sections.map((section) => section.group)).toEqual(['canvas', 'library']);
    expect(sections.flatMap((section) => section.entries.map((entry) => entry.index))).toEqual(list.map((_, index) => index));
    // A filtered list keeps its sections; an ungrouped list has one section without heading.
    expect(mentionSections(filterMentions(list, 'img1')).map((section) => section.group)).toEqual(['library']);
    expect(mentionSections([{ handle: '@a', kind: 'image' }, { handle: '@b', kind: 'image' }]).map((section) => [section.group, section.entries.length]))
      .toEqual([[undefined, 2]]);
  });
});
