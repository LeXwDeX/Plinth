import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from '@/lib/chat/browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

describe('ChatBrowserClient', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('preserves Gemini ratio and resolution from the Chat or Canvas controls', async () => {
    const schema = schemaFixture();
    let sent: Record<string, unknown> = {};
    const client = createChatBrowserClient(schema, {
      fetcher: async (_input, init) => {
        sent = JSON.parse(String(init?.body));
        return Response.json({
          [schema.chat.api.response_fields.message_id]: 'assistant',
          [schema.chat.api.response_fields.session_id]: 'session',
        });
      },
    });
    await client.startTurn('session', 'paint', {
      model: 'gemini-3-pro-image', aspectRatio: '16:9', imageSize: '2K',
    }, new AbortController().signal);
    expect(sent).toMatchObject({ ai_model: 'gemini-3-pro-image', aspect_ratio: '16:9', image_size: '2K' });
    expect(sent).not.toHaveProperty('eca_image_config');
  });

  it('reads and writes the session notes through the Schema route and member names', async () => {
    const schema = schemaFixture();
    schema.runtime.http.routes.chat_session_notes = '/configured/{sessionID}/summary';
    schema.chat.api.fields.notes = 'configured-notes-in';
    schema.chat.api.response_fields.notes = 'configured-notes-out';
    const calls: Array<{ url: string; method: string; body?: unknown }> = [];
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        const method = init?.method ?? 'GET';
        calls.push({ url: String(input), method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
        if (String(input).includes('missing')) return Response.json({ error: 'gone' }, { status: 404 });
        return Response.json({ id: 's', 'configured-notes-out': method === 'PUT' ? 'saved text' : 'stored' });
      },
    });
    expect(await client.getNotes('s/1')).toBe('stored');
    expect(await client.setNotes('s/1', 'new')).toBe('saved text');
    expect(calls).toEqual([
      { url: '/configured/s%2F1/summary', method: 'GET' },
      { url: '/configured/s%2F1/summary', method: 'PUT', body: { 'configured-notes-in': 'new' } },
    ]);
    await expect(client.getNotes('missing')).rejects.toMatchObject({ status: 404, message: 'gone' });
    delete schema.runtime.http.routes.chat_session_notes;
    await expect(createChatBrowserClient(schema).getNotes('s')).rejects.toBeInstanceOf(Error);
  });

  it('reads, sets and clears the session pick through the Schema route and member names', async () => {
    const schema = schemaFixture();
    schema.runtime.http.routes.chat_session_pick = '/configured/{sessionID}/chosen';
    schema.chat.api.response_fields.pick = 'configured-pick';
    schema.chat.api.attachment.asset_id = 'configuredAsset';
    const calls: Array<{ url: string; method: string; body?: unknown }> = [];
    const picked = { handle: '@img2', kind: 'image', role: 'generated', configuredAsset: 'a1', [schema.chat.api.attachment.task_id]: 't1' };
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        const method = init?.method ?? 'GET';
        calls.push({ url: String(input), method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
        if (String(input).includes('missing')) return Response.json({ error: 'gone' }, { status: 404 });
        return Response.json({ 'configured-pick': method === 'DELETE' || String(input).includes('empty') ? null : picked });
      },
    });
    expect(await client.getPick('s/1')).toMatchObject({ handle: '@img2', assetId: 'a1', taskId: 't1' });
    expect(await client.getPick('empty')).toBeNull();
    expect(await client.setPick('s/1', { kind: 'image', taskId: 't1', assetId: 'a1' })).toMatchObject({ assetId: 'a1' });
    await client.clearPick('s/1');
    expect(calls).toEqual([
      { url: '/configured/s%2F1/chosen', method: 'GET' },
      { url: '/configured/empty/chosen', method: 'GET' },
      { url: '/configured/s%2F1/chosen', method: 'PUT', body: { kind: 'image', configuredAsset: 'a1', [schema.chat.api.attachment.task_id]: 't1' } },
      { url: '/configured/s%2F1/chosen', method: 'DELETE' },
    ]);
    await expect(client.getPick('missing')).rejects.toMatchObject({ status: 404, message: 'gone' });
    await expect(client.setPick('missing', { kind: 'image', taskId: 't', assetId: 'a' })).rejects.toMatchObject({ status: 404 });
    delete schema.runtime.http.routes.chat_session_pick;
    await expect(createChatBrowserClient(schema).getPick('s')).rejects.toBeInstanceOf(Error);
  });

  it('keeps an accepted pick that has no handle and drops one of an unknown kind', async () => {
    const schema = schemaFixture();
    let body: unknown = { pick: { kind: 'image', assetId: 'a1', taskId: 't1' } };
    const client = createChatBrowserClient(schema, { fetcher: async () => Response.json(body) });
    // The server accepts a picture that has no handle (an older message, a model screenshot): it must not read as "nothing picked".
    expect(await client.getPick('s')).toMatchObject({ handle: '', kind: 'image', assetId: 'a1', taskId: 't1' });
    expect(await client.setPick('s', { kind: 'image', taskId: 't1', assetId: 'a1' })).toMatchObject({ handle: '', assetId: 'a1' });
    body = { pick: { kind: 'audio', taskId: 't1' } };
    expect(await client.getPick('s')).toBeNull();
    body = { pick: null };
    expect(await client.getPick('s')).toBeNull();
  });

  it('reads the session version chain through the Schema route', async () => {
    const schema = schemaFixture();
    schema.runtime.http.routes.chat_session_lineage = '/configured/{sessionID}/versions';
    const urls: string[] = [];
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        urls.push(String(input));
        if (String(input).includes('missing')) return Response.json({ error: 'gone' }, { status: 404 });
        return Response.json({
          nodes: [{ id: 't1', kind: 'task', task_id: 't1', created_at: '2026-01-01T00:00:00Z', assets: [{ id: 'a1', role: 'image', media_type: 'image/png', ordinal: 0, url: '/api/v1/tasks/t1/assets/a1' }] }],
          edges: [], truncated: false,
        });
      },
    });
    expect((await client.getLineage('s/1')).nodes).toMatchObject([{ id: 't1', assets: [{ id: 'a1', kind: 'image' }] }]);
    expect(urls).toEqual(['/configured/s%2F1/versions']);
    await expect(client.getLineage('missing')).rejects.toMatchObject({ status: 404, message: 'gone' });
    delete schema.runtime.http.routes.chat_session_lineage;
    await expect(createChatBrowserClient(schema).getLineage('s')).rejects.toBeInstanceOf(Error);
  });

  it('sends the per-domain model choices under the Schema field name', async () => {
    const schema = schemaFixture();
    schema.chat.api.fields.domain_models = 'configured-domain-models';
    let sent: Record<string, unknown> = {};
    const client = createChatBrowserClient(schema, {
      fetcher: async (_input, init) => {
        sent = JSON.parse(String(init?.body));
        return Response.json({ messageId: 'assistant', sessionId: 'session' });
      },
    });
    await client.startTurn('session', 'hi', {
      creativeDomain: 'image', model: 'image-model', domainModels: { 'model-3d': 'three-model' },
    }, new AbortController().signal);
    expect(sent['configured-domain-models']).toEqual({ 'model-3d': 'three-model' });
    expect(sent).toMatchObject({ ai_model: 'image-model' });
    await client.startTurn('session', 'hi', { domainModels: {} }, new AbortController().signal);
    expect(sent).not.toHaveProperty('configured-domain-models');
  });

  it('sends attachment claims and the selection, and reads back verified attachments', async () => {
    const schema = schemaFixture();
    schema.chat.api.fields.attachments = 'configured-claims';
    schema.chat.api.fields.selection = 'configured-selected';
    schema.chat.api.fields.input_source = 'configured-source';
    schema.chat.api.response_fields.attachments = 'configured-objects';
    schema.chat.api.attachment.asset_id = 'configured-asset';
    let sent: Record<string, unknown> = {};
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        if (init?.method === 'POST') {
          sent = JSON.parse(String(init.body));
          return Response.json({ messageId: 'assistant', sessionId: 'session' });
        }
        expect(String(input)).toContain('session');
        return Response.json({ messages: [{
          id: 'm1', role: schema.chat.roles.user, content: 'hi', createdAt: '2026-01-01T00:00:00Z',
          'configured-objects': [{ handle: '@img1', kind: 'image', role: 'capture', 'configured-asset': 'shot', taskId: 'model-1', captureId: 'capture-1', imageSlot: 1 }],
        }], next_cursor: '' });
      },
    });
    await client.startTurn('session', 'hi', {
      imageURLs: ['/api/v1/assets/shot'],
      attachments: [{ kind: 'image', imageSlot: 1, assetId: 'shot', captureId: 'capture-1', taskId: 'model-1' }],
      selection: [{ kind: 'model', taskId: 'model-1' }],
      inputSource: 'canvas',
    }, new AbortController().signal);
    // #266: the declared source travels under its Schema field name.
    expect(sent['configured-source']).toBe('canvas');
    expect(sent['configured-claims']).toEqual([
      { kind: 'image', 'configured-asset': 'shot', taskId: 'model-1', captureId: 'capture-1', imageSlot: 1 },
    ]);
    expect(sent['configured-selected']).toEqual([{ kind: 'model', taskId: 'model-1' }]);
    const { messages } = await client.listMessagePage('session', { limit: 50 });
    expect(messages[0].attachments).toEqual([
      expect.objectContaining({ handle: '@img1', kind: 'image', role: 'capture', assetId: 'shot', captureId: 'capture-1', imageSlot: 1 }),
    ]);
    // Without claims nothing is sent for them.
    await client.startTurn('session', 'plain', {}, new AbortController().signal);
    expect(sent).not.toHaveProperty('configured-claims');
    expect(sent).not.toHaveProperty('configured-selected');
    expect(sent).not.toHaveProperty('configured-source');
  });

  it('sends the answer to a choice card under its Schema field (#321)', async () => {
    const schema = schemaFixture();
    schema.chat.api.fields.choice_answer = 'configured-answer';
    let sent: Record<string, unknown> = {};
    const client = createChatBrowserClient(schema, {
      fetcher: async (_input, init) => {
        sent = JSON.parse(String(init?.body));
        return Response.json({ messageId: 'assistant', sessionId: 'session' });
      },
    });
    await client.startTurn('session', '选择', { choiceAnswer: { choiceId: 'choice_1', optionId: 'hyper3d/bang/rodin-task' } });
    expect(sent['configured-answer']).toEqual({ choice_id: 'choice_1', option_id: 'hyper3d/bang/rodin-task' });
    await client.startTurn('session', '默认', { choiceAnswer: { choiceId: 'choice_1', useDefault: true } });
    expect(sent['configured-answer']).toEqual({ choice_id: 'choice_1', use_default: true });
    await client.startTurn('session', 'plain', {});
    expect(sent).not.toHaveProperty('configured-answer');
  });

  it('resolves the Schema tool capability by reference-image presence', () => {
    const schema = schemaFixture();
    schema.chat.tools[0].capability = 'configured-with-reference';
    schema.chat.tools[0].fallback_capability =
      'configured-without-reference';
    const client = createChatBrowserClient(schema);

    expect(client.generationCapability(0)).toBe(
      'configured-without-reference',
    );
    expect(client.generationCapability(2)).toBe(
      'configured-with-reference',
    );
  });

  it('enhances a prompt through the Schema-configured API contract', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.prompt_enhance =
      '/configured/prompt-enhance';
    schema.chat.api.prompt_enhance.prompt_field = 'configured-prompt';
    schema.chat.api.prompt_enhance.capability_field =
      'configured-capability';
    schema.chat.api.prompt_enhance.model_field = 'configured-model';
    schema.chat.api.prompt_enhance.result_field = 'configured-result';

    let requestedURL = '';
    let requestedBody: unknown;
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        requestedURL = String(input);
        requestedBody = JSON.parse(String(init?.body));
        return Response.json({
          'configured-result': 'configured enhanced prompt',
        });
      },
    });

    const result = await client.enhancePrompt(
      'original prompt',
      'image-capability',
      'image-model',
    );

    expect(requestedURL).toBe('/configured/prompt-enhance');
    expect(requestedBody).toEqual({
      'configured-prompt': 'original prompt',
      'configured-capability': 'image-capability',
      'configured-model': 'image-model',
    });
    expect(result).toBe('configured enhanced prompt');
  });

  it('decodes workbench prompt fields from the Schema contract', async () => {
    const schema = schemaFixture();
    const contract = schema.chat.api.prompt_enhance;
    contract.prompt_result_field = 'configured-prompt-result';
    contract.negative_prompt_result_field =
      'configured-negative-result';
    const client = createChatBrowserClient(schema, {
      fetcher: async () =>
        Response.json({
          'configured-prompt-result': 'configured prompt',
          'configured-negative-result': 'configured negative',
        }),
    });

    const result = await client.enhancePromptResult(
      'original',
      'text-to-image',
      'gpt-image-2',
    );

    expect(result).toEqual({
      prompt: 'configured prompt',
      negativePrompt: 'configured negative',
    });
  });

  it('loads and normalizes the Schema-configured Skill Catalog', async () => {
    const schema = schemaFixture();
    schema.runtime.http.routes.chat_skills = '/configured/skill-schema';
    const fields = schema.chat.skills.fields;
    fields.revision = 'configured-revision';
    fields.skills = 'configured-items';
    fields.id = 'configured-id';
    fields.name = 'configured-name';
    fields.name_zh = 'configured-name-zh';
    fields.category = 'configured-category';
    fields.recommended_size = 'configured-size';
    fields.thumbnail_path = 'configured-thumbnail';
    fields.raw_image_url = 'configured-raw-image';
    fields.prompt = 'configured-prompt';
    fields.image_to_image_template = 'configured-i2i-template';
    fields.tags = 'configured-tags';
    fields.tool = 'configured-tool';
    fields.requires_image = 'configured-needs-image';
    fields.default_message = 'configured-default-text';
    fields.one_shot = 'configured-one-shot';

    let requestedURL = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requestedURL = String(input);
        return Response.json({
          'configured-revision': 'catalog-revision',
          'configured-items': [
            {
              'configured-id': 'skill-a',
              'configured-name': 'Skill A',
              'configured-name-zh': '技能 A',
              'configured-category': 'configured-category-a',
              'configured-size': '1024x1024',
              'configured-thumbnail': '/configured-thumbnail.webp',
              'configured-raw-image': 'https://example.test/raw.png',
              'configured-prompt': 'configured prompt',
              'configured-i2i-template': 'configured template',
              'configured-tags': ['configured-tag'],
            },
            {
              'configured-id': 'system-three-view',
              'configured-name': 'Three-view',
              'configured-name-zh': '三视图',
              'configured-category': '',
              'configured-size': '',
              'configured-thumbnail': '',
              'configured-raw-image': '',
              'configured-prompt': 'make three views',
              'configured-tool': 'create_three_view',
              'configured-needs-image': true,
              'configured-default-text': '用这张图生成三视图',
              'configured-one-shot': true,
            },
          ],
        });
      },
    });

    const catalog = await client.listSkills();

    expect(requestedURL).toBe('/configured/skill-schema');
    expect(catalog).toEqual({
      revision: 'catalog-revision',
      skills: [
        {
          id: 'skill-a',
          name: 'Skill A',
          displayName: '技能 A',
          category: 'configured-category-a',
          recommendedSize: '1024x1024',
          thumbnailPath: '/configured-thumbnail.webp',
          rawImageURL: 'https://example.test/raw.png',
          prompt: 'configured prompt',
          imageToImageTemplate: 'configured template',
          tags: ['configured-tag'],
        },
        {
          id: 'system-three-view',
          name: 'Three-view',
          displayName: '三视图',
          category: '',
          recommendedSize: '',
          thumbnailPath: '',
          rawImageURL: '',
          prompt: 'make three views',
          tool: 'create_three_view',
          requiresImage: true,
          defaultMessage: '用这张图生成三视图',
          oneShot: true,
        },
      ],
    });
  });

  it('starts a turn using only Product Schema routes and wire fields', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_messages =
      '/configured/chat/{sessionID}/turns';
    schema.chat.api.fields.content = 'configured-content';
    schema.chat.api.fields.image_urls = 'configured-images';
    schema.chat.api.fields.aspect_ratio = 'configured-aspect';
    schema.chat.api.fields.pose_mode = 'configured-pose';
    schema.chat.api.fields.ai_model = 'configured-model';
    schema.chat.api.fields.size = 'configured-size';
    schema.chat.api.fields.image_size = 'configured-image-size';
    schema.chat.api.fields.parameters = 'configured-parameters';
    schema.chat.api.fields.skill = 'configured-skill';
    schema.chat.api.fields.skill_id = 'configured-skill-id';
    schema.chat.api.fields.skill_name = 'configured-skill-name';
    schema.chat.api.fields.reference_image_indices = 'configured-references';
    schema.chat.api.response_fields.message_id = 'configured-message-id';
    schema.chat.api.response_fields.session_id = 'configured-session-id';

    let requestedURL = '';
    let requestedBody: unknown;
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        requestedURL = String(input);
        requestedBody = JSON.parse(String(init?.body));
        return Response.json({
          'configured-message-id': 'assistant-a',
          'configured-session-id': 'session-a',
        });
      },
    });

    const accepted = await client.startTurn(
      'session / a',
      'draw',
      {
        imageURLs: ['/configured/reference'],
        aspectRatio: '16:9',
        poseMode: 'configured-pose-value',
        model: 'configured-model-value',
        size: 'configured-size-value',
        imageSize: 'configured-image-size-value',
        parameters: { configuredQuality: 'xhigh' },
        skill: { id: 'skill-a', name: 'Configured Skill' },
        referenceImageIndices: [2, 4],
      },
      new AbortController().signal,
    );

    expect(requestedURL).toBe('/configured/chat/session%20%2F%20a/turns');
    expect(requestedBody).toEqual({
      'configured-content': 'draw',
      'configured-images': ['/configured/reference'],
      'configured-aspect': '16:9',
      'configured-pose': 'configured-pose-value',
      'configured-model': 'configured-model-value',
      'configured-size': 'configured-size-value',
      'configured-image-size': 'configured-image-size-value',
      'configured-parameters': { configuredQuality: 'xhigh' },
      'configured-skill': {
        'configured-skill-id': 'skill-a',
        'configured-skill-name': 'Configured Skill',
      },
      'configured-references': [2, 4],
    });
    expect(accepted).toEqual({
      assistantMessageID: 'assistant-a',
      sessionID: 'session-a',
    });
  });

  it('normalizes messages from Schema-configured response fields and roles', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_messages =
      '/configured/chat/{sessionID}/messages';
    schema.chat.api.response_fields.id = 'configured-id';
    schema.chat.api.response_fields.role = 'configured-role';
    schema.chat.api.response_fields.content = 'configured-content';
    schema.chat.api.response_fields.image_urls = 'configured-images';
    schema.chat.api.response_fields.tool_calls = 'configured-tools';
    schema.chat.api.response_fields.tool_name = 'configured-tool-name';
    schema.chat.api.response_fields.task_id = 'configured-task-id';
    schema.chat.api.response_fields.created_at = 'configured-created';
    schema.chat.roles.user = 'configured-user-role';
    schema.chat.roles.assistant = 'configured-assistant-role';
    schema.chat.state.pending_content = 'configured-pending-content';
    schema.chat.tools[0].name = 'configured-tool';
    schema.chat.tools[0].pending_capability = 'configured-capability';

    let requestedURL = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requestedURL = String(input);
        return Response.json({ messages: [
          {
            'configured-id': 'message-a',
            'configured-role': 'configured-assistant-role',
            'configured-content': 'configured-pending-content',
            'configured-images': JSON.stringify(['/configured/image']),
            'configured-tools': JSON.stringify([
              {
                'configured-tool-name': 'configured-tool',
                'configured-task-id': 'task-a',
              },
            ]),
            'configured-created': '2026-07-26T10:00:00.000Z',
          },
        ], next_cursor: '' });
      },
    });

    const { messages } = await client.listMessagePage('session / a', { limit: 50 });

    expect(requestedURL).toBe(
      '/configured/chat/session%20%2F%20a/messages?limit=50',
    );
    expect(messages).toEqual([
      {
        id: 'message-a',
        role: 'assistant',
        content: '',
        images: [
          { url: '/configured/image', taskId: 'task-a' },
        ],
        isPending: true,
        pendingTaskTypes: ['configured-capability'],
        taskIds: ['task-a'],
        createdAt: Date.parse('2026-07-26T10:00:00.000Z'),
      },
    ]);
  });

  it('reads one page of messages with limit and before, oldest first, and reports errors', async () => {
    const schema = schemaFixture();
    const requested: string[] = [];
    let status = 200;
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requested.push(String(input));
        if (status !== 200) return Response.json({ error: 'configured bad page' }, { status });
        return Response.json({
          messages: [
            { id: 'older', role: schema.chat.roles.user, content: 'first', createdAt: '2026-10-12T09:00:00Z' },
            { id: 'newer', role: schema.chat.roles.assistant, content: 'second', createdAt: '2026-10-12T09:01:00Z' },
          ],
          next_cursor: 'cursor-1',
        });
      },
    });

    const newest = await client.listMessagePage('session / a', { limit: 2 });
    expect(newest.messages.map((message) => message.id)).toEqual(['older', 'newer']);
    expect(newest.nextCursor).toBe('cursor-1');
    await client.listMessagePage('session / a', { before: 'cursor-1', limit: 2 });
    expect(requested).toEqual([
      '/api/chat/sessions/session%20%2F%20a/messages?limit=2',
      '/api/chat/sessions/session%20%2F%20a/messages?limit=2&before=cursor-1',
    ]);
    status = 400;
    await expect(client.listMessagePage('s', { limit: 2 })).rejects.toMatchObject({ status: 400 });
  });

  it('gives each picture the task its attachment names, else the message\'s only task, else none', async () => {
    const schema = schemaFixture();
    const message = (id: string, extra: Record<string, unknown>) => ({
      id, role: schema.chat.roles.assistant, content: 'done', createdAt: '2026-01-01T00:00:00Z', ...extra,
    });
    const call = (taskId: string) => ({ name: 'tool', taskId });
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ messages: [
        // A 3D task's render (slot 1) and its pre-model picture (slot 2, an old image attachment) next to a 2D task's picture.
        message('m1', {
          imageUrls: ['/a', '/b', '/c'], toolCalls: [call('t-3d'), call('t-2d')],
          attachments: [
            { handle: '@model1', kind: 'model', taskId: 't-3d', imageSlot: 1 },
            { handle: '@img1', kind: 'image', taskId: 't-3d', imageSlot: 2 },
            { handle: '@img2', kind: 'image', taskId: 't-2d', imageSlot: 3 },
          ],
        }),
        // Two tasks and no attachment for slot 1: the picture is not guessed onto the first task.
        message('m2', {
          imageUrls: ['/a', '/b'], toolCalls: [call('t1'), call('t2')],
          attachments: [{ handle: '@img1', kind: 'image', taskId: 't2', imageSlot: 2 }],
        }),
        // One task, only slot 1 attached: the second picture belongs to that task too.
        message('m3', {
          imageUrls: ['/a', '/b'], toolCalls: [call('t1')],
          attachments: [{ handle: '@model1', kind: 'model', taskId: 't1', imageSlot: 1 }],
        }),
        // The same task called twice is still one task; no task at all is none.
        message('m4', { imageUrls: ['/a'], toolCalls: [call('t1'), call('t1')] }),
        message('m5', { imageUrls: ['/a'] }),
        // A new 3D message carries only the cover; the attachment names its task among several tool calls.
        message('m6', {
          imageUrls: ['/a'], toolCalls: [call('t-2d'), call('t-3d')],
          attachments: [{ handle: '@model1', kind: 'model', taskId: 't-3d', imageSlot: 1 }],
        }),
      ], next_cursor: '' }),
    });
    const { messages } = await client.listMessagePage('s', { limit: 50 });
    expect(messages.map((item) => item.images?.map((image) => image.taskId))).toEqual([
      ['t-3d', 't-3d', 't-2d'], ['', 't2'], ['t1', 't1'], ['t1'], [''], ['t-3d'],
    ]);
  });

  it('creates and normalizes a session from Product Schema', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_sessions =
      '/configured/chat/sessions';
    schema.chat.defaults.title = 'Configured default title';
    schema.chat.api.fields.title = 'configured-title-input';
    schema.chat.api.response_fields.id = 'configured-id';
    schema.chat.api.response_fields.title = 'configured-title';
    schema.chat.api.response_fields.created_at = 'configured-created';
    schema.chat.api.response_fields.updated_at = 'configured-updated';

    let requestedBody: unknown;
    const client = createChatBrowserClient(schema, {
      fetcher: async (_input, init) => {
        requestedBody = JSON.parse(String(init?.body));
        return Response.json({
          'configured-id': 'session-a',
          'configured-title': 'Configured default title',
          'configured-created': '2026-07-26T10:00:00.000Z',
          'configured-updated': '2026-07-26T10:01:00.000Z',
        });
      },
    });

    const session = await client.createSession();

    // The server applies the default title (in the reader's locale) and keeps the automatic title pending.
    expect(requestedBody).toEqual({
      'configured-title-input': '',
    });
    expect(session).toEqual({
      id: 'session-a',
      title: 'Configured default title',
      createdAt: '2026-07-26T10:00:00.000Z',
      updatedAt: '2026-07-26T10:01:00.000Z',
      // A payload without approval fields is a manual session with no credits yet.
      approvalMode: 'manual',
      estimatedCredits: 0,
    });
  });

  it('renames a stable session through Schema-configured routes and fields', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_session = '/configured/sessions/{sessionID}';
    schema.chat.api.fields.title = 'name';
    schema.chat.api.response_fields.title = 'name';
    schema.chat.api.response_fields.id = 'session';
    const fetcher = vi.fn(async () => Response.json({ session: 'a / b', name: 'Renamed' }));
    const client = createChatBrowserClient(schema, { fetcher });
    expect(await client.renameSession('a / b', 'Renamed')).toMatchObject({ id: 'a / b', title: 'Renamed' });
    expect(fetcher).toHaveBeenCalledWith('/configured/sessions/a%20%2F%20b', {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'Renamed' }),
    });
  });

  it.each([400, 404, 500])('does not treat failed rename (%s) as saved', async (status) => {
    const schema = schemaFixture();
    schema.chat.api.response_fields.error = 'reason';
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ reason: 'Safe error' }, { status }),
    });
    await expect(client.renameSession('session-a', 'Name')).rejects.toMatchObject({ status, message: 'Safe error' });
  });

  it('lists sessions using Schema-configured response fields', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_sessions =
      '/configured/chat/sessions';
    schema.chat.api.response_fields.id = 'configured-id';
    schema.chat.api.response_fields.title = 'configured-title';
    schema.chat.api.response_fields.created_at = 'configured-created';
    schema.chat.api.response_fields.updated_at = 'configured-updated';
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json([
        {
          'configured-id': 'session-a',
          'configured-title': 'Configured title',
          'configured-created': 'configured-created-value',
          'configured-updated': 'configured-updated-value',
        },
      ]),
    });

    const sessions = await client.listSessions();

    expect(sessions).toEqual([
      {
        id: 'session-a',
        title: 'Configured title',
        createdAt: 'configured-created-value',
        updatedAt: 'configured-updated-value',
        approvalMode: 'manual',
        estimatedCredits: 0,
      },
    ]);
  });

  it('reads one session page with its cursor and owner-visible total', async () => {
    vi.stubGlobal('window', { location: { origin: 'http://localhost' } });
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_sessions = '/configured/chat/sessions';
    const requested: string[] = [];
    let payload: Record<string, unknown> = {
      sessions: [{
        [schema.chat.api.response_fields.id]: 'session-a',
        [schema.chat.api.response_fields.title]: 'Cat',
        [schema.chat.api.response_fields.created_at]: '2026-01-01T00:00:00Z',
        [schema.chat.api.response_fields.updated_at]: '2026-01-01T00:00:00Z',
      }],
      next_cursor: 'c1',
      total: 16,
    };
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requested.push(String(input));
        return Response.json(payload);
      },
    });

    const page = await client.listSessionPage({ search: 'cat', cursor: 'c0', limit: 15 });

    expect(requested).toEqual(['/configured/chat/sessions?search=cat&limit=15&cursor=c0']);
    expect(page.total).toBe(16);
    expect(page.nextCursor).toBe('c1');
    expect(page.sessions.map((session) => session.id)).toEqual(['session-a']);

    // A missing or invalid total is a read failure, never a made-up zero.
    const invalidTotals = [undefined, null, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_SAFE_INTEGER + 1, '16'];
    for (const total of invalidTotals) {
      const invalid = createChatBrowserClient(schema, {
        fetcher: async () => ({
          ok: true,
          status: 200,
          json: async () => ({ sessions: [], next_cursor: '', total }),
        }) as Response,
      });
      await expect(invalid.listSessionPage({ search: 'cat', limit: 15 })).rejects.toMatchObject({
        name: 'ChatBrowserError',
        status: 200,
        message: schema.chat.presentation.messages.internal_error,
      });
    }
    const overflow = createChatBrowserClient(schema, {
      fetcher: async () => new Response('{"sessions":[],"next_cursor":"","total":1e999}', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    });
    await expect(overflow.listSessionPage({ search: 'cat', limit: 15 })).rejects.toMatchObject({ status: 200 });
    payload = { sessions: [], next_cursor: '', total: 0 };
    expect((await client.listSessionPage({ search: 'cat', limit: 15 })).total).toBe(0);
  });

  it('decodes Schema-configured SSE wire events into semantic events', () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_stream =
      '/configured/chat/{sessionID}/events';
    schema.chat.api.fields.from_id = 'configured-from';
    schema.chat.stream.event_fields.type = 'configured-type';
    schema.chat.stream.event_fields.content = 'configured-content';
    schema.chat.stream.event_fields.task_id = 'configured-task';
    schema.chat.stream.event_fields.final_task_id = 'configured-final-task';
    schema.chat.stream.event_fields.status = 'configured-status';
    schema.chat.stream.event_fields.progress = 'configured-progress';
    schema.chat.stream.event_fields.result = 'configured-result';
    schema.chat.stream.event_fields.image_urls = 'configured-image-urls';
    schema.chat.stream.event_fields.error = 'configured-error';
    schema.chat.stream.event_types.text = 'configured-text-event';
    schema.chat.stream.event_types.task_update =
      'configured-task-update-event';
    schema.chat.stream.event_types.done = 'configured-done-event';

    const source = new FakeEventSource();
    let requestedURL = '';
    const client = createChatBrowserClient(schema, {
      eventSourceFactory: (url) => {
        requestedURL = url;
        return source;
      },
    });
    const events: unknown[] = [];
    let done = 0;
    client.subscribe('session / a', {
      fromID: 7,
      onEvent: (event) => events.push(event),
      onDone: () => { done++; },
    });

    source.emit({
      'configured-type': 'configured-text-event',
      'configured-content': 'delta',
    });
    source.emit({
      'configured-type': 'configured-task-update-event',
      'configured-task': 'task-a',
      'configured-final-task': 'task-b',
      'configured-status': 'SUCCEEDED',
      'configured-progress': 100,
      'configured-result': {
        'configured-image-urls': ['/configured/result'],
      },
      'configured-error': 'configured safe error',
    });
    source.emit({
      'configured-type': 'configured-done-event',
    });

    expect(requestedURL).toBe(
      '/configured/chat/session%20%2F%20a/events?configured-from=7',
    );
    expect(events).toEqual([
      { type: 'text', content: 'delta' },
      {
        type: 'taskUpdate',
        taskID: 'task-a',
        finalTaskID: 'task-b',
        status: 'SUCCEEDED',
        progress: 100,
        result: { imageURLs: ['/configured/result'] },
        error: 'configured safe error',
      },
      { type: 'done' },
    ]);
    expect(done).toBe(1);
    expect(source.closed).toBe(true);
  });

  it('deletes a message through the Schema-configured item route', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_message =
      '/configured/chat/{sessionID}/messages/{messageID}';
    let requestedURL = '';
    let requestedMethod = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        requestedURL = String(input);
        requestedMethod = String(init?.method);
        return new Response(null, { status: 204 });
      },
    });

    const deleted = await client.deleteMessage('session / a', 'message / a');

    expect(deleted).toBe(true);
    expect(requestedURL).toBe(
      '/configured/chat/session%20%2F%20a/messages/message%20%2F%20a',
    );
    expect(requestedMethod).toBe('DELETE');
  });

  it('deletes a session through the Schema-configured item route', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_session =
      '/configured/chat/sessions/{sessionID}';
    let requestedURL = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requestedURL = String(input);
        return new Response(null, { status: 204 });
      },
    });

    const deleted = await client.deleteSession('session / a');

    expect(deleted).toBe(true);
    expect(requestedURL).toBe(
      '/configured/chat/sessions/session%20%2F%20a',
    );
  });

  it('fails an idle SSE connection at the Schema-configured timeout', () => {
    vi.useFakeTimers();
    const schema = schemaFixture();
    schema.chat.frontend.connection_timeout_milliseconds = 37;
    schema.chat.presentation.messages.sse_interrupted =
      'configured stream interruption';
    const source = new FakeEventSource();
    const client = createChatBrowserClient(schema, {
      eventSourceFactory: () => source,
    });
    const errors: string[] = [];
    client.subscribe('session-a', {
      onEvent: () => {},
      onError: (error) => errors.push(error.message),
    });

    vi.advanceTimersByTime(36);
    expect(errors).toEqual([]);
    vi.advanceTimersByTime(1);

    expect(errors).toEqual(['configured stream interruption']);
    expect(source.closed).toBe(true);
  });
});

