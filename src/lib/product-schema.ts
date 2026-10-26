/** Frozen static model profile safety budgets shared by runtime and Offering parameters. */
export interface ModelInputLimits {
  max_bytes: number;
  max_json_bytes: number;
  max_depth: number;
  max_elements: number;
  max_vertices: number;
  max_triangles: number;
  max_nodes: number;
  max_work: number;
  max_image_pixels: number;
  max_total_image_pixels: number;
}

import type { PublicHarnessSchema } from '@/lib/harness';
import { activeLocale } from '@/lib/i18n/locale';

export interface ProductSchema {
  revision: string;
  /**
   * UI locales. The server projects the whole Schema in one locale per request
   * (`active_locale`); the other locales' copy never reaches the browser.
   * Absent on a single-locale Schema.
   */
  localization?: {
    default_locale: string;
    active_locale?: string;
    locales: Array<{ id: string; label: string }>;
    preference: { cookie: string; max_age_seconds: number };
    /** Every locale's text of a message saved into a person's data as an untouched default. */
    persisted_default_texts?: Record<string, string[]>;
  };
  agent_configuration: {
    routes: { admin: string; effective: string; validate: string; publish: string; restore: string; reset: string };
    limits: { max_prompt_characters: number; max_skill_count: number; max_skill_prompt_characters: number };
    mcp_presets: Array<{ id: string; name: string; description: string; tool_names?: string[]; tools?: Array<{ name: string; description: string }>; protocol_version?: string; transport?: string }>;
    ui: Record<string, string>;
  };
  /**
   * #322: the administrator's LLM settings page. Absent on older servers: the page shows nothing to manage.
   * Connection values (addresses, keys) never reach the browser; the page only learns whether each is set.
   */
  llm_settings?: {
    routes: { admin: string; publish: string; restore: string; reset: string; test: string };
    ui: Record<string, string>;
  };
  runtime: {
    /**
     * #329 note 99647: the one text that says only the first reference pictures are used — the general form and
     * the form for a model that takes one picture. The task API, the approval card and the finished message use it too.
     * Absent on older servers: the composer says nothing.
     */
    generation_messages?: { reference_limit?: string; reference_limit_one?: string };
    http: {
      routes: {
        home: string;
        liveness: string;
        readiness: string;
        product_schema: string;
        chat_skills: string;
        client_errors?: string;
        chat_session_approval: string;
        chat_session_notes?: string;
        chat_session_pick?: string;
        chat_session_agent_scope?: string;
        /** #322: the session's thinking level (GET/PUT). Absent on older servers: the picker is hidden. */
        chat_session_reasoning?: string;
        /** #322: the session's context usage and compaction records (GET). */
        chat_session_context?: string;
        /** #322: compact the session's context now (POST). */
        chat_session_context_compaction?: string;
        /** #322: the session's chat model (GET/PUT). Absent on older servers: the model picker is hidden. */
        chat_session_model?: string;
        chat_session_harness?: string;
        chat_session_lineage?: string;
        chat_session_pending_approvals?: string;
        /** #321: the session's in-chat choice cards. */
        chat_session_choices?: string;
        chat_session_three_views?: string;
        chat_session_three_view_item?: string;
        provider_asset: string;
        offerings: string;
        assets: string;
        asset_item: string;
        asset_model_preview?: string;
        model_captures?: string;
        model_capture_item?: string;
        scene_objects?: string;
        scene_object_item?: string;
        scene_object_representations?: string;
        scene_object_apply?: string;
        scene_snapshot?: string;
        render_manifests?: string;
        render_manifest_item?: string;
        skill_runs?: string;
        skill_run_item?: string;
        skill_run_step?: string;
        skill_run_review?: string;
        tasks: string;
        task_prepare: string;
        task_grants: string;
        task_grant_decision: string;
        task_item: string;
        processing_plans: string;
        processing_plan_item: string;
        processing_plan_retry: string;
        processing_plan_resume: string;
        processing_plan_reconcile?: string;
        task_cancel: string;
        task_retry?: string;
        task_recovery?: string;
        task_recovery_prepare?: string;
        task_refetch?: string;
        task_events: string;
        task_asset: string;
        model_formats?: string;
        model_formats_complete?: string;
        model_archive?: string;
        account_usage: string;
        account_resources: string;
        agent_params: string;
        agent_bind: string;
        agent_consent: string;
      };
      legacy_routes: {
        health: string;
        providers: string;
        tasks: string;
        version: string;
        asset_upload: string;
        asset_item: string;
        prompt_enhance: string;
        auth_login: string;
        auth_mock: string;
        auth_session: string;
        cache_status: string;
        generated_image: string;
        chat_sessions: string;
        chat_session: string;
        chat_messages: string;
        chat_message: string;
        chat_stream: string;
        chat_turn_cancel: string;
        provider_balance: string;
        canvas_projects: string;
        canvas_project: string;
        canvas_nodes: string;
        canvas_merge: string;
        task_stream: string;
        admin_users: string;
        admin_user: string;
        admin_audit_logs: string;
        admin_generations: string;
        admin_generation: string;
        admin_generation_download: string;
        admin_usage: string;
        admin_balance_summary: string;
        admin_user_assets: string;
        admin_backfill_status: string;
        meshy_proxy_asset: string;
      };
      legacy_provider_balance: {
        accounts_field: string;
        provider_field: string;
        balance_field: string;
        error_field: string;
        accounts: Array<{
          account_id: string;
          label?: string;
          consumption_estimates?: Array<{
            capability: string;
            credits: number;
          }>;
        }>;
      };
    };
    input_assets: {
      model_profile?: {
        id: string;
        revision: string;
        label: string;
        description: string;
        asset_type: string;
        extensions: string[];
        media_types: string[];
        preview_media_types?: string[];
        upload_media_type: string;
        limits: ModelInputLimits;
      };
      upload_field_name: string;
      upload_max_bytes: number;
      default_asset_type: string;
      api: {
        fields: {
          id: string;
          asset_id: string;
          asset_type: string;
          stored_sha256: string;
          original_filename: string;
          media_type: string;
          file_size: string;
          data_uri: string;
          created_at: string;
        };
      };
      type_rules: Array<{
        asset_type: string;
        media_types?: string[];
        extensions?: string[];
      }>;
    };
    cache: {
      bytes_per_megabyte: number;
      fields: {
        active_downloads: string;
        active_items: string;
        filename: string;
        elapsed: string;
        task_id: string;
        task_type: string;
        provider_id: string;
        asset_kind: string;
        total_cached_files: string;
        total_cache_size_mb: string;
      };
    };
    asset_delivery: {
      proxy: {
        api: {
          query: {
            url: string;
            filename: string;
            task_id: string;
            size: string;
          };
        };
        security: {
          allowed_schemes: string[];
          allowed_hosts?: string[];
        };
        thumbnail: { request_value: string };
      };
      /** #290: the query that asks a task asset route for its downscaled preview. */
      asset_preview?: { query: string; request_value: string };
    };
    frontend: {
      model_processing_capabilities: string[];
      image_upload: {
        maximum_pixels: number;
        conversions: Array<{
          source_media_types?: string[] | null;
          source_extensions?: string[] | null;
          target_media_type: string;
        }>;
      };
      web_asset_prefix: string;
      favicon_path: string;
      client_routes: {
        account: string;
        home: string;
        login: string;
      };
      provider_metadata_cache_ttl_milliseconds: number;
      task_history_refresh_milliseconds: number;
      task_history_deduping_milliseconds: number;
      task_history_revalidate_on_focus: boolean;
      task_history_keep_previous_data: boolean;
      balance_refresh_milliseconds: number;
      cache_status_refresh_milliseconds: number;
      default_prompt_max_length: number;
      default_reference_image_min: number;
      default_reference_image_max: number;
      product_version_refresh_milliseconds: number;
      task_submission_cooldown_seconds: number;
      task_queue_maximum: number;
      batch_download_interval_milliseconds: number;
      task_identifier_preview_characters: number;
      countdown_tick_milliseconds: number;
      task_polling_interval_milliseconds: number;
      harness_refresh_milliseconds: number;
      harness_save_debounce_milliseconds: number;
      session_menu_page_size: number;
      session_menu_search_debounce_milliseconds: number;
      session_title_refresh_attempts: number;
      session_title_refresh_interval_milliseconds: number;
      chat_message_page_size: number;
      /** #329: generation models no longer offered and the model that replaces each. */
      retired_generation_models?: Array<{ provider_id: string; model_id: string; model_name: string; successor_model_id: string }>;
      display_locale: string;
      task_api_prefix: string;
      spinner_frame_milliseconds: number;
      percentage_multiplier: number;
      percentage_suffix: string;
      advanced_model_form_feature: string;
      advanced_model_default_mode: 'regular' | 'parts';
      task_progress: {
        fallback_state: string;
        initial_state: string;
        not_found_state: string;
        milliseconds_per_second: number;
        seconds_per_minute: number;
        minutes_per_hour: number;
        hour_decimal_places: number;
      };
      toaster: {
        position:
          | 'top-left'
          | 'top-center'
          | 'top-right'
          | 'bottom-left'
          | 'bottom-center'
          | 'bottom-right';
        rich_colors: boolean;
      };
    };
  };
  content: {
    brand: {
      title: string;
      metadata_title: string;
      metadata_description: string;
      version_prefix: string;
    };
    service: {
      id: string;
      name: string;
      version: string;
    };
    navigation: NavigationItemSchema[];
    asset_kinds: ValueLabelSchema[];
    asset_kind_patterns: AssetKindPatternSchema[];
    messages: Record<string, string>;
  };
  chat: ChatProductSchema;
  canvas: CanvasProductSchema;
  task_history: TaskHistoryProductSchema;
  task_stream: TaskStreamProductSchema;
  administration: AdministrationProductSchema;
  capabilities: CapabilitySchema[];
  task_states: TaskStateSchema[];
  audit_actions: ValueLabelSchema[];
  /** The formats a finished 3D task is expected to deliver, in presentation order. */
  delivery_formats?: Array<{ id: string; label: string }>;
  authentication: {
    api: AuthenticationAPISchema;
    public_paths: string[];
    messages: AuthenticationMessagesSchema;
    login: LoginSchema;
  };
}

