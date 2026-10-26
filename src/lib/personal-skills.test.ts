import { afterEach, describe, expect, it, vi } from 'vitest';

import { deletePersonalSkill, listPersonalSkillIds } from '@/lib/personal-skills';
import type { ProductSchema } from '@/lib/product-schema';

const creation = {
  route: '/api/v1/chat/skills/personal',
  item_route: '/api/v1/chat/skills/personal/{skillID}',
  max_name_characters: 100,
  recommended_size: 'auto',
};
const schema = { chat: { skills: { creation } } } as unknown as ProductSchema;

describe('personal Skill client (#238)', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('deletes through the Schema item route with an encoded ID', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    vi.stubGlobal('fetch', fetchMock);
    await deletePersonalSkill(schema, 'personal-a/b');
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/chat/skills/personal/personal-a%2Fb', { method: 'DELETE' });
  });

  it('throws on a failed delete so the caller keeps the row and selection', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{}', { status: 404 }))));
    await expect(deletePersonalSkill(schema, 'personal-a')).rejects.toThrow('HTTP 404');
  });

  it('lists only string IDs of the caller personal Skills', async () => {
    const body = JSON.stringify({ skills: [{ id: 'personal-a' }, { id: 3 }, {}] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(body, { status: 200 }))));
    expect(await listPersonalSkillIds(schema)).toEqual(['personal-a']);
  });

  it('refuses a cross-origin item route', async () => {
    const broken = { chat: { skills: { creation: { ...creation, item_route: '//evil.test/{skillID}' } } } } as unknown as ProductSchema;
    await expect(deletePersonalSkill(broken, 'x')).rejects.toThrow('Invalid Skill route');
  });
});
