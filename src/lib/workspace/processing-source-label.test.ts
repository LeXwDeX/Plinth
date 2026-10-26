import { describe, expect, it } from 'vitest';
import { projectTaskAssets } from './task-projection';
import { processingSourceLabel } from './processing-source';

// #327 (#326 note 99189): the 加工 panel's source row read "meshy · rigged_character_glb_url", the
// Provider's own result field. It names the file by its public format from the task projection.
describe('the 加工 panel source row', () => {
  const wire = { id: 'a1', role: 'model', variant: 'rigged_character_glb_url', media_type: 'model/gltf-binary', size: 1, ordinal: 0, url: '/a1' };

  it('reads the public format the server names for each model file', () => {
    expect(projectTaskAssets([{ ...wire, format: 'GLB' }])[0].format).toBe('GLB');
    expect(projectTaskAssets([wire])[0]).not.toHaveProperty('format');
  });

  it('names the provider and the format, never the result field', () => {
    const [asset] = projectTaskAssets([{ ...wire, format: 'GLB' }]);
    expect(processingSourceLabel({ provider_id: 'meshy', capability: 'rigging' }, asset)).toBe('meshy · GLB');
    // Without a known format: the media type, then the step — still not the field name.
    const [unknown] = projectTaskAssets([wire]);
    expect(processingSourceLabel({ provider_id: 'meshy', capability: 'rigging' }, unknown)).toBe('meshy · model/gltf-binary');
    expect(processingSourceLabel({ provider_id: 'meshy', capability: 'rigging' }, null)).toBe('meshy · rigging');
  });
});