export interface ProviderWebhookProductSchema {
  id: string;
  provider_id: string;
  route: string;
  path_parameter: string;
  path_secret_setting: string;
  key_setting: string;
  key_headers: string[];
  payload_job_id_fields: string[];
  event_type: string;
  api: {
    timestamp_layout: string;
    fields: {
      error: string;
      received: string;
      timestamp: string;
      status: string;
      service: string;
    };
    values: {
      not_found: string;
      unauthorized: string;
      bad_request: string;
      unavailable: string;
      ok: string;
      service: string;
    };
    statuses: {
      ok: number;
      bad_request: number;
      unauthorized: number;
      not_found: number;
      unavailable: number;
    };
  };
}

export interface AdministrationProductSchema {
  navigation: Array<{
    key: string;
    href: string;
    label: string;
    icon: string;
    match: 'exact' | 'prefix';
  }>;
  users: {
    api: {
      fields: {
        data: string;
        success: string;
        error: string;
        id: string;
        username: string;
        password: string;
        nickname: string;
        name: string;
        email: string;
        is_admin: string;
        is_active: string;
        created_at: string;
        last_login: string;
        open_id: string;
      };
      path_parameters: {
        user_id: string;
      };
    };
    limits: {
      username_minimum_characters: number;
      password_minimum_characters: number;
    };
    messages: {
      user_not_found: string;
      self_deactivate: string;
      self_delete: string;
      duplicate_username: string;
      invalid_username: string;
      invalid_password: string;
      invalid_request: string;
      internal_error: string;
    };
    presentation: {
      date_locale: string;
      loading_rows: number;
      date_time_options: Partial<Record<
        'weekday' | 'year' | 'month' | 'day' | 'hour' | 'minute' | 'second',
        'numeric' | '2-digit' | 'long' | 'short' | 'narrow'
      >>;
      empty_placeholder: string;
      text: Record<string, string>;
    };
  };
  audit_logs: {
    api: {
      fields: {
        data: string;
        error: string;
        total: string;
        page: string;
        page_size: string;
        id: string;
        action: string;
        user_id: string;
        user_name: string;
        resource_type: string;
        resource_id: string;
        detail: string;
        ip_address: string;
        user_agent: string;
        created_at: string;
      };
      query: {
        page: string;
        page_size: string;
        user_id: string;
        action: string;
        from: string;
        to: string;
      };
    };
    limits: {
      default_page: number;
      default_page_size: number;
      maximum_page_size: number;
    };
    messages: {
      internal_error: string;
    };
    presentation: {
      default_recent_days: number;
      loading_rows: number;
      user_id_preview_characters: number;
      resource_id_preview_characters: number;
      preview_suffix: string;
      empty_placeholder: string;
      date_locale: string;
      date_time_options: Partial<Record<
        'weekday' | 'year' | 'month' | 'day' | 'hour' | 'minute' | 'second',
        'numeric' | '2-digit' | 'long' | 'short' | 'narrow'
      >>;
      action_badge_classes: Record<string, string>;
      action_prefix_badge_classes: Record<string, string>;
      default_badge_class: string;
      text: Record<string, string>;
    };
  };
  generations: {
    api: {
      fields: {
        data: string;
        error: string;
        total: string;
        page: string;
        page_size: string;
        id: string;
        user_id: string;
        user_name: string;
        user_email: string;
        task_type: string;
        task_id: string;
        estimated_credits: string;
        task_status: string;
        params: string;
        result_urls: string;
        thumbnail_url: string;
        created_at: string;
        finished_at: string;
        ledger: string;
        task_cache: string;
        /** Optional (#276); absent in older Schemas. */
        input_derivations?: string;
        status: string;
        provider_id: string;
        cache_data: string;
        cached_at: string;
        updated_at: string;
        redirect: string;
      };
      query: {
        page: string;
        page_size: string;
        user_id: string;
        task_type: string;
        status: string;
        from: string;
        to: string;
      };
      path_parameters: {
        task_id: string;
      };
    };
    limits: {
      default_page: number;
      default_page_size: number;
      maximum_page_size: number;
      default_recent_days: number;
    };
    assets: {
      download_filename_template: string;
      remote_thumbnail_url_query: string;
      remote_thumbnail_task_id_query: string;
      preview_image_extensions: string[];
    };
    messages: {
      task_not_found: string;
      cache_not_found: string;
      no_downloadable_assets: string;
      all_downloads_failed: string;
      invalid_request: string;
      internal_error: string;
      download_failed: string;
      unknown_download_response: string;
    };
    presentation: {
      loading_rows: number;
      user_id_preview_characters: number;
      task_id_preview_characters: number;
      preview_suffix: string;
      empty_placeholder: string;
      date_locale: string;
      date_time_options: Partial<Record<
        'weekday' | 'year' | 'month' | 'day' | 'hour' | 'minute' | 'second',
        'numeric' | '2-digit' | 'long' | 'short' | 'narrow'
      >>;
      text: Record<string, string>;
    };
  };
  user_assets: {
    api: {
      envelope_fields: {
        error: string;
        user: string;
        summary: string;
        uploads: string;
        generations: string;
        downloads: string;
      };
      page_fields: {
        data: string;
        total: string;
        page: string;
        page_size: string;
      };
      user_fields: {
        id: string;
        username: string;
        nickname: string;
        email: string;
        name: string;
        is_admin: string;
        is_active: string;
        created_at: string;
        last_login: string;
      };
      summary_fields: {
        total_uploads: string;
        total_generations: string;
        total_downloads: string;
        total_storage_bytes: string;
      };
      upload_fields: {
        id: string;
        asset_type: string;
        original_filename: string;
        stored_filename: string;
        mime_type: string;
        file_size: string;
        storage_path: string;
        created_at: string;
      };
      generation_fields: {
        id: string;
        task_type: string;
        task_id: string;
        task_status: string;
        result_urls: string;
        params: string;
        estimated_credits: string;
        thumbnail_url: string;
        created_at: string;
        finished_at: string;
      };
      download_fields: {
        id: string;
        action: string;
        resource_type: string;
        resource_id: string;
        detail: string;
        created_at: string;
      };
      query: {
        page_size: string;
        uploads_page: string;
        generations_page: string;
        downloads_page: string;
        from: string;
        to: string;
      };
      path_parameters: {
        user_id: string;
      };
    };
    limits: {
      default_page: number;
      default_page_size: number;
      maximum_page_size: number;
      frontend_page_size: number;
      default_recent_days: number;
    };
    filters: {
      image_asset_type: string;
      model_asset_type: string;
      image_media_type_prefix: string;
      generated_image_resource_type: string;
      download_actions: Array<{
        action: string;
        label: string;
        text_class: string;
        dot_class: string;
      }>;
    };
    messages: {
      user_not_found: string;
      internal_error: string;
    };
    presentation: {
      loading_rows: number;
      filename_preview_characters: number;
      task_id_preview_characters: number;
      resource_id_preview_characters: number;
      preview_suffix: string;
      empty_placeholder: string;
      date_locale: string;
      date_time_options: Partial<Record<
        'weekday' | 'year' | 'month' | 'day' | 'hour' | 'minute' | 'second',
        'numeric' | '2-digit' | 'long' | 'short' | 'narrow'
      >>;
      bytes_per_unit: number;
      size_decimal_places: number;
      size_units: string[];
      asset_type_labels: Record<string, string>;
      text: Record<string, string>;
    };
  };
  usage: {
    api: {
      balance_fields: {
        current_balance: string;
        history: string;
        balance: string;
        captured_at: string;
      };
      fields: {
        data: string;
        error: string;
        user_id: string;
        user_name: string;
        user_email: string;
        total_tasks: string;
        succeeded_tasks: string;
        failed_tasks: string;
        estimated_credits: string;
        last_active_at: string;
        by_type: string;
        total: string;
      };
      query: {
        days: string;
        start_date: string;
        end_date: string;
      };
    };
    defaults: {
      days: number;
      custom_range_days: number;
    };
    messages: {
      invalid_range: string;
      internal_error: string;
    };
    presentation: {
      quick_ranges: Array<{ days: number; label: string }>;
      loading_rows: number;
      empty_placeholder: string;
      approximation_prefix: string;
      range_separator: string;
      date_separator: string;
      date_locale: string;
      date_time_options: Partial<Record<
        'weekday' | 'year' | 'month' | 'day' | 'hour' | 'minute' | 'second',
        'numeric' | '2-digit' | 'long' | 'short' | 'narrow'
      >>;
      text: Record<string, string>;
    };
  };
}