class FakeEventSource {
  onmessage: ((event: MessageEvent<string>) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;

  close(): void {
    this.closed = true;
  }

  emit(payload: unknown): void {
    this.onmessage?.({
      data: JSON.stringify(payload),
    } as MessageEvent<string>);
  }
}

describe('native task registration stream', () => {
  const quote = { version: 1, unit: 'vendor.credit', unit_label: 'Vendor credits', base_amount: '0.5', extra_amount: '0', total_amount: '0.5' };
  it.each([quote, null, { ...quote, total_amount: 'broken' }])('retains valid or unknown native quotes from SSE without a legacy fallback', (native_quote) => {
    const schema = schemaFixture();
    const source = new FakeEventSource();
    const events: Array<import('./browser-client').ChatSemanticEvent> = [];
    createChatBrowserClient(schema, { eventSourceFactory: () => source }).subscribe('s', { onEvent: event => events.push(event) });
    source.emit({ [schema.chat.stream.event_fields.type]: schema.chat.stream.event_types.task_registered,
      [schema.chat.stream.event_fields.task_id]: 'native-task', [schema.chat.stream.event_fields.estimated_credits]: 999, native_quote });
    expect(events[0]).toMatchObject({ type: 'taskRegistered', nativeQuote: native_quote === quote ? quote : null });
    expect(events[0]).not.toHaveProperty('estimatedCredits');
  });
});
