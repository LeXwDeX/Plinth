import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { hasIcon, iconByName, ICON_NAMES } from './icon-map';

interface CatalogIcons {
  canvas: { frontend: {
    stage_actions: Array<{ id: string; icon: string }>;
    tools: Array<{ id: string; rail: string; icon: string }>;
    panel_tabs: { tabs: Array<{ id: string; icon: string }> };
  } };
  capabilities: Array<{ id: string; domain: string; icon?: string }>;
  runtime: { frontend: { model_processing_capabilities: string[] } };
}
const application = defaultCatalog.application as unknown as CatalogIcons;

describe('icon map', () => {
  it('resolves every icon the default catalog names for a stage action', () => {
    expect(application.canvas.frontend.stage_actions.length).toBeGreaterThan(0);
    for (const action of application.canvas.frontend.stage_actions) {
      expect(hasIcon(action.icon), `stage action ${action.id}: icon ${action.icon}`).toBe(true);
    }
  });

  it('resolves every icon the default catalog names for a canvas tool or a 3D view', () => {
    expect(application.canvas.frontend.tools.length).toBeGreaterThan(0);
    for (const tool of application.canvas.frontend.tools) {
      expect(hasIcon(tool.icon), `${tool.rail} tool ${tool.id}: icon ${tool.icon}`).toBe(true);
    }
  });

  it('resolves every icon the default catalog names for a right panel tab', () => {
    expect(application.canvas.frontend.panel_tabs.tabs.length).toBeGreaterThan(0);
    expect(application.capabilities.find((item) => item.id === 'mesh-completion')?.icon).toBe('package-plus');
    for (const tab of application.canvas.frontend.panel_tabs.tabs) {
      expect(hasIcon(tab.icon), `panel tab ${tab.id}: icon ${tab.icon}`).toBe(true);
    }
  });

  it('gives each meaning its own picture across stage actions, processing steps, rail tools and panel tabs', () => {
    const owners = new Map<string, string>();
    const claim = (icon: string, owner: string) => {
      expect(owners.get(icon), `${owner} reuses ${icon} of ${owners.get(icon)}`).toBeUndefined();
      owners.set(icon, owner);
    };
    for (const action of application.canvas.frontend.stage_actions) claim(action.icon, `stage action ${action.id}`);
    for (const id of application.runtime.frontend.model_processing_capabilities) {
      claim(application.capabilities.find((item) => item.id === id && item.domain === 'processing')?.icon ?? '', `processing ${id}`);
    }
    for (const tool of application.canvas.frontend.tools) claim(tool.icon, `${tool.rail} tool ${tool.id}`);
    for (const tab of application.canvas.frontend.panel_tabs.tabs) claim(tab.icon, `panel tab ${tab.id}`);
  });

  it('draws the glyphs the UI spec names for the renamed and re-iconed tools', () => {
    const iconOf = (id: string) => application.canvas.frontend.tools.find((tool) => tool.id === id)?.icon;
    expect(Object.fromEntries(['mask', 'upload', 'brush', 'eraser', 'zoom', 'texture', 'clay', 'wire'].map((id) => [id, iconOf(id)]))).toEqual({
      mask: 'spray-can', upload: 'image-plus', brush: 'paintbrush', eraser: 'eraser', zoom: 'zoom-in',
      texture: 'wallpaper', clay: 'cylinder', wire: 'grid-3x3',
    });
  });

  it('resolves the icon of every processing capability the toolbar can offer', () => {
    for (const id of application.runtime.frontend.model_processing_capabilities) {
      const capability = application.capabilities.find((item) => item.id === id && item.domain === 'processing');
      expect(capability, `capability ${id}`).toBeDefined();
      expect(capability?.icon, `capability ${id} icon`).toBeTruthy();
      expect(hasIcon(capability?.icon ?? ''), `capability ${id}: icon ${capability?.icon}`).toBe(true);
    }
  });

  it('draws a plain circle for a name it does not know, and the real component for one it does', () => {
    const unknown = iconByName('no-such-icon');
    expect(hasIcon('no-such-icon')).toBe(false);
    for (const name of ICON_NAMES) expect(iconByName(name), name).not.toBe(unknown);
  });

  it('answers only for its own names, not inherited object keys', () => {
    expect(hasIcon('constructor')).toBe(false);
    expect(hasIcon('toString')).toBe(false);
  });
});