export interface ProviderDiscoveryProductSchema {
  api: {
    query: {
      capability: string;
      domain: string;
      projection: string;
    };
    projection_values: {
      extended: string;
    };
    envelope_fields: {
      providers: string;
      default_model: string;
    };
    provider_fields: {
      id: string;
      name: string;
      capabilities: string;
      domain: string;
      features: string;
      parameter_schemas: string;
      input_schemas: string;
      constraints: string;
      recommended: string;
      estimated_credits: string;
      provider_key: string;
      /** 安全派生的额外积分估算线名（extended 投影） */
      estimated_extra_credits?: string;
    };
    parameter_fields: {
      validation?: string;
      name: string;
      label: string;
      type: string;
      options: string;
      default: string;
      required: string;
      minimum: string;
      maximum: string;
      step: string;
      description: string;
      disabled_when: string;
      visible_when: string;
      /** 参数级"参与额外计费"安全派生标记线名（extended 投影） */
      cost_extra?: string;
      forced_when: string;
      notices: string;
    };
    option_fields: {
      value: string;
      label: string;
      group: string;
    };
    condition_fields: {
      parameter: string;
      operator: string;
      value: string;
    };
    notice_fields: {
      when: string;
      message_key: string;
      tone: string;
    };
    forced_value_fields: {
      when: string;
      value: string;
    };
    input_fields: {
      uploaded_model?: string;
      task_source_kind?: string;
      task_offerings?: string;
      task_capabilities?: string;
      same_provider?: string;
      name: string;
      label: string;
      type: string;
      media_types: string;
      extensions: string;
      max_bytes: string;
      min_items: string;
      max_items: string;
    };
    constraint_fields: {
      prompt_max_length: string;
      negative_prompt_max_length: string;
      reference_image_min: string;
      reference_image_max: string;
      reference_image_formats: string;
      reference_image_max_size: string;
      polycount_min: string;
      polycount_max: string;
      output_sizes: string;
    };
  };
  legacy_models: Array<{
    model_id: string;
    provider_key: string;
    name?: string;
    recommended?: boolean;
    parameter_projections?: Record<
      string,
      {
        order?: string[];
        omit?: string[];
        fields?: Record<
          string,
          {
            omit?: string[];
            required?: boolean;
          }
        >;
      }
    >;
    constraint_projections?: Record<
      string,
      {
        omit?: string[];
        overrides?: {
          prompt_max_length?: number;
          negative_prompt_max_length?: number;
          reference_image_min?: number;
          reference_image_max?: number;
          reference_image_max_size?: number;
          polycount_min?: number;
          polycount_max?: number;
        };
      }
    >;
  }>;
  legacy_constraint_option_labels: Record<string, Record<string, string>>;
}

export interface TaskAttachmentPresentation {
  title: string;
  role_labels: Record<string, string>;
  fallback_role: string;
  format: string;
  media_type: string;
  size: string;
  unknown_size: string;
  download: string;
  no_preview: string;
}

export interface PrintReportPresentation {
  media_type: string;
  format: string;
  max_bytes: number;
  title: string;
  loading: string;
  load_failed: string;
  retry: string;
  unsupported: string;
  diagnostic_notice: string;
  unknown_value: string;
  not_reported: string;
  kind_labels: Record<string, string>;
  status_labels: Record<string, string>;
  details_status_labels: Record<string, string>;
  field_labels: Record<string, string>;
  value_labels: Record<string, string>;
}

