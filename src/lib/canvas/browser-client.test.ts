import { describe, expect, it, vi } from 'vitest';
import { createCanvasBrowserClient } from './browser-client';
import type { CanvasProductSchema } from '../product-schema';

const canvasSchema: CanvasProductSchema = {
  api: {
    fields: {
      id: 'wireId',
      user_id: 'wireUser',
      name: 'wireName',
      session_id: 'wireSession',
      viewport: 'wireViewport',
      bg_style: 'wireBackground',
      created: 'wireWasCreated',
      created_at: 'wireCreated',
      updated_at: 'wireUpdated',
      nodes: 'wireNodes',
      count: 'wireCount',
      node_count: 'wireNodeCount',
      ok: 'wireOK',
      error: 'wireError',
      project_id: 'wireProject',
      type: 'wireType',
      x: 'wireX',
      y: 'wireY',
      width: 'wireWidth',
      height: 'wireHeight',
      z_index: 'wireZ',
      data: 'wireData',
      visible: 'wireVisible',
      locked: 'wireLocked',
      asset_id: 'wireAsset',
      url: 'wireURL',
      image_url: 'wireImageURL',
      canvas_width: 'wireCanvasWidth',
      canvas_height: 'wireCanvasHeight',
      coordinate_version: 'wireCoordinates',
      revision: '7',
    },
    path_parameters: {
      project_id: 'wireProjectKey',
    },
  },
  defaults: {
    bg_style: 'configured-grid',
    coordinate_version: 2,
  },
  limits: {
    maximum_merge_nodes: 20,
    maximum_bitmap_nodes: 50,
    maximum_dimension: 8192,
    minimum_dimension: 64,
  },
  frontend: {
    focus_delay_milliseconds: 100,
    auto_save_delay_milliseconds: 1500,
    saved_indicator_milliseconds: 2000,
    context_menu_bind_delay_milliseconds: 0,
    permission_redirect_path: '/tasks',
    initial_node_x: 40,
    initial_node_y: 40,
    node_gap_pixels: 20,
    minimum_node_size: 50,
    model_capture_long_edge_pixels: 2048,
    to_3d_long_edge_pixels: 1024,
    initial_layer: { enabled: true, fill: 'transparent', maximum_bitmap_pixels: 16777216 },
    artboard: { resize: { default_anchor: 'center', anchors: ['center'] }, center_x: 600, center_y: 400, legacy_max_edge: 512, default_width: 2048, default_height: 2048, ratio_pixels: { '1:1': { width: 1024, height: 1024 } }, frame_stroke_width: 1, frame_line_dash: [6, 4], label: { common_ratios: ['1:1', '3:2'], font_size_pixels: 10, offset_pixels: 4, color_token: '--muted-foreground', padding_pixels: 3, background_token: '--background', background_opacity: 0.72 }, outside_dim: { color: '#000000', opacity: 0.45 }, transparency: { cell_pixels: 8, light_color: '#ffffff', dark_color: '#000000', opacity: 0.08 } },
    brush: { minimum_size: 1, maximum_size: 200, default_size: 12 },
    selection: { ants_dash: [5, 5], ants_tick_milliseconds: 120, tint_color: '#6366f1', tint_opacity: 0.3, preview_max_edge: 512 },
    mask: { tint_color_token: '--info', tint_opacity: 0.75 },
    snap: { default_enabled: true, threshold_pixels: 6, guide_stroke_width: 1, guide_color_token: '--reminder', group_member_targets: false },
    maximum_node_edge: 512,
    upload_task_id: 'upload',
    export_filename_fallback: 'canvas',
    export_extension: '.png',
    export: {
      padding_pixels: 20,
      default_background_color: '#ffffff',
      transparent_background_value: 'transparent',
      artboard_background_color: 'transparent',
      output_media_type: 'image/png',
      image_placeholder_color: '#e5e7eb',
      frame: {
        stroke_color: '#6366f1',
        stroke_width: 2,
        line_dash: [6, 4],
      },
      arrow: {
        head_length: 10,
        head_angle_divisor: 6,
      },
      pencil: {
        line_cap: 'round',
        line_join: 'round',
      },
    },
    viewport: {
      default_zoom: 1,
      default_pan_x: 0,
      default_pan_y: 0,
      minimum_zoom: 0.1,
      maximum_zoom: 3,
      zoom_step: 0.1,
      decimal_places: 2,
      percentage_multiplier: 100,
      fit_padding_pixels: 40,
      wheel_line_height_pixels: 16,
    },
    text: { default_color: '#000000', font_family: 'sans-serif', line_height: 1.2 },
    drawing: {
      default_node_size: 256,
      text_node_width: 200,
      text_node_height: 40,
      initial_shape_size: 2,
      pencil_minimum_points: 2,
      pencil_padding: 4,
      minimum_draw_size: 1,
      line_height: 4,
      minimum_committed_shape_size: 10,
      selection_drag_threshold_pixels: 4,
      brush_idle_commit_milliseconds: 1500,
    },
    history: {
      maximum_depth: 50,
      recent_limit: 10,
      snapshot_throttle_milliseconds: 500,
    },
    clipboard: {
      offset_x: 20,
      offset_y: 20,
    },
    style: {
      default_stroke_width: 2,
      default_stroke_color: '#333333',
      default_shape_stroke_color: '#6b7280',
      default_fill_color: 'transparent',
      default_background_color: '#ffffff',
      transparent_fill_value: 'transparent',
      preset_colors: ['#333333', '#ffffff'],
      stroke_widths: [1, 2, 5],
      font_sizes: [16, 24],
      default_font_size: 24,
      rgb_minimum: 0,
      rgb_maximum: 255,
      selection_border: '1px solid rgba(99, 102, 241, 0.5)',
      selection_background: 'rgba(99, 102, 241, 0.1)',
    },
    backgrounds: [
      {
        value: 'dots',
        label_key: 'toolbar.background.dots',
        pattern: 'dots',
        background_color: '#ffffff',
        pattern_color: '#e5e7eb',
        cell_size: 20,
        pattern_width: 1,
      },
    ],
    keyboard: {
      space_code: 'Space',
      space_tap_milliseconds: 250,
      recenter_key: '0',
      undo_key: 'z',
      redo_key: 'z',
      alternate_redo_key: 'y',
      copy_key: 'c',
      paste_key: 'v',
      select_all_key: 'a',
      group_key: 'g',
      layer_forward_key: ']',
      layer_backward_key: '[',
      select_tool_key: 'v',
      hand_tool_key: 'h',
      marquee_tool_key: 'm',
      lasso_tool_key: 'l',
      mask_tool_key: 'q',
      crop_tool_key: 'c',
      brush_tool_key: 'b',
      eraser_tool_key: 'e',
      text_tool_key: 't',
      shape_tool_key: 'u',
      shape_cycle: ['rectangle', 'ellipse', 'line', 'arrow'],
      zoom_tool_key: 'z',
      eyedropper_tool_key: 'i',
      brush_size_down_key: '[',
      brush_size_up_key: ']',
      brush_size_step_ratio: 0.1,
      swap_colors_key: 'x',
      default_colors_key: 'd',
      help_key: '?',
      mask_confirm_key: 'Enter',
      mask_exit_key: 'Escape',
      delete_keys: ['delete', 'backspace'],
      generate_shortcut: 'mod+enter',
      lock_shortcut: 'mod+/',
      merge_visible_shortcut: 'mod+alt+shift+e',
      cut_shortcut: 'mod+x',
      duplicate_shortcut: 'mod+j',
      deselect_shortcut: 'mod+d',
      invert_selection_shortcut: 'mod+shift+i',
      zoom_in_shortcut: 'mod+=',
      zoom_out_shortcut: 'mod+-',
      zoom_actual_shortcut: 'mod+1',
      zoom_selection_shortcut: 'mod+alt+0',
      layers_panel_shortcut: 'f7',
      history_panel_shortcut: 'alt+f9',
      nudge_pixels: 1,
      nudge_large_pixels: 10,
      reserved_shortcuts: ['mod+a', 'f7'],
    },
    relative_time: {
      milliseconds_per_minute: 60000,
      minutes_per_hour: 60,
      hours_per_day: 24,
      days_per_month: 30,
      locale: 'zh-CN',
    },
    processing_action: { calls_model: false, cost_hint: 'process_panel', group: 'produce' },
    stage_actions: [
      {
        id: 'inpaint',
        kinds: ['image'],
        group: 'produce',
        calls_model: true,
        cost_hint: 'approval_card',
        icon: 'wand-sparkles',
        label_key: 'ui.studio.stage.action.inpaint',
        hint_key: 'ui.studio.stage.action.inpaint_hint',
        target_key: 'ui.studio.stage.action.inpaint_target',
      },
      {
        id: 'enlarge',
        kinds: ['image'],
        group: 'view',
        calls_model: false,
        cost_hint: 'free',
        icon: 'maximize-2',
        label_key: 'ui.studio.stage.action.enlarge',
        hint_key: 'ui.studio.stage.action.enlarge_hint',
        target_key: 'ui.studio.stage.action.enlarge_target',
        shortcut: 'space_code',
      },
    ],
    tools: [
      {
        id: 'select',
        rail: 'canvas',
        section: 'select',
        icon: 'mouse-pointer-2',
        label_key: 'ui.canvas.toolbar.select',
        hint_key: 'ui.canvas.toolbar.select_hint',
        shortcut: 'select_tool_key',
      },
      {
        id: 'undo',
        rail: 'canvas',
        section: 'history',
        icon: 'undo-2',
        label_key: 'ui.canvas.toolbar.undo',
        hint_key: 'ui.canvas.toolbar.undo_hint',
        shortcut: 'undo_key',
        modifiers: ['mod'],
      },
      {
        id: 'texture',
        rail: 'viewer',
        section: 'views',
        icon: 'wallpaper',
        label_key: 'ui.studio.stage.view.texture',
        hint_key: 'ui.studio.stage.view.texture_hint',
      },
    ],
    panel_tabs: {
      width_pixels: 300,
      tabs: [
        { id: 'layers', icon: 'layers', label_key: 'ui.studio.panel.tab.layers', hint_key: 'ui.studio.panel.tab.layers_hint', shortcut: 'layers_panel_shortcut' },
        { id: 'details', icon: 'info', label_key: 'ui.studio.panel.tab.details', hint_key: 'ui.studio.panel.tab.details_hint' },
      ],
      kinds: [
        { kind: 'image', tabs: ['layers', 'details'], default: 'layers' },
        { kind: 'board', tabs: ['layers'], default: 'layers' },
        { kind: 'model', tabs: ['details'], default: 'details' },
      ],
    },
  },
  presentation: {
    messages: {
      forbidden: 'configured forbidden',
      project_not_found: 'configured missing',
      nodes_must_array: 'configured nodes',
      invalid_merge: 'configured merge',
      dimension_limit: 'configured dimension',
      merge_failed: 'configured merge failure',
      revision_conflict: 'conflict',
      internal_error: 'configured internal',
    },
    text: {},
  },
};