export interface RigCheckReportPresentation {
  media_type: string;
  format: string;
  max_bytes: number;
  title: string;
  loading: string;
  load_failed: string;
  retry: string;
  unsupported: string;
  limitation: string;
  field_labels: Record<string, string>;
  boolean_labels: Record<string, string>;
  species_labels: Record<string, string>;
}

export interface TaskHistoryProductSchema {
  asset_recovery?: {
    action: string;
    description: string;
    eligibility_loading: string;
    eligibility_failed: string;
    preparing: string;
    prepare_failed: string;
    awaiting_approval: string;
    approve: string;
    deny: string;
    submitting: string;
    submit_failed: string;
    submitted: string;
    open_recovery_task: string;
    open_source_task: string;
    source_label: string;
  };
  assets: { preview_roles: string[]; attachments: TaskAttachmentPresentation; print_report: PrintReportPresentation; rig_check_report: RigCheckReportPresentation };
  api: {
    query: {
      page: string;
      page_size: string;
      recent: string;
      type: string;
      search: string;
      status: string;
      hide_failures: string;
    };
    fields: {
      data: string;
      total: string;
      page: string;
      page_size: string;
      id: string;
      task_type: string;
      status: string;
      progress: string;
      thumbnail_url: string;
      created_at: string;
      prompt: string;
    };
  };
  values: {
    all_capability: string;
    enabled: string;
  };
  labels: {
    /** 仅展示的历史模型名（不激活任何 Offering） */
    models: Record<string, string>;
    /** 按 capability 作用域的任务参数字段展示标签 */
    parameters: Record<string, Record<string, string>>;
  };
  /** 按 capability 作用域的任务详情隐藏字段 */
  hidden_fields: Record<string, string[]>;
}

export interface TaskStreamProductSchema {
  aliases: Array<{
    external_type: string;
    capability: string;
  }>;
}

export interface CanvasProductSchema {
  api: {
    fields: {
      id: string;
      user_id: string;
      name: string;
      session_id: string;
      viewport: string;
      bg_style: string;
      /** Whether the create call made this session canvas; absent from reads. */
      created: string;
      placed_results?: string;
      /** #246: the canvas pixel size and coordinate version, saved with the nodes. */
      canvas_width: string;
      canvas_height: string;
      coordinate_version: string;
      created_at: string;
      updated_at: string;
      nodes: string;
      count: string;
      node_count: string;
      ok: string;
      error: string;
      project_id: string;
      type: string;
      x: string;
      y: string;
      width: string;
      height: string;
      z_index: string;
      data: string;
      visible: string;
      locked: string;
      asset_id: string;
      url: string;
      image_url: string;
      revision: string;
    };
    path_parameters: {
      project_id: string;
    };
  };
  /** coordinate_version: the canvas coordinate contract (2 = one world unit is one pixel). */
  defaults: { bg_style: string; coordinate_version: number };
  limits: {
    maximum_merge_nodes: number;
    maximum_bitmap_nodes: number;
    /** Also the largest canvas edge in pixels (#246). */
    maximum_dimension: number;
    /** The smallest canvas edge in pixels (#246). */
    minimum_dimension: number;
  };
  frontend: {
    focus_delay_milliseconds: number;
    auto_save_delay_milliseconds: number;
    saved_indicator_milliseconds: number;
    context_menu_bind_delay_milliseconds: number;
    permission_redirect_path: string;
    initial_node_x: number;
    initial_node_y: number;
    node_gap_pixels: number;
    minimum_node_size: number;
    maximum_node_edge: number;
    model_capture_long_edge_pixels: number;
    to_3d_long_edge_pixels: number;
    /** #246: the bitmap layer a new canvas starts with; fill is the transparent value or a #rrggbb colour. */
    initial_layer: {
      enabled: boolean;
      fill: string;
      /** Largest bitmap (width*height) the brush paints the layer in; a larger canvas gets a smaller bitmap. */
      maximum_bitmap_pixels: number;
    };
    artboard: {
      /** #246: where a canvas resize keeps content (its pixel size and position relative to this anchor). */
      resize: { default_anchor: string; anchors: string[] };
      /** The canvas centre in world coordinates; the canvas is its pixel size around it. */
      center_x: number;
      center_y: number;
      /** Long edge (world units) of a canvas saved before coordinate version 2; read only to migrate it. */
      legacy_max_edge: number;
      /** The size of a new canvas when no 2D model names one. */
      default_width: number;
      default_height: number;
      /** The pixels a ratio-only model (aspect ratio, no pixel size) produces for each ratio. */
      ratio_pixels: Record<string, { width: number; height: number }>;
      /** Per resolution tier of an Offering parameter (Gemini image_size 1K/2K/4K): parameter -> tier -> ratio -> pixels. */
      ratio_pixel_tiers?: Record<string, Record<string, Record<string, { width: number; height: number }>>>;
      /** The dashed outline, in screen pixels at any zoom (drawn with stroke scaling off). */
      frame_stroke_width: number;
      frame_line_dash: number[];
      /** The size label inside the frame's top-left corner (#282), in screen pixels at any zoom. */
      label: {
        common_ratios: string[];
        font_size_pixels: number;
        /** Inset from the dashed frame. */
        offset_pixels: number;
        /** CSS custom property name, such as --muted-foreground. */
        color_token: string;
        /** The plate under the text that keeps it readable over pictures. */
        padding_pixels: number;
        background_token: string;
        background_opacity: number;
      };
      /** The translucent veil over everything outside the dashed frame (no pointer events). */
      outside_dim: {
        color: string;
        opacity: number;
      };
      /** #246: the checkerboard inside the frame while the exported canvas background is transparent. */
      transparency: {
        /** One square in screen pixels, whatever the zoom. */
        cell_pixels: number;
        light_color: string;
        dark_color: string;
        opacity: number;
      };
    };
    /** Pixel brush bounds (also the quick-mask brush), in canvas pixels. */
    brush: {
      minimum_size: number;
      maximum_size: number;
      default_size: number;
    };
    /** Marching ants and tint of the canvas-level selection. */
    selection: {
      ants_dash: number[];
      ants_tick_milliseconds: number;
      tint_color: string;
      tint_opacity: number;
      preview_max_edge: number;
    };
    /** #310: tint of the quick-mask area on the board, laid over the merged area once (not the selection tint). */
    mask: {
      /** CSS custom property name, such as --info. */
      tint_color_token: string;
      tint_opacity: number;
    };
    snap: {
      default_enabled: boolean;
      threshold_pixels: number;
      guide_stroke_width: number;
      /** CSS custom property name, such as --reminder. */
      guide_color_token: string;
      /** A group snaps as one box; true also keeps each member's own edges as secondary targets. */
      group_member_targets: boolean;
    };
    upload_task_id: string;
    export_filename_fallback: string;
    export_extension: string;
    export: {
      padding_pixels: number;
      default_background_color: string;
      transparent_background_value: string;
      /** What every artboard picture is drawn over: transparent_background_value keeps empty canvas transparent (#271), or #rrggbb. */
      artboard_background_color: string;
      output_media_type: string;
      image_placeholder_color: string;
      frame: {
        stroke_color: string;
        stroke_width: number;
        line_dash: number[];
      };
      arrow: {
        head_length: number;
        head_angle_divisor: number;
      };
      pencil: {
        line_cap: CanvasLineCap;
        line_join: CanvasLineJoin;
      };
    };
    viewport: {
      default_zoom: number;
      default_pan_x: number;
      default_pan_y: number;
      minimum_zoom: number;
      maximum_zoom: number;
      zoom_step: number;
      decimal_places: number;
      percentage_multiplier: number;
      fit_padding_pixels: number;
      /** Pixels per line for line-mode wheel deltas when panning. */
      wheel_line_height_pixels: number;
    };
    /** Text layers: the canvas and the exported picture read the same family, line height and fixed default colour. */
    text: {
      default_color: string;
      font_family: string;
      line_height: number;
    };
    drawing: {
      default_node_size: number;
      text_node_width: number;
      text_node_height: number;
      initial_shape_size: number;
      pencil_minimum_points: number;
      pencil_padding: number;
      minimum_draw_size: number;
      line_height: number;
      minimum_committed_shape_size: number;
      selection_drag_threshold_pixels: number;
      /** Idle time after the last brush stroke before the local bitmap is uploaded as one step. */
      brush_idle_commit_milliseconds: number;
    };
    history: {
      maximum_depth: number;
      recent_limit: number;
      snapshot_throttle_milliseconds: number;
    };
    clipboard: {
      offset_x: number;
      offset_y: number;
    };
    style: {
      default_stroke_width: number;
      default_stroke_color: string;
      default_shape_stroke_color: string;
      default_fill_color: string;
      /** The background colour D restores (the foreground is default_stroke_color). */
      default_background_color: string;
      transparent_fill_value: string;
      preset_colors: string[];
      stroke_widths: number[];
      /** Font size presets of the selection controls. */
      font_sizes: number[];
      default_font_size: number;
      rgb_minimum: number;
      rgb_maximum: number;
      selection_border: string;
      selection_background: string;
    };
    backgrounds: Array<{
      value: string;
      label_key: string;
      pattern: 'dots' | 'grid' | 'solid';
      background_color: string;
      pattern_color: string;
      cell_size: number;
      pattern_width: number;
    }>;
    keyboard: {
      space_code: string;
      space_tap_milliseconds: number;
      recenter_key: string;
      undo_key: string;
      redo_key: string;
      alternate_redo_key: string;
      copy_key: string;
      paste_key: string;
      select_all_key: string;
      group_key: string;
      /** Layer order keys (with Ctrl/Cmd; Shift = to the edge). */
      layer_forward_key: string;
      layer_backward_key: string;
      /** Photoshop tool keys: one bare key per tool. */
      select_tool_key: string;
      hand_tool_key: string;
      marquee_tool_key: string;
      lasso_tool_key: string;
      mask_tool_key: string;
      crop_tool_key: string;
      /** Shift on the brush key swaps brush and pencil. */
      brush_tool_key: string;
      eraser_tool_key: string;
      text_tool_key: string;
      /** Shift on the shape key steps through shape_cycle. */
      shape_tool_key: string;
      shape_cycle: Array<'rectangle' | 'ellipse' | 'line' | 'arrow'>;
      zoom_tool_key: string;
      eyedropper_tool_key: string;
      /** [ and ] resize the brush by brush_size_step_ratio of its current size. */
      brush_size_down_key: string;
      brush_size_up_key: string;
      brush_size_step_ratio: number;
      /** Swap foreground / background colour (paint / erase in the quick mask); restore the default pair. */
      swap_colors_key: string;
      default_colors_key: string;
      /** Opens the keyboard-shortcut panel. */
      help_key: string;
      mask_confirm_key: string;
      mask_exit_key: string;
      delete_keys: string[];
      /** "mod+alt+shift+e" style descriptors; mod is Ctrl or Cmd. */
      generate_shortcut: string;
      lock_shortcut: string;
      merge_visible_shortcut: string;
      cut_shortcut: string;
      duplicate_shortcut: string;
      deselect_shortcut: string;
      invert_selection_shortcut: string;
      zoom_in_shortcut: string;
      zoom_out_shortcut: string;
      zoom_actual_shortcut: string;
      zoom_selection_shortcut: string;
      layers_panel_shortcut: string;
      history_panel_shortcut: string;
      nudge_pixels: number;
      nudge_large_pixels: number;
      /** Canvas keys whose browser default is always cancelled while the pointer or focus is on the canvas. */
      reserved_shortcuts: string[];
    };
    relative_time: {
      milliseconds_per_minute: number;
      minutes_per_hour: number;
      hours_per_day: number;
      days_per_month: number;
      locale: string;
    };
    /**
     * Every stage-toolbar button in display order. The frontend binds a handler to each id and keeps no
     * second list of names, icons or hints. 3D processing steps are not listed: they come from the
     * processing capabilities (CapabilitySchema.icon / label_cn).
     */
    stage_actions: StageActionSchema[];
    /** Display-only configuration for processing navigation, independent of Offering execution/cost. */
    processing_action: StageActionPresentationSchema;
    /**
     * Every button of the stage's left rails in display order: the 2D canvas tool rail and the 3D view bar.
     * The frontend binds a handler to each id and keeps no second list of names, icons, hints or shortcuts.
     * Rail tools never call a model, so there is no cost field.
     */
    tools: ToolSchema[];
    /**
     * The right panel: its width, every tab it can show, and per kind of stage object which tabs it gets, in
     * what order, which opens first and which opens instead when the first has nothing to offer. The frontend
     * binds a body to each tab id and keeps no second list of tabs, names, icons, hints or shortcuts.
     */
    panel_tabs: PanelTabsSchema;
  };
  presentation: {
    messages: {
      forbidden: string;
      project_not_found: string;
      nodes_must_array: string;
      invalid_merge: string;
      dimension_limit: string;
      merge_failed: string;
      internal_error: string;
      revision_conflict: string;
      canvas_frame_invalid?: string;
    };
    text: Record<string, string>;
  };
}