const routes = {
  projects: '/configured/projects',
  project: '/configured/projects/{wireProjectKey}',
  nodes: '/configured/projects/{wireProjectKey}/nodes',
  merge: '/configured/merge',
};

describe('Canvas Browser Client', () => {
  it('uses Schema routes and fields while exposing stable project semantics', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify([{
      wireId: 'project/a b',
      wireName: 'Configured board',
      wireSession: 'session-1',
      wireBackground: 'grid',
      wireCreated: '2026-07-26T00:00:00Z',
      wireUpdated: '2026-07-26T01:00:00Z',
      wireNodeCount: { wireNodes: 3 },
    }]), { status: 200 }));
    const client = createCanvasBrowserClient({
      schema: canvasSchema,
      routes,
      fetcher,
    });

    await expect(client.listProjects()).resolves.toEqual([{
      id: 'project/a b',
      name: 'Configured board',
      sessionId: 'session-1',
      bgStyle: 'grid',
      createdAt: '2026-07-26T00:00:00Z',
      updatedAt: '2026-07-26T01:00:00Z',
      nodeCount: 3,
    }]);
    expect(fetcher).toHaveBeenCalledWith(
      '/configured/projects',
      expect.objectContaining({ method: 'GET' }),
    );
  });

  it('encodes mutations with Schema fields and route parameters', async () => {
    const responses = [
      new Response(JSON.stringify({
        wireId: 'project/a b',
        wireUser: 'owner-1',
        wireName: 'New board',
        wireSession: 'session-1',
        wireViewport: '{}',
        wireBackground: 'grid',
        wireWasCreated: true,
        wireCreated: '2026-07-26T00:00:00Z',
        wireUpdated: '2026-07-26T00:00:00Z',
      }), { status: 201 }),
      new Response(JSON.stringify({ wireOK: true, wireCount: 1 }), {
        status: 200,
      }),
    ];
    const fetcher = vi.fn(async () => responses.shift()!);
    const client = createCanvasBrowserClient({
      schema: canvasSchema,
      routes,
      fetcher,
    });

    await expect(client.createProject('New board', 'session-1')).resolves.toMatchObject({ created: true });
    await expect(client.replaceNodes('project/a b', [{
      id: 'node-1',
      type: 'image',
      x: 1,
      y: 2,
      width: 3,
      height: 4,
      zIndex: 5,
      data: '{"imageUrl":"asset"}',
      visible: true,
      locked: false,
    }])).resolves.toEqual({ ok: true, count: 1 });

    expect(fetcher.mock.calls[0]).toEqual([
      '/configured/projects',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ wireName: 'New board', wireSession: 'session-1' }),
      }),
    ]);
    expect(fetcher.mock.calls[1]).toEqual([
      '/configured/projects/project%2Fa%20b/nodes',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({
          wireNodes: [{
            wireId: 'node-1',
            wireType: 'image',
            wireX: 1,
            wireY: 2,
            wireWidth: 3,
            wireHeight: 4,
            wireZ: 5,
            wireData: '{"imageUrl":"asset"}',
            wireVisible: true,
            wireLocked: false,
          }],
        }),
      }),
    ]);
  });
  it('only trusts the boolean create-response flag, never a read or a truthy string', async () => {
    const project = {
      wireId: 'p', wireUser: 'u', wireName: 'n', wireSession: 's', wireViewport: '{}', wireBackground: 'grid',
      wireCreated: '', wireUpdated: '', wireNodes: [],
    };
    const replies = [false, undefined, 'true', true].map(created => new Response(JSON.stringify({ ...project, wireWasCreated: created })));
    const client = createCanvasBrowserClient({ schema: canvasSchema, routes, fetcher: async () => replies.shift()! });
    for (let i = 0; i < 3; i++) await expect(client.createProject(undefined, 's')).resolves.toMatchObject({ created: false });
    expect(await client.getProject('p')).not.toHaveProperty('created');
  });

  it('sends and reads the canvas pixel size through Schema fields (#246)', async () => {
    const project = {
      wireId: 'p', wireUser: 'u', wireName: 'n', wireSession: 's', wireViewport: '{}', wireBackground: 'grid',
      wireCreated: '2026-10-19T00:00:00Z', wireUpdated: '2026-10-19T00:00:00Z', wireNodes: [],
    };
    const responses = [
      new Response(JSON.stringify({ ...project, wireCanvasWidth: 3840, wireCanvasHeight: 2160, wireCoordinates: 2 }), { status: 200 }),
      new Response(JSON.stringify({ ...project, wireCanvasWidth: null, wireCanvasHeight: null, wireCoordinates: null }), { status: 200 }),
      new Response(JSON.stringify({ ...project, wireCanvasWidth: 1024.5, wireCanvasHeight: 1024, wireCoordinates: 2 }), { status: 200 }),
      new Response(JSON.stringify({ wireOK: true, wireCount: 0 }), { status: 200 }),
    ];
    const fetcher = vi.fn(async () => responses.shift()!);
    const client = createCanvasBrowserClient({ schema: canvasSchema, routes, fetcher });
    await expect(client.getProject('p')).resolves.toMatchObject({ frame: { width: 3840, height: 2160, coordinateVersion: 2 } });
    // A legacy canvas (no frame yet) and a malformed one both read as null: the board migrates it.
    await expect(client.getProject('p')).resolves.toMatchObject({ frame: null });
    await expect(client.getProject('p')).resolves.toMatchObject({ frame: null });
    await client.replaceNodes('p', [], 3, undefined, { width: 2048, height: 1152, coordinateVersion: 2 });
    expect(JSON.parse(String((fetcher.mock.calls[3] as unknown[])[1] && ((fetcher.mock.calls[3] as unknown[])[1] as RequestInit).body))).toEqual({
      wireNodes: [], 7: 3, wireCanvasWidth: 2048, wireCanvasHeight: 1152, wireCoordinates: 2,
    });
  });
});