export interface ChatProductSchema {
  api: {
    fields: {
      title: string;
      content: string;
      image_urls: string;
      aspect_ratio: string;
      pose_mode: string;
      ai_model: string;
      creative_domain: string;
      domain_models: string;
      size: string;
      image_size: string;
      parameters: string;
      skill: string;
      skill_id: string;
      skill_name: string;
      reference_image_indices: string;
      /** 对本条消息图片的结构化声明（截图来源等）与工作区当前选中对象。 */
      attachments: string;
      selection: string;
      mentions: string;
      input_source: string;
      from_id: string;
      turn_canceled: string;
      approval_mode: string;
      /** 会话设定摘要的请求成员。 */
      notes: string;
      /** 会话 Agent 范围的请求成员。 */
      agent_scope: string;
      /** #321: the answer to an in-chat choice card that travels with a turn. */
      choice_answer?: string;
      /** #322: the request member of the thinking level switch. */
      reasoning_effort?: string;
      /** #322: the request member of the chat model switch. */
      chat_model?: string;
    };
    response_fields: {
      id: string;
      title: string;
      created_at: string;
      updated_at: string;
      count: string;
      messages: string;
      role: string;
      content: string;
      image_urls: string;
      attachments: string;
      tool_calls: string;
      tool_name: string;
      tool_arguments: string;
      message_id: string;
      session_id: string;
      error: string;
      is_pending: string;
      pending_task_types: string;
      images: string;
      url: string;
      task_id: string;
      approval_mode: string;
      estimated_credits: string;
      approval_source: string;
      notes: string;
      /** 会话选用的候选图成员。 */
      pick: string;
      /** 会话 Agent 范围成员。 */
      agent_scope: string;
      /** 会话读取时各域最近一次发送所用模型的成员（#318）。 */
      generation_models?: string;
      /** #329 follow-up: per domain, the retired model the session last used when generation_models names its successor. */
      generation_models_retired?: string;
      /** #322: the thinking level the next turn sends, the levels the model offers and its default. */
      reasoning_effort?: string;
      reasoning_levels?: string;
      reasoning_default?: string;
      /** #322: the session's context usage as the upstream reported it, the model's window and the compaction records. */
      context_tokens?: string;
      context_window?: string;
      compactions?: string;
      /** #322: members of one compaction record. */
      compaction_trigger?: string;
      compacted_rounds?: string;
      through_message_id?: string;
      /**
       * #322: the session's chat model: the one the next turn uses, the session's own choice ('' when it
       * follows the default), the administrator's default, whether the choice went offline, and the offered
       * models (each with its id and upstream model name).
       */
      chat_model?: string;
      chat_model_chosen?: string;
      chat_model_default?: string;
      chat_model_fallback?: string;
      chat_models?: string;
      chat_model_name?: string;
    };
    /** 一个附件对象的成员名（消息响应与 attachments/selection 请求共用）。 */
    attachment: {
      handle: string;
      kind: string;
      role: string;
      asset_id: string;
      task_id: string;
      capture_id: string;
      image_slot: string;
      url: string;
      /** Request-only: the text of a mention. */
      token: string;
      /** Request-only: the visible layer count of the artboard snapshot claim (#272). */
      canvas_layers: string;
      /** Request-only, optional: the picture URLs of the layers the artboard snapshot flattens (#189). */
      canvas_sources?: string;
    };
    prompt_enhance: {
      prompt_field: string;
      capability_field: string;
      model_field: string;
      result_field: string;
      prompt_result_field: string;
      negative_prompt_result_field: string;
      error_field: string;
    };
  };
  defaults: {
    title: string;
  };
  roles: {
    system: string;
    user: string;
    assistant: string;
    tool: string;
  };
  state: {
    pending_content: string;
    active: string;
    done: string;
    error: string;
    turn_queued: string;
    turn_running: string;
    turn_done: string;
    turn_error: string;
    placeholder_stale_milliseconds: number;
    failed_prompt_preview_characters: number;
  };
  frontend: {
    connection_timeout_milliseconds: number;
    fallback_poll_milliseconds: number;
    reconnect_check_delay_milliseconds: number;
    option_storage_key: string;
    add_images_event_name: string;
    image_mime_prefix: string;
    image_file_accept: string;
    send_key: string;
    send_modifier: string;
    lightbox_close_key: string;
    lightbox_previous_key: string;
    lightbox_next_key: string;
    textarea_maximum_height_pixels: number;
    optimistic_message_id_prefix: string;
    optimistic_user_message_id: string;
    optimistic_assistant_message_id: string;
    filename_query_parameter: string;
    download_filename_max_characters: number;
    download_task_id_characters: number;
    download_extension: string;
    fallback_download_identifier: string;
    internal_api_path_prefix: string;
    legacy_hidden_content_prefix: string;
    legacy_hidden_content_suffix: string;
    progress_maximum: number;
  };
  context: { image_index_base: number; maximum_session_notes_characters: number; canvas_input_source: string; reference_filename: string };
  /** Paid-step approval modes the Schema offers, and which capabilities a person still decides in manual mode. */
  approval_policy: {
    default_mode: string;
    modes: string[];
    manual_approval_capabilities: string[];
  };
  /**
   * Agent 范围（2D 模式 / 3D 模式 / 自动模式）：有哪些模式，以及每个模式让哪些领域的
   * 模型选择器保持可用。标签是 `ui.studio.agent_scope.*` 文案；工具分类和 Agent 的提示
   * 文字只在服务端。
   */
  agent_scope: {
    default_mode: string;
    modes: Array<{ id: string; workspace_domains: string[] }>;
  };
  /**
   * #322: context compaction. `enabled: false` hides the compact action; the ratios color the context
   * meter (warning) and name the automatic compaction point; `keep_recent_rounds` stay verbatim.
   * Absent on older servers: the context meter is hidden.
   */
  context_compaction?: {
    enabled: boolean;
    auto_threshold_ratio: number;
    warning_ratio: number;
    keep_recent_rounds: number;
  };
  /** Public semantic operation catalog; no private tool wiring or prompts. */
  harness?: PublicHarnessSchema;
  /**
   * Whether the deployment's Agent can answer. `available: false` means it has no API key or
   * no model; the reason is `presentation.messages.agent_not_configured`, the missing setting
   * is only in the server log. Absent (older servers, raw catalog fixtures) means available.
   */
  agent_status?: { available: boolean };
  stream: { event_fields: ChatStreamEventFieldsSchema; event_types: ChatStreamEventTypesSchema };
  tools: Array<{
    name: string;
    capability: string;
    fallback_capability: string;
    pending_capability: string;
    workspace_domain?: string;
    require_unique_offering?: boolean;
  }>;
  skills: {
    fields: ChatSkillCatalogFieldsSchema;
    categories: Array<{ id: string; label: string; hidden_capabilities: string[] }>;
    creation: { route: string; item_route?: string; max_name_characters: number; recommended_size: string };
  };
  presentation: {
    messages: {
      session_not_found: string;
      message_not_found: string;
      empty_title: string;
      empty_message: string;
      invalid_parameters: string;
      interrupted: string;
      interrupted_prompt: string;
      generation_failed: string;
      generation_error: string;
      summary_failed: string;
      no_result: string;
      unknown_error: string;
      agent_not_configured?: string;
      error_reference?: string;
      /** #322: the chat model switch was refused (no per-session choice, or the model is not offered). */
      chat_model_unavailable?: string;
      chat_model_invalid?: string;
      sse_interrupted: string;
      internal_error: string;
      unauthenticated: string;
      agent_busy: string;
      turn_active: string;
    };
    status: {
      generating: string;
      /** Shown while a model round has no visible output yet (#285). */
      thinking?: string;
    };
  };
}

export interface ChatStreamEventFieldsSchema {
  type: string;
  content: string;
  percent: string;
  task_id: string;
  urls: string;
  message_id: string;
  capability: string;
  prompt: string;
  ai_model: string;
  session_id: string;
  final_task_id: string;
  estimated_credits: string;
  approval_source: string;
  status: string;
  progress: string;
  result: string;
  image_urls: string;
  error: string;
}

export interface ChatStreamEventTypesSchema {
  text: string;
  status: string;
  progress: string;
  images: string;
  title: string;
  message_created: string;
  task_registered: string;
  task_update: string;
  error: string;
  done: string;
  /** Content-free "the model is thinking" status event (#285); optional. */
  thinking?: string;
}

export interface ChatSkillCatalogFieldsSchema {
  revision: string;
  skills: string;
  id: string;
  name: string;
  name_zh: string;
  category: string;
  recommended_size: string;
  thumbnail_path: string;
  raw_image_url: string;
  prompt: string;
  image_to_image_template: string;
  tags: string;
  tool: string;
  requires_image: string;
  default_message: string;
  one_shot: string;
  error: string;
}

export interface NavigationItemSchema {
  key: string;
  label: string;
  href: string;
  icon: string;
  order: number;
  placement: 'main' | 'bottom';
  admin_only?: boolean;
}

export interface ValueLabelSchema {
  value: string;
  label: string;
}

export interface AssetKindPatternSchema {
  prefix: string;
  message_key: string;
  uppercase_suffix?: boolean;
}

export interface ParameterConditionSchema {
  parameter: string;
  operator:
    | 'equals'
    | 'not-equals'
    | 'truthy'
    | 'falsy'
    | 'present'
    | 'absent';
  value?: unknown;
}

export interface ConditionalParameterSchema {
  disabled_when?: ParameterConditionSchema[];
  visible_when?: ParameterConditionSchema[];
  forced_when?: ForcedValueSchema[];
  notices?: ParameterNoticeSchema[];
}

export interface ParameterNoticeSchema {
  when: ParameterConditionSchema;
  message_key: string;
  tone: 'info' | 'warning' | 'error';
}

export interface ForcedValueSchema {
  when: ParameterConditionSchema;
  value?: unknown;
}

/** On which stage object a toolbar button appears. */
export type StageActionKind = 'image' | 'model' | 'board';
/** Where it sits in the toolbar after the identity group. */
export type StageActionGroup = 'produce' | 'output' | 'view';
/** Which cost sentence its hover hint carries (messages `ui.studio.hint.cost.<cost_hint>`). */
export type StageActionCostHint = 'approval_card' | 'process_panel' | 'free';

export interface StageActionPresentationSchema {
  group: StageActionGroup;
  /** Legacy display flag: draws the ✦ mark; does not classify or authorize actual execution.
   * approval_card requires true, free requires false; process_panel may be unmarked navigation. */
  calls_model: boolean;
  cost_hint: StageActionCostHint;
}

export interface StageActionSchema extends StageActionPresentationSchema {
  id: string;
  kinds: StageActionKind[];
  /** lucide kebab-case name, resolved by the frontend icon map. */
  icon: string;
  label_key: string;
  hint_key: string;
  target_key: string;
  /** Name of a `canvas.frontend.keyboard` setting whose key is printed beside the name. */
  shortcut?: string;
}

/** Which rail draws a tool: the 2D board's tool rail or the 3D viewer's view bar. */
export type ToolRailName = 'canvas' | 'viewer';
/** Groups buttons inside a rail (a thin line separates sections). canvas: select / draw / snap / history; viewer: views. */
export type ToolSectionName = 'select' | 'draw' | 'snap' | 'history' | 'views';
export type ToolModifier = 'mod' | 'shift' | 'alt';

export interface ToolSchema {
  id: string;
  rail: ToolRailName;
  section: ToolSectionName;
  /** lucide kebab-case name, resolved by the frontend icon map. */
  icon: string;
  label_key: string;
  hint_key: string;
  /** Name of a `canvas.frontend.keyboard` setting that holds a single key; printed beside the name. */
  shortcut?: string;
  /** Printed before the key: undo_key "z" with ['mod'] reads Ctrl+Z. */
  modifiers?: ToolModifier[];
}

/** The kind of stage object a set of right-panel tabs belongs to: a picture, the board with no version yet, or a 3D model. */
export type PanelKind = 'image' | 'board' | 'model';

export interface PanelTabSchema {
  /** The body to draw: layers, history, details, process, formats or parts. An id the code has no body for is not drawn. */
  id: string;
  /** lucide kebab-case name, resolved by the frontend icon map. */
  icon: string;
  label_key: string;
  hint_key: string;
  /** Name of a `canvas.frontend.keyboard` setting; pressing it switches to this tab, and the hover hint prints the key. */
  shortcut?: string;
}

export interface PanelKindSchema {
  kind: PanelKind;
  /** Tab ids left to right. */
  tabs: string[];
  /** The tab that opens when nothing was chosen for this kind in this visit. */
  default: string;
  /** Opens instead of `default` while the default tab has nothing to offer (today: no processing step is available). */
  default_when_unavailable?: string;
}

export interface PanelTabsSchema {
  width_pixels: number;
  tabs: PanelTabSchema[];
  kinds: PanelKindSchema[];
}

export interface CapabilitySchema {
  id: string;
  domain: 'image' | 'model-3d' | 'processing';
  label: string;
  label_cn: string;
  /** lucide kebab-case name of its toolbar button; set for every capability in runtime.frontend.model_processing_capabilities. */
  icon?: string;
  order: number;
  /** Whether the capability participates in the generic workspace model chooser; missing preserves legacy behavior. */
  workspace_model_selector?: boolean;
  workflow?: {
    initial_input_mode: string;
    initial_phase?: string;
    default_auto_remesh?: boolean;
  };
  legacy_api?: LegacyTaskSchema;
  prompt_composition?: {
    minimum_references: number;
    template_message_key: string;
  };
  /** Ordered image result slots for one public Task; separate from delivery groups. */
  image_result_group?: { slots: Array<{ variant: string; label: string }>; source_selectable?: boolean; partial_message?: string };
}

export type LegacyRecordSemantic =
  | 'task-id'
  | 'status'
  | 'downloadable'
  | 'download-path'
  | 'source-name'
  | 'error-message'
  | 'invalid-reason';

export interface LegacyTaskSchema {
  path: string;
  model_parameter: string;
  output_kind: 'image' | 'model';
  output_projections: Array<{
    name: string;
    role: string;
    mode: 'array' | 'map' | 'scalar' | 'array-map' | 'records';
    record_source?: 'assets' | 'provider-data';
    default_key?: string;
    record_fields?: Array<{
      name: string;
      semantic: LegacyRecordSemantic;
      source?: string;
      literal?: unknown;
    }>;
  }>;
  fields?: Array<{
    name: string;
    target_parameter?: string;
    required?: boolean;
    restore?: boolean;
    forwarding: 'always' | 'if-supported';
  }>;
  asset_aliases?: Array<{
    name: string;
    semantic: string;
    cardinality: 'one' | 'many';
    restore?: boolean;
    restore_minimum_items?: number;
  }>;
  task_aliases?: Array<{
    name: string;
    target_parameter: string;
    restore?: boolean;
  }>;
  multipart?: {
    file_field: string;
    params_field: string;
    target_parameter: string;
    cardinality: 'one' | 'many';
    missing_file_message_key: string;
    invalid_count_message_key?: string;
    invalid_extension_message_key: string;
    invalid_params_message_key: string;
  };
  asset_download?: {
    path: string;
    token_parameter: string;
    filename_template: string;
    media_type: string;
    cache_subdirectory: string;
    cache_filename_template: string;
    cache_header?: string;
    cache_value?: string;
  };
  result_field?: string;
  result_assets?: Array<{
    name: string;
    role: string;
    variant?: string;
    media_types?: string[];
  }>;
  result_collections?: Array<{
    name: string;
    role: string;
    variant_prefix: string;
  }>;
}

export interface TaskStateSchema {
  internal_state: string;
  public_state: string;
  label: string;
  color: string;
  terminal: boolean;
  outcome: 'pending' | 'running' | 'success' | 'failure' | 'canceled';
  progress: {
    mode: 'hidden' | 'reported' | 'fixed' | 'retry-countdown';
    fixed_value?: number;
    show_error?: boolean;
    error_color?: string;
    retry_badge_color?: string;
    track_color?: string;
    countdown_color?: string;
  };
}

export function taskStatePresentation(
  schema: ProductSchema,
  status: string,
): TaskStateSchema {
  const exact = schema.task_states.find(
    (state) => state.internal_state === status,
  );
  if (exact) return exact;
  const projected = schema.task_states.find(
    (state) => state.public_state === status,
  );
  if (projected) return projected;
  const fallback = schema.task_states.find(
    (state) =>
      state.internal_state ===
      schema.runtime.frontend.task_progress.fallback_state,
  );
  if (fallback) return { ...fallback, label: status };
  throw new Error('Product Schema task progress fallback is missing');
}

export interface AuthenticationMessagesSchema {
  unauthenticated: string;
  forbidden: string;
  user_deactivated: string;
  missing_credentials: string;
  invalid_credentials: string;
  login_failed: string;
  network_error: string;
  internal_error: string;
  mock_disabled: string;
  mock_user_missing: string;
  mock_user_invalid: string;
}

export interface AuthenticationAPISchema {
  id_field: string;
  username_field: string;
  password_field: string;
  name_field: string;
  email_field: string;
  error_field: string;
  user_field: string;
  is_admin_field: string;
}

export interface LoginSchema {
  redirect_query: string;
  title: string;
  description: string;
  username_label: string;
  username_placeholder: string;
  password_label: string;
  password_placeholder: string;
  submit_label: string;
  submitting_label: string;
  show_password: string;
  hide_password: string;
  loading_label: string;
  default_redirect: string;
}

export interface TaskTabSchema {
  value: string;
  label: string;
}

export function schemaText(schema: ProductSchema, key: string): string {
  return schema.content.messages[key] ?? '';
}

export function schemaMessage(
  schema: ProductSchema,
  key: string,
  values: Record<string, string | number>,
): string {
  return formatSchemaText(schemaText(schema, key), values);
}

/**
 * Whether this locale's plural rule calls `count` "one" (#363). Shared by
 * message counts and Canvas relative time, whose locale comes from its own
 * `frontend.relative_time.locale` instead of a ProductSchema localization block.
 * An unknown tag falls back to `count === 1`.
 */
export function isSingularCount(locale: string, count: number): boolean {
  try {
    return new Intl.PluralRules(locale).select(count) === 'one';
  } catch {
    return count === 1;
  }
}

/**
 * A message that names a count (#330 C): when the active locale's plural rule
 * calls `count` "one" and the Schema declares `<key>_one`, that singular text is
 * used ("About 1 credit"); otherwise the general text. Locales without a
 * singular form (Chinese) simply declare no `_one` text or the same one.
 */
export function schemaCountMessage(
  schema: ProductSchema,
  key: string,
  count: number,
  values: Record<string, string | number>,
): string {
  const singular = isSingularCount(activeLocale(schema), count)
    ? schemaText(schema, `${key}_one`)
    : '';
  return formatSchemaText(singular || schemaText(schema, key), values);
}

/** The Schema's reason the Agent cannot answer (no API key or no model), or null while it can. */
export function agentUnavailableReason(schema: ProductSchema): string | null {
  if (schema.chat.agent_status?.available !== false) return null;
  return schema.chat.presentation.messages.agent_not_configured || schema.chat.presentation.messages.unknown_error;
}

export function formatSchemaText(
  template: string,
  values: Record<string, string | number>,
): string {
  return template.replace(
    /\{([A-Za-z0-9_.-]+)\}/g,
    (placeholder, name: string) =>
      Object.hasOwn(values, name) ? String(values[name]) : placeholder,
  );
}

export function valueLabel(
  values: ValueLabelSchema[],
  value: string,
  fallback: string,
): string {
  return values.find((item) => item.value === value)?.label ?? fallback;
}

export function parameterDisabled(
  parameter: ConditionalParameterSchema,
  values: Record<string, unknown>,
): boolean {
  return (parameter.disabled_when ?? []).some((condition) =>
    parameterConditionMatches(condition, values),
  );
}

/**
 * 声明式可见条件（AND 语义）：visible_when 全部满足时控件才渲染；
 * 未声明或声明为空时恒可见。隐藏参数不进入提交值。
 */
export function parameterVisible(
  parameter: ConditionalParameterSchema,
  values: Record<string, unknown>,
): boolean {
  return (parameter.visible_when ?? []).every((condition) =>
    parameterConditionMatches(condition, values),
  );
}

/**
 * 解析参数在当前值下的强制值：返回第一条命中的 forced_when 值。
 * 服务端 normalize 拥有最终裁决；前端仅用于展示锁定与提交对齐。
 */
export function parameterForcedValue(
  parameter: ConditionalParameterSchema,
  values: Record<string, unknown>,
): { found: boolean; value?: unknown } {
  for (const forced of parameter.forced_when ?? []) {
    if (parameterConditionMatches(forced.when, values)) {
      return { found: true, value: forced.value };
    }
  }
  return { found: false };
}

export function parameterNotices(
  parameter: ConditionalParameterSchema,
  values: Record<string, unknown>,
): ParameterNoticeSchema[] {
  return (parameter.notices ?? []).filter((notice) =>
    parameterConditionMatches(notice.when, values),
  );
}

export function parameterConditionMatches(
  condition: ParameterConditionSchema,
  values: Record<string, unknown>,
): boolean {
  const current = values[condition.parameter];
  const present = current !== undefined && current !== null && current !== '';
  switch (condition.operator) {
    case 'equals':
      return current === condition.value;
    case 'not-equals':
      return current !== condition.value;
    case 'truthy':
      return Boolean(current);
    case 'falsy':
      return !current;
    case 'present':
      return present;
    case 'absent':
      return !present;
  }
}

export function withRuntimeVersion(
  schema: ProductSchema,
  version: string,
): ProductSchema {
  const normalized = version.trim();
  if (!normalized || normalized === schema.content.service.version) {
    return schema;
  }
  return {
    ...schema,
    content: {
      ...schema.content,
      service: {
        ...schema.content.service,
        version: normalized,
      },
    },
  };
}

export function projectRuntimeProductSchema(
  baseSchema: ProductSchema,
  runtimeSchema: ProductSchema,
  refreshedVersion?: string,
): ProductSchema {
  const projected = {
    ...baseSchema,
    runtime: {
      ...baseSchema.runtime,
      frontend: {
        ...baseSchema.runtime.frontend,
        task_api_prefix:
          runtimeSchema.runtime.frontend.task_api_prefix,
      },
    },
  };
  return withRuntimeVersion(
    projected,
    refreshedVersion ?? runtimeSchema.content.service.version,
  );
}

export function selectNavigation(
  schema: ProductSchema,
  placement: NavigationItemSchema['placement'],
  isAdmin: boolean,
): NavigationItemSchema[] {
  return schema.content.navigation
    .filter((item) => item.placement === placement)
    .filter((item) => !item.admin_only || isAdmin)
    .toSorted((left, right) => left.order - right.order);
}

export function capabilityWorkflow(
  schema: ProductSchema,
  capabilityID: string,
): NonNullable<CapabilitySchema['workflow']> {
  const workflow = schema.capabilities.find(
    (capability) => capability.id === capabilityID,
  )?.workflow;
  if (!workflow) {
    throw new Error(
      `Product Schema capability workflow missing: ${capabilityID}`,
    );
  }
  return workflow;
}

export function buildTaskTabs(schema: ProductSchema): TaskTabSchema[] {
  return [
    {
      value: schema.task_history.values.all_capability,
      label: schemaText(schema, 'ui.tasks.all'),
    },
    ...schema.capabilities
      .toSorted((left, right) => left.order - right.order)
      .map((capability) => ({
        value: capability.id,
        label: capability.label,
      })),
  ];
}
