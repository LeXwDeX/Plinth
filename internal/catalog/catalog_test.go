package catalog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const minimalApplication = `"application":{
	"revision":"test.1",
	"runtime":{
		"configuration":{"settings":{
			"application_environment":{"type":"string","environment":[{"name":"APP_ENV"}],"default":"development","allowed_values":["development","production","test"]},
			"address":{"type":"string","environment":[{"name":"APP_ADDR"}],"default":"127.0.0.1:13333","required":true},
			"database_path":{"type":"string","environment":[{"name":"DATABASE_PATH"}],"default":"./test.db","required":true},
			"storage_root":{"type":"string","environment":[{"name":"STORAGE_ROOT"}],"default":"./test-storage","required":true},
			"web_assets_root":{"type":"string","environment":[{"name":"WEB_ASSETS_ROOT"}],"default":"./web","required":true},
			"legacy_generated_images_root":{"type":"string","environment":[{"name":"LEGACY_GENERATED_IMAGES_ROOT"}],"default":"./test-storage/generated","required":true},
			"legacy_asset_cache_root":{"type":"string","environment":[{"name":"ASSET_CACHE_DIR"}],"default":"./test-storage/cache","required":true},
			"legacy_thumbnail_cache_root":{"type":"string","environment":[{"name":"THUMBNAIL_CACHE_DIR"}],"default":"./test-storage/thumbnails","required":true},
			"legacy_input_assets_root":{"type":"string","environment":[{"name":"LEGACY_INPUT_ASSETS_ROOT"},{"name":"ART_ASSET_ROOT_DIR"}],"default":"./storage/assets","required":true},
			"catalog_file":{"type":"string","environment":[{"name":"CATALOG_FILE"}],"default":""},
			"skill_catalog_file":{"type":"string","environment":[{"name":"SKILL_CATALOG_FILE"}],"default":""},
			"legacy_backend_startup_enabled":{"type":"string","environment":[{"name":"LEGACY_BACKEND_STARTUP_ENABLED"}],"default":"true","allowed_values":["true","false"]},
			"public_base_url":{"type":"string","environment":[{"name":"PUBLIC_BASE_URL"}],"default":""},
			"asset_signing_key":{"type":"string","environment":[{"name":"ASSET_SIGNING_KEY"}],"default":"","secret":true},
			"provider_mode":{"type":"string","environment":[{"name":"PROVIDER_MODE"}],"default":"mock","allowed_values":["mock","live"]},
			"agent_mode":{"type":"string","environment":[{"name":"AGENT_MODE"}],"default":"mock","allowed_values":["mock","live"]},
			"openai_base_url":{"type":"string","environment":[{"name":"OPENAI_BASE_URL"},{"name":"LLM_BASE_URL"}],"default":"https://api.openai.com/v1"},
			"openai_api_key":{"type":"string","environment":[{"name":"OPENAI_API_KEY"},{"name":"LLM_API_KEY"}],"default":"","secret":true},
			"llm_base_url":{"type":"string","environment":[{"name":"LLM_BASE_URL"}],"default":"http://127.0.0.1:8000/v1","required":true},
			"llm_api_key":{"type":"string","environment":[{"name":"LLM_API_KEY"}],"default":"","secret":true},
			"llm_model":{"type":"string","environment":[{"name":"LLM_MODEL"}],"default":"test-model","required":true},
			"agent_max_tool_calls_per_reply":{"type":"string","environment":[{"name":"AGENT_MAX_TOOL_CALLS_PER_REPLY"}],"default":""},
			"gemini_base_url":{"type":"string","environment":[{"name":"GEMINI_BASE_URL"},{"name":"LLM_BASE_URL"}],"default":"http://127.0.0.1:8000/v1"},
			"gemini_api_key":{"type":"string","environment":[{"name":"GEMINI_API_KEY"},{"name":"LLM_API_KEY"}],"default":"","secret":true},
			"meshy_base_url":{"type":"string","environment":[{"name":"MESHY_BASE_URL"},{"name":"MESHY_API_BASE_URL"}],"default":"https://api.meshy.ai"},
			"meshy_api_key":{"type":"string","environment":[{"name":"MESHY_API_KEY"}],"default":"","secret":true},
			"meshy_webhook_path_secret":{"type":"string","environment":[{"name":"MESHY_WEBHOOK_PATH_SECRET"}],"default":"","secret":true},
			"meshy_webhook_key":{"type":"string","environment":[{"name":"MESHY_WEBHOOK_KEY"}],"default":"","secret":true},
			"tripo_base_url":{"type":"string","environment":[{"name":"TRIPO_BASE_URL"},{"name":"TRIPO_API_BASE_URL"}],"default":"https://api.tripo3d.ai/v2/openapi"},
			"tripo_api_key":{"type":"string","environment":[{"name":"TRIPO_API_KEY"}],"default":"","secret":true},
			"matting_base_url":{"type":"string","environment":[{"name":"MATTING_BASE_URL"},{"name":"MATTING_API_URL"}],"default":"http://127.0.0.1:8001"},
			"matting_api_key":{"type":"string","environment":[{"name":"MATTING_API_KEY"},{"name":"MATTING_API_TOKEN"}],"default":"","secret":true},
			"auth_mode":{"type":"string","environment":[{"name":"AUTH_MODE"}],"default":"development","default_by_environment":{"production":"session"},"allowed_values":["development","session","bearer"]},
			"development_user_id":{"type":"string","environment":[{"name":"DEV_USER_ID"}],"default":"local-user"},
			"development_user_name":{"type":"string","environment":[{"name":"DEV_USER_NAME"}],"default":"本地用户"},
			"development_user_email":{"type":"string","environment":[{"name":"DEV_USER_EMAIL"}],"default":""},
			"development_user_roles":{"type":"string-list","environment":[{"name":"DEV_USER_ROLES"}],"default":"","separator":","},
			"auth_tokens_json":{"type":"json","environment":[{"name":"AUTH_TOKENS_JSON"}],"default":"","secret":true},
			"session_secret":{"type":"string","environment":[{"name":"SESSION_SECRET"}],"default":"","secret":true},
			"session_cookie_name":{"type":"string","environment":[{"name":"SESSION_COOKIE_NAME"}],"default":""},
			"frontend_public_app_url":{"type":"string","environment":[{"name":"NEXT_PUBLIC_APP_URL"}],"default":""},
			"dev_mock_auth":{"type":"string","environment":[{"name":"DEV_MOCK_AUTH"}],"default":"false","allowed_values":["true","false"]},
			"dev_mock_user_json":{"type":"json","environment":[{"name":"DEV_MOCK_USER"}],"default":""},
			"admin_allowlist_emails":{"type":"string-list","environment":[{"name":"ADMIN_ALLOWLIST_EMAILS"}],"default":"","separator":","},
			"admin_username":{"type":"string","environment":[{"name":"ADMIN_USERNAME"}],"default":"admin"},
			"admin_password":{"type":"string","environment":[{"name":"ADMIN_PASSWORD"}],"default":"","secret":true},
			"admin_display_name":{"type":"string","environment":[{"name":"ADMIN_DISPLAY_NAME"}],"default":"管理员"},
			"admin_email":{"type":"string","environment":[{"name":"ADMIN_EMAIL"}],"default":"admin@example.invalid"}
		}},
		"legacy_migration":{
			"audit_asset_recovery":{
				"root_subdirectory":"audit",
				"filename_template":"{task_id}_{source_stem}{source_extension}"
			},
			"thumbnail_backfill":{
				"source_cache_subdirectory":"legacy-thumbnail-backfill",
				"batch_size":50,
				"maximum_asset_bytes":52428800
			},
			"task_output_excluded_roles":["*"],
			"cache_only_task_exclusions":[]
		},
		"storage_maintenance":{"minimum_age_seconds":86400},
      "generation_messages":{"inline_store_failed":"inline","stored_asset_invalid":"stored","submission_unknown":"unknown"},
		"task_cancellation":{"timeout_seconds":15,"local_only":"local","requesting":"requesting","confirmed":"confirmed","failed":"failed","unsupported":"unsupported","unknown":"unknown"},
		"coordinator":{"poll_interval_milliseconds":250,"lease_seconds":120,"batch_size":20,"concurrency":8,"lease_renew_milliseconds":30000,"max_backoff_milliseconds":30000},
		"provider_accounts":{
			"meshy":{
				"provider_id":"meshy",
				"base_url_setting":"meshy_base_url",
				"api_key_setting":"meshy_api_key",
				"balance":{
					"adapter_key":"http-json",
					"endpoint_path":"/openapi/v1/balance",
					"authorization_header":"Authorization",
					"authorization_type":"Bearer",
					"upstream_balance_field":"balance",
					"request_timeout_seconds":30,
					"response_max_bytes":65536,
					"error_body_max_bytes":65536,
					"mock_value":1000
				}
			}
		},
		"input_assets":{
			"upload_field_name":"file","upload_max_bytes":10485760,
			"default_media_type":"application/octet-stream",
			"default_asset_type":"image","stored_content_policy":"preserve-source",
			"json_format_parameter":"format","json_format_value":"json",
			"binary_cache_control":"private, no-store",
			"legacy_storage_root_setting":"legacy_input_assets_root",
			"allowed_upload_media_types":["image/png"],
			"inline_media_types":["image/png"],
			"api":{"fields":{
				"id":"id","asset_id":"asset_id","asset_type":"asset_type",
				"stored_sha256":"stored_sha256",
				"original_filename":"original_filename",
				"media_type":"mime_type","file_size":"file_size",
				"data_uri":"data_uri","created_at":"created_at"
			}},
			"audit":{
				"upload_action":"access_asset",
				"access_action":"access_asset",
				"resource_type":"asset",
				"detail_fields":{
					"filename":"filename","media_type":"mimeType",
					"file_size":"fileSize"
				}
			},
			"type_rules":[
				{"asset_type":"image","media_types":["image/png"]},
				{"asset_type":"model","extensions":[".glb"]}
			]
		},
		"task_deletion":{
			"local_record_policy":"tombstone",
			"upstream_policy":"when-supported",
			"upstream_failure_policy":"abort",
			"generated_asset_policy":"retain",
			"audit_event_type":"task.deleted"
		},
		"task_events":{
			"public_projection":[
				{"type":"task.submitted","fields":["offering_id","capability"]},
				{"type":"attempt.failed","fields":["class","retryable","ambiguous","detail"]}
			]
		},
		"task_audit":{
			"create_action":"access_asset","delete_action":"access_asset",
			"resource_type":"task",
			"detail_fields":{
				"capability":"capability","offering_id":"offeringId",
				"replayed":"replayed"
			}
		},
		"cache":{
			"milliseconds_per_second":1000,
			"bytes_per_megabyte":1048576,
			"size_decimal_places":1,
			"fields":{
				"active_downloads":"activeDownloads",
				"active_items":"activeItems",
				"filename":"filename","elapsed":"elapsed",
				"task_id":"taskId","task_type":"taskType",
				"provider_id":"providerId","asset_kind":"assetKind",
				"total_cached_files":"totalCachedFiles",
				"total_cache_size_mb":"totalCacheSizeMB"
			}
		},
		"asset_delivery":{
			"task_asset_audit":{
				"download_action":"access_asset","resource_type":"asset",
				"detail_fields":{"task_id":"taskId","asset_id":"assetId"}
			},
			"asset_preview":{
				"query":"size","request_value":"preview","cache_subdirectory":"asset-previews",
				"maximum_dimension":512,"maximum_input_bytes":67108864,"maximum_pixels":33554432,"decode_concurrency":2,
				"source_media_types":["image/png","image/jpeg"],"output_media_type":"image/png"
			},
			"legacy_generated_image":{
				"filename_separator":"_","ordinal_base":0,
				"asset_roles":["image"],
				"extension_media_types":{"png":"image/png"},
				"default_media_type":"image/png",
				"cache_control":"private, no-store",
				"not_found_message_key":"error.legacy_generated_image_not_found",
				"audit_action":"access_asset",
				"audit_resource_type":"gpt-image"
			},
			"proxy":{
				"api":{
					"query":{"url":"url","filename":"filename","task_id":"taskId","size":"size"},
					"fields":{"error":"error"},
					"statuses":{"success":200,"bad_request":400,"forbidden":403,"not_found":404,"bad_gateway":502}
				},
				"security":{"host_source":"offering_asset_hosts","allowed_schemes":["http","https"]},
				"cache":{
					"root_setting":"legacy_asset_cache_root","shard_characters":2,
					"metadata_suffix":".meta.json",
					"metadata_fields":{"url":"url","content_type":"contentType","size":"size","cached_at":"cachedAt"},
					"control":"private, no-store","cors_origin":"",
					"status_header":"X-Cache","hit_value":"HIT","miss_value":"MISS",
					"refreshed_value":"REFRESHED"
				},
				"http":{"request_timeout_milliseconds":300000,"response_max_bytes":1073741824,"maximum_redirects":5},
				"thumbnail":{
					"root_setting":"legacy_thumbnail_cache_root",
					"request_value":"thumb","maximum_dimension":256,"maximum_input_bytes":33554432,"maximum_pixels":16777216,"decode_concurrency":2,
					"output_media_type":"image/png","filename_suffix":"-thumb.png",
					"header":"X-Thumbnail","header_value":"thumb-256"
				},
				"metadata":{
					"supported_media_types":["image/jpeg","image/png","image/webp","image/tiff"],
					"copyright_template":"Generated with Plinth, {year}",
					"artist":"Plinth","software":"Plinth"
				},
				"audit":{
					"action":"access_asset","resource_type":"asset",
					"resource_id_maximum_characters":200,
					"detail_fields":{"filename":"filename","size":"size"}
				},
				"messages":{"missing_url":"missing","invalid_url":"invalid","forbidden_host":"forbidden","fetch_failed":"failed"},
				"media_type_extensions":{
					"image/png":".png",
					"application/octet-stream":".bin"
				},
				"default_media_type":"application/octet-stream"
			}
		},
		"http":{
			"read_header_timeout_seconds":10,"read_timeout_seconds":30,
			"write_timeout_seconds":1800,"idle_timeout_seconds":120,
			"shutdown_timeout_seconds":10,"json_body_max_bytes":105906176,
			"security_headers":{"X-Content-Type-Options":"nosniff"},
			"task_admission":{"per_owner_max_inflight_tasks":8},
			"multipart_body_max_bytes":105906176,"default_page_size":32,
			"maximum_page_size":100,"maximum_recent_tasks":50,"maximum_lineage_tasks":200,"lineage_page_size":50,
			"private_cache_control":"private, no-store",
			"immutable_asset_cache_control":"private, no-store",
			"schema_cache_control":"public, max-age=60",
			"routes":{
				"home":"/{$}","liveness":"/healthz","readiness":"/readyz",
				"product_schema":"/api/v1/schema",
				"chat_skills":"/api/v1/chat/skills",
				"provider_asset":"/api/v1/provider-assets/{token}",
				"offerings":"/api/v1/offerings","assets":"/api/v1/assets",
				"asset_item":"/api/v1/assets/{assetID}",
				"tasks":"/api/v1/tasks","task_item":"/api/v1/tasks/{taskID}",
				"task_cancel":"/api/v1/tasks/{taskID}/cancel",
				"task_events":"/api/v1/tasks/{taskID}/events",
				"task_asset":"/api/v1/tasks/{taskID}/assets/{assetID}"
			},
			"legacy_routes":{
				"health":"/api/health","providers":"/api/providers",
				"tasks":"/api/tasks","version":"/api/version",
				"asset_upload":"/api/assets/upload",
				"asset_item":"/api/assets/{assetID}",
				"prompt_enhance":"/api/prompt-enhance",
				"auth_login":"/api/auth/login",
				"auth_mock":"/api/auth/mock",
				"auth_session":"/api/auth/session",
				"cache_status":"/api/cache-status",
				"generated_image":"/api/gpt-images/{filename}",
				"chat_sessions":"/api/chat/sessions",
				"chat_session":"/api/chat/sessions/{sessionID}",
				"chat_messages":"/api/chat/sessions/{sessionID}/messages",
				"chat_message":"/api/chat/sessions/{sessionID}/messages/{messageID}",
				"chat_stream":"/api/chat/sessions/{sessionID}/stream",
				"chat_turn_cancel":"/api/chat/sessions/{sessionID}/turn/cancel",
				"provider_balance":"/api/meshy/balance",
				"canvas_projects":"/api/canvas/projects",
				"canvas_project":"/api/canvas/projects/{projectID}",
				"canvas_nodes":"/api/canvas/projects/{projectID}/nodes",
				"canvas_merge":"/api/canvas/merge-images",
			"task_stream":"/api/meshy/stream/{type}/{taskID}",
			"admin_users":"/api/admin/users",
			"admin_user":"/api/admin/users/{userID}",
			"admin_audit_logs":"/api/admin/audit-logs",
			"admin_generations":"/api/admin/generations",
			"admin_generation":"/api/admin/generations/{taskID}",
			"admin_generation_download":"/api/admin/generations/{taskID}/download",
			"admin_usage":"/api/admin/usage",
			"admin_balance_summary":"/api/admin/balance-summary",
			"admin_user_assets":"/api/admin/user-assets/{userID}",
			"admin_backfill_status":"/api/admin/backfill-status",
			"meshy_proxy_asset":"/api/meshy/proxy-asset"
			},
			"legacy_provider_balance":{"accounts_field":"accounts","provider_field":"provider","balance_field":"balance","error_field":"error","accounts":[{"account_id":"meshy"}]},
			"candidate_proxy":{
				"task_prefix":"/api/go","core_prefix":"/api/v1",
				"legacy_route_keys":[
					"health","providers","tasks","version","asset_upload",
					"asset_item","auth_login","auth_mock","auth_session",
					"cache_status","generated_image","admin_usage",
					"admin_balance_summary"
				]
			}
		},
		"frontend":{
			"image_upload":{"maximum_pixels":16777216,"conversions":[]},
			"web_asset_prefix":"/assets/",
			"favicon_path":"/favicon.ico",
			"client_routes":{"home":"/","login":"/login"},
			"provider_metadata_cache_ttl_milliseconds":300000,
			"task_history_refresh_milliseconds":30000,
			"task_history_deduping_milliseconds":3000,
			"task_history_revalidate_on_focus":false,
			"task_history_keep_previous_data":true,
			"balance_refresh_milliseconds":60000,
			"cache_status_refresh_milliseconds":5000,
			"default_prompt_max_length":10000,
			"default_reference_image_min":1,
			"default_reference_image_max":5,
			"product_version_refresh_milliseconds":300000,
			"task_submission_cooldown_seconds":3,
			"task_queue_maximum":10,
			"batch_download_interval_milliseconds":300,
			"task_identifier_preview_characters":12,
			"countdown_tick_milliseconds":1000,
			"task_polling_interval_milliseconds":3000,
			"session_menu_page_size":15,
			"session_menu_search_debounce_milliseconds":250,
			"session_title_refresh_attempts":6,
			"session_title_refresh_interval_milliseconds":2000,
			"chat_message_page_size":50,
			"display_locale":"zh-CN",
			"task_api_prefix":"",
			"spinner_frame_milliseconds":80,
			"percentage_multiplier":100,
			"percentage_suffix":"%",
			"advanced_model_form_feature":"advanced-model-form",
			"advanced_model_default_mode":"regular",
			"task_progress":{
				"fallback_state":"RUNNING",
				"initial_state":"QUEUED",
				"not_found_state":"FAILED",
				"milliseconds_per_second":1000,
				"seconds_per_minute":60,
				"minutes_per_hour":60,
				"hour_decimal_places":1
			},
			"toaster":{
				"position":"top-right",
				"rich_colors":true
			}
		}
	},
	"content":{
		"brand":{
			"title":"Plinth","metadata_title":"Plinth 测试",
			"metadata_description":"测试平台","version_prefix":"v"
		},
		"service":{"id":"art-ai-go","name":"Plinth Platform","version":"test"},
		"home":{
			"title":"测试运行时","description":"测试说明",
			"links":[{"href":"/healthz","label":"运行状态","method":"GET"}]
		},
		"navigation":[{
			"key":"home","label":"首页","href":"/","icon":"home",
			"order":0,"placement":"main"
		}],
		"asset_kinds":[{"value":"image","label":"生成图片"}],
		"asset_kind_patterns":[
			{"prefix":"model","message_key":"ui.asset.model","uppercase_suffix":true},
			{"prefix":"texture","message_key":"ui.asset.texture"}
		],
		"messages":{
		"error.internal":"服务器内部错误",
		"ui.studio.hint.cost.approval_card":"会调用模型，积分和批准在对话里",
		"ui.studio.hint.cost.process_panel":"会调用模型，确认后才花积分",
		"ui.studio.hint.cost.free":"免费，不调用模型",
		"ui.studio.stage.action.inpaint":"局部重绘",
		"ui.studio.stage.action.inpaint_hint":"重画选中的区域",
		"ui.studio.stage.action.inpaint_target":"选区",
		"ui.studio.stage.action.enlarge":"大图",
		"ui.studio.stage.action.enlarge_hint":"全窗口查看这张图",
		"ui.studio.stage.action.enlarge_target":"舞台上当前这张图",
		"ui.canvas.toolbar.select":"选择",
		"ui.canvas.toolbar.select_hint":"点选图层，拖动来移动",
		"ui.canvas.toolbar.undo":"撤销",
		"ui.canvas.toolbar.undo_hint":"撤销上一步操作",
		"ui.studio.stage.view.texture":"贴图",
		"ui.studio.stage.view.texture_hint":"显示模型自带的贴图和颜色",
		"ui.studio.rail.expand":"展开",
		"ui.studio.rail.collapse":"收起",
		"ui.studio.rail.expand_hint":"展开后，每个按钮旁边显示名字",
		"ui.studio.rail.collapse_hint":"收起后，只显示图标",
		"ui.studio.panel.tab.layers":"图层",
		"ui.studio.panel.tab.layers_hint":"画板上的每一层",
		"ui.studio.panel.tab.details":"详情",
		"ui.studio.panel.tab.details_hint":"这个版本的来源",
		"ui.studio.panel.tab.process":"加工",
		"ui.studio.panel.tab.process_hint":"选加工步骤",
		"error.legacy_generated_image_not_found":"文件不存在",
		"error.invalid_capability":"能力名称无效",
		"error.invalid_input":"请求参数无效",
		"error.unknown_offering":"指定模型不存在",
		"error.no_offering":"当前没有可用模型",
		"error.idempotency_conflict":"幂等冲突",
		"error.not_found":"资源不存在",
		"error.conflict":"状态冲突",
		"error.unavailable":"服务不可用",
		"error.invalid_json":"JSON 无效",
		"error.trailing_json":"JSON 尾部有额外内容",
		"error.required_parameter":"缺少必填参数",
		"error.task_parameters_encode":"任务参数无法编码",
		"error.task_not_found":"任务不存在",
		"error.task_parameters_corrupt":"任务参数损坏",
		"error.prompt_enhance_required":"提示词不能为空",
		"error.prompt_enhance_failed":"提示词增强失败",
		"error.provider_balance_failed":"获取余额失败",
		"error.input_asset_service_unavailable":"输入资产服务未配置",
		"error.multipart_required":"请求必须是 multipart/form-data",
		"error.invalid_upload":"上传文件无效",
		"error.forbidden":"无权执行此操作",
		"error.legacy_asset_missing_upload":"未上传文件",
		"error.legacy_asset_upload_too_large":"文件超过 {limit} 限制",
		"error.legacy_asset_upload_failed":"上传处理失败",
		"error.legacy_asset_not_found":"资源不存在",
		"error.legacy_asset_forbidden":"无权限访问",
		"error.legacy_asset_get_failed":"获取资源失败",
		"error.legacy_task_delete_not_found":"任务不存在",
		"error.legacy_task_delete_failed":"删除任务失败",
		"error.limit_positive":"limit 必须是正整数",
		"error.after_nonnegative":"after 必须是非负整数",
		"ui.providers.load_failed":"加载模型列表失败",
		"ui.header.menu_toggle":"Toggle menu",
		"ui.header.failures_show_title":"点击显示失败任务",
		"ui.header.failures_hide_title":"点击隐藏失败任务",
		"ui.header.failures_hidden":"已隐藏失败",
		"ui.header.failures_visible":"显示失败",
		"ui.header.cache_count":"缓存中 {count}",
		"ui.header.cache_count_one":"缓存中 {count}",
		"ui.header.cache_tooltip":"正在缓存 {count} 个资源",
		"ui.header.cache_tooltip_one":"正在缓存 {count} 个资源",
		"ui.header.cache_tooltip_details":"正在缓存 {count} 个资源：{items}",
		"ui.header.cache_tooltip_details_one":"正在缓存 {count} 个资源：{items}",
		"ui.header.cache_item":"[{capability} {task}] {asset}",
		"ui.header.cache_task_id":"#{id}",
		"ui.header.unknown_task":"未知任务",
		"ui.header.unknown_task_id":"#未知",
		"ui.header.unknown_asset":"资源",
		"ui.header.logout":"退出登录",
		"ui.header.logout_success":"已退出登录",
		"ui.header.logout_failed":"登出失败",
		"ui.header.default_user_initial":"U",
		"ui.asset.model":"模型({suffix})",
		"ui.asset.texture":"贴图({suffix})",
		"ui.asset.animation":"动画({suffix})",
		"ui.asset.result":"结果({suffix})",
		"ui.workbench.prompt_section":"提示词",
		"ui.workbench.parameters_section":"生成参数",
		"ui.workbench.enhance":"AI 增强",
		"ui.workbench.regenerate":"重新生成",
		"ui.workbench.progress":"任务进度",
		"ui.workbench.task_id_prefix":"ID:",
		"ui.workbench.not_found":"任务不存在或已过期",
		"ui.workbench.result":"生成结果",
		"ui.workbench.prompt_required":"请输入提示词",
		"ui.workbench.enhance_prompt_required":"请先输入提示词",
		"ui.workbench.enhance_failed":"提示词增强失败",
		"ui.workbench.enhance_no_change":"增强未产生变化",
		"ui.workbench.enhance_success":"提示词已增强",
		"ui.task_progress.retry_badge":"重试 {retryCount}/{maxRetries}",
		"ui.task_progress.retry_imminent":"即将重试",
		"ui.task_progress.retry_seconds":"约 {value} 秒后重试",
		"ui.task_progress.retry_minutes":"约 {value} 分钟后重试",
		"ui.task_progress.retry_hours":"约 {value} 小时后重试",
		"ui.task_progress.percentage":"{value}%",
		"ui.text_to_image.description":"通过文本生成图片",
		"ui.text_to_image.positive_prompt_placeholder":"描述图片",
		"ui.text_to_image.positive_prompt_label":"正向提示词",
		"ui.text_to_image.negative_prompt_placeholder":"排除内容",
		"ui.text_to_image.negative_prompt_label":"负向提示词",
		"ui.text_to_image.generate":"生成图片",
		"ui.text_to_image.task_created":"任务已创建",
		"ui.text_to_image.create_failed":"创建任务失败",
		"ui.text_to_image.empty":"输入提示词并点击生成",
		"behavior.image_to_image.multi_reference_prompt":"Transform references. {prompt}",
		"ui.image_to_image.description":"图片转换",
		"ui.image_to_image.reference_section":"参考图片",
		"ui.image_to_image.reference_description":"上传 {min}-{max} 张图片",
		"ui.image_to_image.reference_target":"第一张为目标图",
		"ui.image_to_image.reference_style_suffix":"后续为风格图",
		"ui.image_to_image.reference_required":"至少上传 {min} 张图片",
		"ui.image_to_image.positive_prompt_placeholder":"描述转换效果",
		"ui.image_to_image.positive_prompt_label":"正向提示词",
		"ui.image_to_image.negative_prompt_placeholder":"排除内容",
		"ui.image_to_image.negative_prompt_label":"负向提示词",
		"ui.image_to_image.convert":"开始转换",
		"ui.image_to_image.reconvert":"重新转换",
		"ui.image_to_image.task_created":"转换任务已创建",
		"ui.image_to_image.create_failed":"转换任务创建失败",
		"ui.image_to_image.aspect_ratio_warning":"宽高比提示",
		"ui.image_to_image.result":"转换结果",
		"ui.image_to_image.empty":"上传图片后开始转换",
		"ui.tasks.title":"任务历史",
		"ui.tasks.description":"查看所有任务记录",
		"ui.tasks.all":"全部",
		"ui.tasks.total_prefix":"共",
		"ui.tasks.record_suffix":"条记录",
		"ui.tasks.current_filter_prefix":"当前筛选",
		"ui.tasks.search_placeholder":"搜索任务",
		"ui.tasks.empty":"暂无任务",
		"ui.pagination.previous":"上一页",
		"ui.pagination.next":"下一页",
		"ui.pagination.page_prefix":"第",
		"ui.pagination.page_separator":"/",
		"ui.pagination.page_suffix":"页",
		"failure.invalid_input":"任务输入无效",
		"failure.authentication":"上游身份验证失败",
		"failure.rate_limited":"上游请求过于频繁",
		"failure.transport":"无法连接上游服务",
		"failure.timed_out":"上游请求超时",
		"failure.rejected":"上游拒绝任务",
		"failure.insufficient_balance":"上游账户余额不足",
		"failure.credential_quota":"该 API Key 已达额度上限",
		"failure.upstream_unavailable":"上游服务不可用",
		"failure.submission_unknown":"无法确认任务提交",
		"failure.integrity_failure":"资产校验失败",
		"failure.protocol":"上游协议错误",
		"failure.internal":"任务处理失败",
		"failure.source_expired":"来源原任务已过期"
	}},
	"chat":{
		"api":{
			"session_list":{"default_page_size":15,"maximum_page_size":100},
			"message_list":{"default_page_size":50,"maximum_page_size":200},
			"fields":{
				"title":"title","content":"content","image_urls":"image_urls",
				"aspect_ratio":"aspect_ratio","pose_mode":"pose_mode",
				"ai_model":"ai_model","size":"size","image_size":"image_size",
				"skill":"_skill","skill_id":"id","skill_name":"name",
				"reference_image_indices":"reference_image_indices",
				"attachments":"attachments","selection":"selection","mentions":"mentions","input_source":"inputSource","domain_models":"domainModels",
				"from_id":"fromId","approval_mode":"approvalMode","notes":"notes",
				"agent_scope":"agentScope"
			},
			"response_fields":{
				"id":"id","title":"title","created_at":"createdAt",
				"updated_at":"updatedAt","count":"_count","messages":"messages",
				"role":"role","content":"content","image_urls":"imageUrls",
				"attachments":"attachments",
				"tool_calls":"toolCalls","tool_name":"name",
				"tool_arguments":"arguments","message_id":"messageId",
				"session_id":"sessionId","error":"error",
				"is_pending":"isPending","pending_task_types":"pendingTaskTypes",
				"images":"images","url":"url","task_id":"taskId",
				"approval_mode":"approvalMode","estimated_credits":"estimatedCredits",
				"approval_source":"approvalSource","notes":"notes","pick":"pick",
				"agent_scope":"agentScope"
			},
			"attachment":{
				"handle":"handle","kind":"kind","role":"role","asset_id":"assetId",
				"task_id":"taskId","capture_id":"captureId","image_slot":"imageSlot","token":"token",
				"url":"url","canvas_layers":"canvasLayers"
			},
			"prompt_enhance":{
				"prompt_field":"prompt","capability_field":"capability",
				"model_field":"model","result_field":"enhanced_prompt",
				"prompt_result_field":"prompt",
				"negative_prompt_result_field":"negative_prompt",
				"error_field":"error",
				"default_profile":"image",
				"capability_profiles":{"text-to-image":"image"},
				"profiles":{
					"image":{
						"system_prompt":"system {length_rule}",
						"length_rule_placeholder":"{length_rule}",
						"bounded_length_rule":"max {max_length}",
						"unbounded_length_rule":"unbounded",
						"max_length_placeholder":"{max_length}",
						"prompt_field":"prompt",
						"negative_prompt_field":"negative_prompt",
						"default_negative_prompt":"bad"
					}
				},
				"temperature":0
			}
		},
		"defaults":{"title":"新对话"},
		"roles":{"system":"system","user":"user","assistant":"assistant","tool":"tool"},
		"state":{
			"pending_content":"__PENDING__","active":"active","done":"done",
			"error":"error","turn_queued":"queued","turn_running":"running","turn_waiting_task":"waiting_task",
			"turn_done":"done","turn_error":"error",
			"placeholder_stale_milliseconds":300000,
			"failed_prompt_preview_characters":50
		},
		"frontend":{
			"connection_timeout_milliseconds":15000,
			"fallback_poll_milliseconds":3000,
			"reconnect_check_delay_milliseconds":300,
			"option_storage_key":"test-chat-options",
			"add_images_event_name":"test:add-images",
			"image_mime_prefix":"image/",
			"image_file_accept":"image/*",
			"send_key":"Enter","send_modifier":"not-shift",
			"lightbox_close_key":"Escape",
			"lightbox_previous_key":"ArrowLeft",
			"lightbox_next_key":"ArrowRight",
			"textarea_maximum_height_pixels":128,
			"optimistic_message_id_prefix":"__stream_",
			"optimistic_user_message_id":"__stream_user__",
			"optimistic_assistant_message_id":"__stream_assistant__",
			"filename_query_parameter":"filename",
			"download_filename_max_characters":60,
			"download_task_id_characters":8,
			"download_extension":"png",
			"fallback_download_identifier":"img",
			"internal_api_path_prefix":"/api/",
			"legacy_hidden_content_prefix":"[hidden:",
			"legacy_hidden_content_suffix":"]",
			"progress_maximum":100
		},
		"agent":{
			"adapter_key":"openai-compatible","base_url_setting":"llm_base_url",
			"api_key_setting":"llm_api_key","model_setting":"llm_model",
			"endpoint_path":"/chat/completions",
			"authorization_header":"Authorization","authorization_type":"Bearer",
			"temperature":0,"stream":true,"vision":true,
			"request_timeout_seconds":600,"scanner_max_bytes":1048576,
			"consent_wait_timeout_seconds":600,
						"consent_approved_content":"approved","consent_denied_content":"denied",
			"consent_waiting_content":"waiting","consent_duplicate_content":"duplicate","consent_blocked_content":"blocked",
			"consent_required_content":"required","consent_limit_content":"limit",
			"consent_reconcile_content":"reconcile original paid call before any new submission",
			"max_generation_calls_per_turn":4,"max_listed_models":20,
			"max_tool_calls_per_reply":3,"max_tool_calls_per_reply_setting":"agent_max_tool_calls_per_reply","max_tool_calls_per_reply_maximum":100,
			"error_body_max_bytes":65536,
			"system_prompt":"configured system",
			"title_prompt":"{maximum}: {content}","title_max_characters":20,
			"title_timeout_seconds":5,
			"worker_count":2,"queue_size":8,
			"mock_tool_name":"refine_image",
			"mock_prompt_argument":"prompt",
			"mock_summary":"configured mock summary"
		},
		"context":{
			"maximum_vision_images":4,"recent_image_limit":4,
			"vision_inline_max_bytes":4194304,
			"vision_derivative":{"long_side_steps":[2048,1024],"jpeg_qualities":[90,70],"flatten_color":"#ffffff",
				"maximum_input_bytes":67108864,"maximum_pixels":100000000,"decode_concurrency":2,"cache_maximum_bytes":67108864},
			"asset_url_template":"/api/v1/tasks/{task_id}/assets/{asset_id}",
			"tool_prompt_argument":"prompt",
			"tool_reference_argument":"reference_image_indices",
			"previous_prompt_template":"{prompt}",
			"reference_filename":"configured-reference",
			"maximum_messages":20,"image_index_base":1,
			"tool_prompt_preview_characters":120,
			"text_part_kind":"configured-text-part",
			"image_part_kind":"configured-image-part",
			"image_detail":"configured-detail",
			"user_image_label_template":"\nconfigured-user-{index}",
			"canvas_input_source":"canvas",
			"ai_image_label_template":"\nconfigured-ai-{index}",
			"current_upload_hint_template":"configured-{count}-{tool}",
			"handle_prefixes":{"image":"@img","model":"@model"},
			"selection_alias":"@selected",
			"reference_handles_argument":"reference_handles",
			"attachment_label_templates":{"image.upload":"\n[{index} {handle}]"},
			"unpictured_attachment_templates":{"image":"\n[{handle}]","model":"\n[{handle} {task_id}]","image.canvas":"\n[{handle} {layout}]"},
			"kind_labels":{"image":"image","model":"model"},
			"selection_template":"\n{alias}{items}",
			"selection_item_template":"\n{handle} {kind}{details}",
			"selection_task_template":" {task_id}",
			"selection_capture_template":" {capture_id}",
			"unknown_handle_template":"unknown {handle}",
			"wrong_handle_kind_template":"wrong {handle}",
			"maximum_attachments_per_message":16,"maximum_selection":8,
			"recent_generated_image_limit":2,"maximum_session_notes_characters":2000,
			"session_notes_template":"\n{open}{notes}{close}",
			"session_notes_open_marker":"[notes]",
			"session_notes_close_marker":"[/notes]",
			"session_notes_saved_message":"saved",
			"session_notes_too_long_template":"max {maximum} got {length}",
			"selection_picked_template":"picked",
			"canvas_layout_template":"{width}x{height}{layers}",
			"canvas_layers_template":" {layers}",
			"selection_canvas_template":" {layout}",
			"selection_canvas_note":"canvas",
			"maximum_canvas_layers":100,
			"canvas_output_size_template":"{size} {canvas}",
			"canvas_output_size_nearest_template":"nearest {size} {canvas}"
		},
		"dsml":{
			"enabled":true,"markers":["configured-marker-"],
			"tool_calls_elements":["calls"],"invoke_element":"run",
			"parameter_element":"arg","name_attribute":"key",
			"string_attribute":"text","true_value":"yes","false_value":"no",
			"call_id_template":"configured-{index}"
		},
		"stream":{
			"retry_milliseconds":86400000,"heartbeat_milliseconds":10000,
			"cleanup_ttl_milliseconds":30000,
			"completion_flush_milliseconds":500,
			"status_check_milliseconds":1000,
			"subscriber_buffer_size":8,
			"write_timeout_milliseconds":1000,
			"id_frame_template":"id: {id}\n",
			"event_prefix":"data: ","event_suffix":"\n\n",
			"heartbeat_frame":": heartbeat\n\n",
			"retry_frame_template":"retry: {milliseconds}\n\n",
			"headers":{"Content-Type":"text/event-stream"},
			"event_fields":{
				"type":"type","content":"content","percent":"percent",
				"task_id":"taskId","urls":"urls","message_id":"msgId",
				"capability":"capability","prompt":"prompt","ai_model":"aiModel",
				"session_id":"sessionId","final_task_id":"finalTaskId",
				"estimated_credits":"estimatedCredits","approval_source":"approvalSource",
				"status":"status","progress":"progress","result":"result",
				"image_urls":"image_urls","error":"error"
			},
			"event_types":{
				"text":"text","status":"status","progress":"progress",
				"images":"images","title":"title",
				"message_created":"msg_created",
				"task_registered":"task_registered","task_update":"task_update",
				"error":"error","done":"done"
			}
		},
		"tools":[{
			"name":"refine_image","description":"configured tool","canvas_source":"bind",
			"execution":{"implementation_key":"generation.execute","effects":["PAID_TASK"],"scope":"session","domains":["image"],"capabilities":["text-to-image"],"consent":"human","idempotency":"required","cancellation":"task","agent_scope":"2d"},
			"capability":"text-to-image",
			"fallback_capability":"text-to-image",
			"pending_capability":"image-to-image",
			"parameters":{"type":"object"},
			"argument_mappings":{"prompt":"prompt"},
			"override_mappings":{"aspect_ratio":"aspect_ratio"},
			"default_parameters":{},
			"result_asset_roles":["image"],
			"prompt_with_reference_template":"Edit {prompt}",
			"prompt_with_previous_template":"{previous_prompt}: {prompt}",
			"idempotency_key_template":"{session_id}:{call_id}",
			"result_fields":{
				"success":"success","task_id":"task_id",
				"image_urls":"image_urls","error":"error","warning":"warning"
			}
		}],
		"skills":{
			"feature":"skills",
			"fields":{
				"revision":"revision","skills":"skills","id":"id","name":"name",
				"name_zh":"name_zh","category":"category",
				"recommended_size":"recommended_size",
				"thumbnail_path":"thumbnail_path","raw_image_url":"raw_image_url",
				"prompt":"prompt",
				"image_to_image_template":"image_to_image_template",
				"tags":"tags","tool":"tool","requires_image":"requires_image","default_message":"default_message","one_shot":"one_shot","error":"error"
			},
			"categories":[{
				"id":"test-category","label":"测试分类","hidden_capabilities":[]
			}],
			"execution":{
				"prompt_template_fields":{
					"user_input":"{user_input}","skill_prompt":"{skill_prompt}"
				},
				"prompt_templates":{
					"text-to-image":"{user_input}\n\n{skill_prompt}"
				},
				"parameter_overrides":{"quality":"high"}
			}
		},
		"presentation":{
			"messages":{
				"session_not_found":"会话不存在","message_not_found":"消息不存在",
				"empty_title":"标题不能为空","empty_message":"消息内容不能为空",
				"invalid_parameters":"参数无效",
				"invalid_reference_indices":"参考图序号无效",
				"reference_index_out_of_range_template":"参考图序号 {index} 超出 {count}",
				"invalid_input_source":"来源无效","canvas_source_missing":"画布缺失","canvas_source_tool_refused":"改用 {tools}",
				"agent_images_unsupported":"模型不读图","agent_not_configured":"Agent 未配置",
				"interrupted":"中断","interrupted_prompt":"中断 {prompt}",
				"generation_failed":"生成失败","generation_error":"生成异常",
				"summary_failed":"总结失败","no_result":"无结果",
				"unknown_error":"未知错误","sse_interrupted":"连接中断",
				"internal_error":"内部错误","unauthenticated":"未登录",
				"turn_active":"当前会话正在处理",
				"agent_busy":"任务繁忙",
				"tool_round_limit":"上限 {limit}","tool_round_limit_pending":"未执行：",
				"tool_round_limit_pending_item":"{tool} {intent}",
				"tool_round_limit_pending_tool":"{tool}"
			},
			"status":{"generating":"正在生成"}
		},
		"audit":{
			"create_action":"access_asset","delete_action":"access_asset",
			"session_resource_type":"chat_session",
			"message_resource_type":"chat_message",
			"approval_mode_action":"set_approval_mode",
			"agent_scope_action":"set_agent_scope"
		},
		"agent_scope":{
			"default_mode":"auto",
			"modes":[
				{"id":"2d","label":"2D","description":"only 2D","allows":["2d","utility"],"workspace_domains":["image"]},
				{"id":"3d","label":"3D","description":"only 3D","allows":["3d","utility"],"workspace_domains":["model-3d"]},
				{"id":"auto","label":"Auto","description":"everything","allows":["2d","3d","cross","utility"],"workspace_domains":["image","model-3d"]}
			],
			"context_template":"scope {mode}: {description} allowed {allowed_tools} blocked {blocked_tools}",
			"empty_list_text":"none",
			"rejection_template":"{tool} refused in {mode}",
			"mock_refusal":"not allowed: {tool}"
		},
		"approval_policy":{
			"default_mode":"manual","modes":["auto","manual"],
			"manual_approval_capabilities":["text-to-image"]
		},
		"pending_approvals":{
			"ttl_seconds":86400,"maximum_listed":20,
			"monitor_interval_milliseconds":2000,"monitor_batch_size":50,
			"messages":{
				"started":"started {model}","succeeded":"done {model}",
				"denied":"denied","submit_failed":"submit failed","expired":"expired","cancelled":"cancelled",
				"denied_extra_credits":"denied {extra}","extra_items_separator":", "
			}
		}
	},
	"canvas":{
		"api":{
			"fields":{
				"id":"id","created":"created","user_id":"userId","name":"name",
				"session_id":"sessionId","viewport":"viewport",
				"bg_style":"bgStyle","created_at":"createdAt",
				"updated_at":"updatedAt","nodes":"nodes","count":"count",
				"node_count":"_count","ok":"ok","error":"error",
				"project_id":"projectId","revision":"revision","type":"type","x":"x","y":"y",
				"width":"width","height":"height","z_index":"zIndex",
				"data":"data","visible":"visible","locked":"locked",
				"asset_id":"assetId","url":"url","image_url":"imageUrl",
				"placed_results":"placedResults","canvas_width":"canvasWidth","canvas_height":"canvasHeight","coordinate_version":"coordinateVersion"
			},
			"path_parameters":{"project_id":"projectID"}
		},
		"defaults":{
			"project_name":"未命名项目",
			"viewport":"{\"zoom\":1,\"panX\":0,\"panY\":0}",
			"bg_style":"dots","project_id_prefix":"canvas",
			"node_id_prefix":"node","node_x":0,"node_y":0,
			"node_width":256,"node_height":256,"node_z_index":0,
			"node_data":"{}","node_visible":true,"node_locked":false,
			"merge_filename_prefix":"canvas-merge","merge_extension":".png",
			"merge_media_type":"image/png","merge_asset_type":"image","coordinate_version":2
		},
		"limits":{"maximum_merge_nodes":20,"maximum_bitmap_nodes":50,"maximum_dimension":8192,"maximum_placed_results":5000,"maximum_placed_result_key_characters":512,"minimum_dimension":64},
		"audit":{
			"create_action":"access_asset",
			"delete_action":"access_asset",
			"merge_action":"access_asset",
			"project_resource_type":"canvas_project",
			"merge_resource_type":"canvas_merge",
			"detail_fields":{"name":"name"}
		},
		"frontend":{
			"focus_delay_milliseconds":100,
			"auto_save_delay_milliseconds":1500,
			"saved_indicator_milliseconds":2000,
			"context_menu_bind_delay_milliseconds":0,
			"permission_redirect_path":"/tasks",
			"initial_node_x":40,"initial_node_y":40,"node_gap_pixels":20,
			"minimum_node_size":50,
			"maximum_node_edge":512,"model_capture_long_edge_pixels":2048,"to_3d_long_edge_pixels":1024,"upload_task_id":"upload",
			"initial_layer":{"enabled":true,"fill":"transparent","maximum_bitmap_pixels":16777216},"artboard":{"center_x":600,"center_y":400,"resize":{"default_anchor":"center","anchors":["center","top-left"]},"legacy_max_edge":512,"default_width":2048,"default_height":2048,"ratio_pixels":{"1:1":{"width":1024,"height":1024}},"frame_stroke_width":1,"frame_line_dash":[6,4],"label":{"common_ratios":["1:1","3:2"],"font_size_pixels":10,"offset_pixels":4,"color_token":"--muted-foreground","padding_pixels":3,"background_token":"--background","background_opacity":0.72},"outside_dim":{"color":"#000000","opacity":0.45},"transparency":{"cell_pixels":8,"light_color":"#ffffff","dark_color":"#000000","opacity":0.08}},
			"brush":{"minimum_size":1,"maximum_size":200,"default_size":12},
			"selection":{"ants_dash":[5,5],"ants_tick_milliseconds":120,"tint_color":"#6366f1","tint_opacity":0.3,"preview_max_edge":512},
			"mask":{"tint_color_token":"--info","tint_opacity":0.75},
			"snap":{"default_enabled":true,"threshold_pixels":6,"guide_stroke_width":1,"guide_color_token":"--reminder","group_member_targets":false},
			"export_filename_fallback":"canvas","export_extension":".png",
			"export":{
				"padding_pixels":20,
				"default_background_color":"#ffffff",
				"transparent_background_value":"transparent",
				"artboard_background_color":"transparent",
				"output_media_type":"image/png",
				"image_placeholder_color":"#e5e7eb",
				"frame":{
					"stroke_color":"#6366f1","stroke_width":2,
					"line_dash":[6,4]
				},
				"arrow":{"head_length":10,"head_angle_divisor":6},
				"pencil":{"line_cap":"round","line_join":"round"}
			},
			"viewport":{
				"default_zoom":1,"default_pan_x":0,"default_pan_y":0,
				"minimum_zoom":0.1,"maximum_zoom":3,"zoom_step":0.1,
				"decimal_places":2,"percentage_multiplier":100,
				"fit_padding_pixels":40,"wheel_line_height_pixels":16
			},
			"drawing":{
				"default_node_size":256,"text_node_width":200,
				"text_node_height":40,"initial_shape_size":2,
				"pencil_minimum_points":2,"pencil_padding":4,
				"minimum_draw_size":1,"line_height":4,
				"minimum_committed_shape_size":10,
				"selection_drag_threshold_pixels":4,"brush_idle_commit_milliseconds":1500
			},
			"history":{
				"maximum_depth":50,"recent_limit":10,
				"snapshot_throttle_milliseconds":500
			},
			"clipboard":{"offset_x":20,"offset_y":20},
			"style":{
				"default_stroke_width":2,
				"default_stroke_color":"#333333",
				"default_shape_stroke_color":"#6b7280",
				"default_fill_color":"transparent","default_background_color":"#ffffff",
				"transparent_fill_value":"transparent",
				"preset_colors":["#333333","#ffffff"],
				"stroke_widths":[1,2,5],"font_sizes":[16,24],"default_font_size":24,
				"rgb_minimum":0,"rgb_maximum":255,
				"selection_border":"1px solid rgba(99, 102, 241, 0.5)",
				"selection_background":"rgba(99, 102, 241, 0.1)"
			},
			"backgrounds":[
				{
					"value":"dots","label_key":"toolbar.background.dots",
					"pattern":"dots","background_color":"#ffffff",
					"pattern_color":"#e5e7eb",
					"cell_size":20,"pattern_width":1
				}
			],
			"keyboard":{
				"space_code":"Space","space_tap_milliseconds":250,"recenter_key":"0","undo_key":"z","redo_key":"z",
				"alternate_redo_key":"y","copy_key":"c","paste_key":"v",
				"select_all_key":"a","group_key":"g","layer_forward_key":"]","layer_backward_key":"[","select_tool_key":"v",
				"hand_tool_key":"h","marquee_tool_key":"m","lasso_tool_key":"l","mask_tool_key":"q","crop_tool_key":"c",
				"brush_tool_key":"b","eraser_tool_key":"e","text_tool_key":"t","shape_tool_key":"u",
				"shape_cycle":["rectangle","ellipse","line","arrow"],"zoom_tool_key":"z","eyedropper_tool_key":"i",
				"brush_size_down_key":"[","brush_size_up_key":"]","brush_size_step_ratio":0.1,
				"swap_colors_key":"x","default_colors_key":"d","help_key":"?","mask_confirm_key":"Enter","mask_exit_key":"Escape","delete_keys":["delete","backspace"],
				"generate_shortcut":"mod+enter","lock_shortcut":"mod+/","merge_visible_shortcut":"mod+alt+shift+e",
				"cut_shortcut":"mod+x","duplicate_shortcut":"mod+j","deselect_shortcut":"mod+d","invert_selection_shortcut":"mod+shift+i",
				"zoom_in_shortcut":"mod+=","zoom_out_shortcut":"mod+-","zoom_actual_shortcut":"mod+1","zoom_selection_shortcut":"mod+alt+0",
				"layers_panel_shortcut":"f7","history_panel_shortcut":"alt+f9",
				"nudge_pixels":1,"nudge_large_pixels":10,"reserved_shortcuts":["mod+a","f7"]
			},
			"text":{"default_color":"#000000","font_family":"sans-serif","line_height":1.2},
			"relative_time":{
				"milliseconds_per_minute":60000,"minutes_per_hour":60,
				"hours_per_day":24,"days_per_month":30,"locale":"zh-CN"
			},
			"processing_action":{"calls_model":false,"cost_hint":"process_panel","group":"produce"},
			"stage_actions":[
				{
					"id":"inpaint","kinds":["image"],"group":"produce","calls_model":true,"cost_hint":"approval_card",
					"icon":"wand-sparkles","label_key":"ui.studio.stage.action.inpaint",
					"hint_key":"ui.studio.stage.action.inpaint_hint","target_key":"ui.studio.stage.action.inpaint_target"
				},
				{
					"id":"enlarge","kinds":["image"],"group":"view","calls_model":false,"cost_hint":"free",
					"icon":"maximize-2","label_key":"ui.studio.stage.action.enlarge",
					"hint_key":"ui.studio.stage.action.enlarge_hint","target_key":"ui.studio.stage.action.enlarge_target",
					"shortcut":"space_code"
				}
			],
			"tools":[
				{
					"id":"select","rail":"canvas","section":"select","icon":"mouse-pointer-2",
					"label_key":"ui.canvas.toolbar.select","hint_key":"ui.canvas.toolbar.select_hint",
					"shortcut":"select_tool_key"
				},
				{
					"id":"undo","rail":"canvas","section":"history","icon":"undo-2",
					"label_key":"ui.canvas.toolbar.undo","hint_key":"ui.canvas.toolbar.undo_hint",
					"shortcut":"undo_key","modifiers":["mod"]
				},
				{
					"id":"texture","rail":"viewer","section":"views","icon":"wallpaper",
					"label_key":"ui.studio.stage.view.texture","hint_key":"ui.studio.stage.view.texture_hint"
				}
			],
			"panel_tabs":{
				"width_pixels":300,
				"tabs":[
					{"id":"layers","icon":"layers","label_key":"ui.studio.panel.tab.layers","hint_key":"ui.studio.panel.tab.layers_hint","shortcut":"layers_panel_shortcut"},
					{"id":"details","icon":"info","label_key":"ui.studio.panel.tab.details","hint_key":"ui.studio.panel.tab.details_hint"},
					{"id":"process","icon":"sliders-horizontal","label_key":"ui.studio.panel.tab.process","hint_key":"ui.studio.panel.tab.process_hint"}
				],
				"kinds":[
					{"kind":"image","tabs":["layers","details"],"default":"layers"},
					{"kind":"board","tabs":["layers"],"default":"layers"},
					{"kind":"model","tabs":["process","details"],"default":"process","default_when_unavailable":"details"}
				]
			}
		},
		"presentation":{
			"messages":{
				"forbidden":"需要管理员权限","project_not_found":"项目不存在",
				"nodes_must_array":"参数 nodes 必须是数组",
				"bitmap_limit_exceeded":"画板图片数量超出限制",
				"invalid_merge":"参数错误","dimension_limit":"尺寸超限",
				"merge_failed":"合并失败",
				"internal_error":"内部错误",
				"placed_results_invalid":"已放置结果清单无效","canvas_frame_invalid":"画布尺寸无效"
			},
			"text":{
				"project.list.new":"新建画板",
				"project.list.empty":"暂无画板",
				"project.list.node_summary":"{count} 个节点 · {time}",
				"project.list.node_summary_one":"{count} 个节点 · {time}",
				"relative.just_now":"刚刚",
				"relative.minutes_ago":"{count} 分钟前",
				"relative.minutes_ago_one":"{count} 分钟前",
				"relative.hours_ago":"{count} 小时前",
				"relative.hours_ago_one":"{count} 小时前",
				"relative.days_ago":"{count} 天前",
				"relative.days_ago_one":"{count} 天前",
				"task.image_edit":"图片修改",
				"task.image_generate":"图片生成",
				"task.pending":"正在{task}中",
				"generator.hint":"请输入描述",
				"status.generating":"正在生成图片",
				"error.load_messages":"加载消息失败",
				"error.create_canvas":"创建画板失败",
				"error.create_session":"创建会话失败",
				"error.request_failed":"请求失败 ({status})",
				"error.sse_interrupted":"SSE 连接中断",
				"error.send_failed":"发送失败",
				"error.delete_failed":"删除失败",
				"error.require_session":"请选择画板",
				"error.require_selection":"请选择素材",
				"error.selection_no_images":"没有图片",
				"error.export_empty":"画板为空",
				"error.export_failed":"导出失败",
				"success.image_sent":"已发送 {count} 张图片",
				"success.image_sent_one":"已发送 {count} 张图片",
				"status.merging":"正在合并",
				"success.merged_sent":"已发送",
				"success.exported":"已导出",
				"permission.denied":"无权限",
				"save.saving":"保存中",
				"save.saved":"已保存",
				"save.failed_retry":"重试",
				"empty.title":"AI 画板",
				"empty.description_line1":"描述一",
				"empty.description_line2":"描述二",
				"panel.generated_files":"生成文件",
				"panel.layers":"图层",
				"board.empty":"画板为空",
				"node.generated_alt":"生成图片",
				"node.edit_text_placeholder":"双击编辑文字",
				"node.frame_default_text":"Frame",
				"node.send_reference":"发送参考图",
				"node.reference":"参考图",
				"toolbar.background":"背景",
				"toolbar.background.dots":"点阵",
				"toolbar.background.grid":"网格",
				"toolbar.background.plain":"纯白",
				"toolbar.background.dark":"深色",
				"toolbar.select":"选择",
				"toolbar.select_tooltip":"选择工具 ({key})",
				"toolbar.hand":"平移",
				"toolbar.hand_tooltip":"平移工具 ({key})",
				"toolbar.recenter":"回到画布（空格）",
				"toolbar.upload":"上传",
				"toolbar.snap":"磁吸",
				"toolbar.snap_on":"磁吸已开启",
				"toolbar.snap_off":"磁吸已关闭",
				"toolbar.shapes":"形状",
				"toolbar.rectangle":"矩形",
				"toolbar.arrow":"箭头",
				"toolbar.line":"直线",
				"toolbar.ellipse":"椭圆",
				"toolbar.text":"文字",
				"toolbar.pencil":"画笔",
				"toolbar.generate":"生成",
				"toolbar.zoom_out":"缩小",
				"toolbar.zoom_in":"放大",
				"toolbar.reset":"重置",
				"toolbar.fit":"适应",
				"toolbar.export":"导出",
				"chat.default_title":"对话",
				"chat.new_board":"新建画板",
				"chat.close":"关闭",
				"chat.loading":"加载中",
				"chat.empty":"暂无消息",
				"chat.example_one":"示例一",
				"chat.example_two":"示例二",
				"files.generated_count":"生成 {count}",
				"files.generated_count_one":"生成 {count}",
				"files.uploaded_count":"上传 {count}",
				"files.uploaded_count_one":"上传 {count}",
				"files.empty":"暂无文件",
				"files.empty_description":"暂无文件描述",
				"files.uploaded_image":"上传图片",
				"files.ai_image":"AI {id}",
				"files.image_alt":"图片 {id}",
				"files.hidden":"已隐藏",
				"files.download":"下载",
				"layers.history":"历史",
				"layers.empty_history":"暂无历史",
				"layers.empty":"暂无图层",
				"layers.node.uploaded_image":"上传图片",
				"layers.node.image":"图片 {id}",
				"layers.node.text":"文字",
				"layers.node.frame":"画框",
				"layers.node.arrow":"箭头",
				"layers.node.rectangle":"矩形",
				"layers.node.generic":"节点",
				"layers.unlock":"解锁",
				"layers.lock":"锁定",
				"layers.hide":"隐藏",
				"layers.show":"显示",
				"context.copy":"复制",
				"context.send_to_chat":"发送到对话",
				"context.group":"成组",
				"context.ungroup":"解组",
				"context.paste":"粘贴",
				"context.bring_to_front":"置顶",
				"context.bring_forward":"上一层",
				"context.send_backward":"下一层",
				"context.send_to_back":"底层",
				"context.delete":"删除",
				"context.unlock":"解锁",
				"context.lock":"锁定",
				"context.show":"显示",
				"context.hide":"隐藏",
				"context.select_all":"全选",
				"shortcut.copy":"Ctrl+C",
				"shortcut.group":"Ctrl+G",
				"shortcut.ungroup":"Ctrl+Shift+G",
				"shortcut.paste":"Ctrl+V",
				"shortcut.delete":"Delete",
				"shortcut.select_all":"Ctrl+A",
				"history.add.image":"添加图片节点",
				"history.add.text":"添加文字节点",
				"history.add.frame":"添加框选节点",
				"history.add.shape":"添加形状节点",
				"history.add.pencil":"添加画笔节点",
				"history.add.generic":"添加节点",
				"history.delete":"删除节点",
				"history.move":"移动节点",
				"history.style":"修改样式",
				"history.edit_text":"编辑文字",
				"history.front":"置顶图层",
				"history.back":"置底图层",
				"history.forward":"上移图层",
				"history.backward":"下移图层",
				"history.visibility":"切换可见性",
				"history.reorder":"排序图层",
				"history.delete_many":"批量删除节点",
				"history.paste":"粘贴节点",
				"history.group":"成组",
				"history.ungroup":"解组",
				"history.move_many":"批量移动节点",
				"models.title":"模型",
				"models.auto":"自动",
				"models.tab.image":"图片",
				"models.tab.3d":"3D",
				"models.loading":"加载中",
				"models.empty":"暂无模型",
				"models.recommended":"推荐",
				"style.line_width":"线宽",
				"style.line_color":"线色",
				"style.custom_color":"自定义颜色",
				"style.fill":"填充",
				"style.no_fill":"无填充",
				"style.rgb":"RGB",
				"style.confirm":"确定",
				"style.cancel":"取消"
			}
		}
	},
	"provider_webhooks":[{
		"id":"meshy-task-status",
		"provider_id":"meshy",
		"route":"/{webhookSecret}/wbh",
		"path_parameter":"webhookSecret",
		"path_secret_setting":"meshy_webhook_path_secret",
		"key_setting":"meshy_webhook_key",
		"key_headers":["x-webhook-key","x-webhook-secret","x-meshy-webhook-key"],
		"payload_job_id_fields":["id","task_id"],
		"event_type":"provider.webhook_received",
		"api":{
			"timestamp_layout":"2006-01-02T15:04:05.000Z07:00",
			"fields":{
				"error":"error","received":"received","timestamp":"timestamp",
				"status":"status","service":"service"
			},
			"values":{
				"not_found":"Not Found","unauthorized":"Unauthorized",
				"bad_request":"Bad Request","unavailable":"Service Unavailable",
				"ok":"ok","service":"Plinth WebHook Endpoint"
			},
			"statuses":{
				"ok":200,"bad_request":400,"unauthorized":401,
				"not_found":404,"unavailable":503
			}
		}
	}],
	"provider_discovery":{
		"api":{
			"query":{
				"capability":"capability","domain":"domain","projection":"projection"
			},
			"projection_values":{"extended":"extended"},
			"envelope_fields":{"providers":"providers","default_model":"defaultModel"},
			"provider_fields":{
				"id":"id","name":"name","capabilities":"capabilities",
				"domain":"domain","features":"features",
				"parameter_schemas":"paramSchemas","input_schemas":"inputSchemas",
				"constraints":"constraints","recommended":"recommended",
				"estimated_credits":"estimatedCredits","provider_key":"providerKey"
			},
			"parameter_fields":{
				"name":"name","label":"label","type":"type","options":"options",
				"default":"default","required":"required","minimum":"min",
				"maximum":"max","step":"step","description":"description",
				"disabled_when":"disabled_when","visible_when":"visible_when",
				"forced_when":"forced_when","notices":"notices"
			},
			"option_fields":{"value":"value","label":"label","group":"group"},
			"condition_fields":{
				"parameter":"parameter","operator":"operator","value":"value"
			},
			"notice_fields":{
				"when":"when","message_key":"message_key","tone":"tone"
			},
			"forced_value_fields":{
				"when":"when","value":"value"
			},
			"input_fields":{
				"name":"name","label":"label","type":"type",
				"media_types":"mediaTypes","extensions":"extensions",
				"max_bytes":"maxBytes","min_items":"minItems","max_items":"maxItems"
			},
			"constraint_fields":{
				"prompt_max_length":"promptMaxLength",
				"negative_prompt_max_length":"negativePromptMaxLength",
				"reference_image_min":"referenceImageMin",
				"reference_image_max":"referenceImageMax",
				"reference_image_formats":"referenceImageFormats",
				"reference_image_max_size":"referenceImageMaxSize",
				"polycount_min":"polycountMin","polycount_max":"polycountMax",
				"output_sizes":"outputSizes"
			}
		},
		"legacy_models":[{"model_id":"m","provider_key":"provider-x"}]
	},
	"task_history":{
		"api":{
			"query":{
				"page":"page","page_size":"pageSize","recent":"recent",
				"type":"type","search":"search","status":"status",
				"hide_failures":"hideFailures"
			},
			"fields":{
				"data":"data","total":"total","page":"page",
				"page_size":"pageSize","id":"id","task_type":"taskType",
				"status":"status","progress":"progress",
				"thumbnail_url":"thumbnailUrl","created_at":"createdAt",
				"prompt":"prompt"
			}
		},
		"assets":{"preview_roles":["thumbnail","image"],"attachments":{"title":"附件","role_labels":{"report":"报告","print_report":"打印报告","analysis_report":"分析报告","repair_report":"修复报告","model":"模型","image":"图片","thumbnail":"缩略图"},"fallback_role":"附件","format":"格式","media_type":"媒体类型","size":"大小","unknown_size":"未提供大小","download":"下载附件","no_preview":"此产物不提供预览，可下载查看。"},"rig_check_report":{"media_type":"application/vnd.art-ai.rig-check-report+json","format":"art-ai.rig-check-report.v1","max_bytes":32768,"title":"绑骨检查报告","loading":"正在读取绑骨检查报告…","load_failed":"报告读取失败，请重试或下载查看。","retry":"重试","unsupported":"此报告版本或内容暂不支持结构化查看，请下载原文件。","limitation":"检查结论只反映模型的绑骨兼容性，不表示已绑定骨骼，也不会自动执行后续操作。","field_labels":{"riggable":"是否适合绑骨","rig_type":"建议骨骼类型"},"boolean_labels":{"true":"适合","false":"不适合"},"species_labels":{"biped":"双足","quadruped":"四足","hexapod":"六足","octopod":"八足","avian":"鸟类","serpentine":"蛇形","aquatic":"水生"}},"print_report":{"media_type":"application/vnd.art-ai.print-report+json","format":"art-ai.print-report.v1","max_bytes":32768,"title":"打印报告","loading":"正在读取报告…","load_failed":"报告读取失败，请重试或下载查看。","retry":"重试","unsupported":"此报告版本或内容暂不支持结构化查看，请下载原文件。","diagnostic_notice":"诊断结果描述此模型的检查情况，不代表打印认证；诊断异常不会改变任务状态。","unknown_value":"未提供","not_reported":"上游未提供详细修复报告。","kind_labels":{"analysis":"打印分析","repair":"打印修复"},"status_labels":{"healthy":"未发现问题","warning":"存在警告","error":"发现问题","unknown":"未提供诊断结论"},"details_status_labels":{"reported":"已提供修复详情","not_reported":"未提供详细报告"},"field_labels":{"status":"诊断结论","issue_count":"问题总数","error_count":"错误数","warning_count":"警告数","is_watertight":"是否封闭","volume_m3":"体积（m³）","non_manifold_edges":"非流形边","degenerate_faces":"退化面","holes":"孔洞数","printer_type":"打印类型","height_mm":"高度（mm）","longest_side_mm":"最长边（mm）","wall_thickness_mm":"壁厚（mm）","dimensions_mm":"尺寸（mm）","thin_wall_status":"薄壁检查","thin_area_ratio":"薄壁面积比例","has_thin_walls":"是否存在薄壁","evaluated_at_ms":"检查时间","details_status":"详情状态","route":"修复路径","applied":"是否应用修复","faces_before":"修复前面数","faces_after":"修复后面数","thin_area_ratio_before":"修复前薄壁面积比例","thin_area_ratio_after":"修复后薄壁面积比例"},"value_labels":{"true":"是","false":"否","fdm":"熔融沉积（FDM）","sla":"光固化（SLA）","full_color":"全彩打印","thin_wall":"薄壁修复","topology":"拓扑修复","topology_unmeasured":"拓扑修复（未提供测量）"}}},
	"values":{"all_capability":"all","enabled":"1"}
	},
	"task_stream":{
		"api":{
			"fields":{
				"task_id":"id","type":"type","status":"status",
				"progress":"progress","created_at":"created_at",
				"updated_at":"updated_at","task_error":"task_error",
				"message":"message","status_code":"status_code","error":"error"
			},
			"path_parameters":{"type":"type","task_id":"taskID"}
		},
		"aliases":[{"external_type":"text-to-image","capability":"text-to-image"}],
		"events":{"message":"message","error":"error"},
		"timing":{"poll_interval_milliseconds":10},
		"presentation":{
			"unsupported_type":"不支持的任务类型: {type}",
			"stream_failed":"任务状态流读取失败",
			"task_not_found":"任务不存在"
		}
	},
	"administration":{
		"navigation":[
			{"key":"dashboard","href":"/admin","label":"管理","icon":"chart","match":"exact"}
		],
		"users":{
			"api":{
				"fields":{
					"data":"data","success":"success","error":"error","id":"id",
					"username":"username","password":"password","nickname":"nickname",
					"name":"name","email":"email","is_admin":"isAdmin",
					"is_active":"isActive","created_at":"createdAt",
					"last_login":"lastLogin","open_id":"openId"
				},
				"path_parameters":{"user_id":"userID"},
				"identity":{
					"email_required":true,"email_must_contain_at":true
				}
			},
			"permission":"users.manage",
			"guards":{"minimum_active_administrators":1},
			"limits":{
				"username_minimum_characters":2,
				"password_minimum_characters":6
			},
			"defaults":{"id_prefix":"user_","active":true},
			"audit":{
				"read_action":"access_users",
				"create_action":"admin_create_user",
				"update_action":"admin_update_user",
				"deactivate_action":"admin_deactivate_user",
				"delete_action":"admin_delete_user",
				"resource_type":"user",
				"detail_fields":{
					"username":"username","is_admin":"isAdmin","is_active":"isActive"
				}
			},
			"messages":{
				"user_not_found":"用户不存在","self_deactivate":"不能停用自己",
				"self_delete":"不能删除自己","duplicate_username":"用户名已存在",
				"invalid_username":"用户名至少 {minimum} 个字符",
				"invalid_password":"密码至少 {minimum} 个字符",
				"invalid_email":"邮箱不能为空或格式无效",
				"last_administrator":"必须保留至少一个可用的管理员账号",
				"invalid_request":"请求无效","internal_error":"内部错误"
			},
			"presentation":{
				"date_locale":"zh-CN","loading_rows":3,
				"date_time_options":{
					"month":"2-digit","day":"2-digit",
					"hour":"2-digit","minute":"2-digit"
				},
				"empty_placeholder":"—",
				"text":{
					"forbidden_title":"无权访问","forbidden_description":"仅管理员可访问",
					"count_summary":"共 {total} 个用户（活跃 {active}）",
					"add_user":"新增用户","list_title":"用户列表","empty":"暂无用户",
					"column_username":"用户名","column_name":"姓名","column_email":"邮箱",
					"column_role":"角色","column_status":"状态",
					"column_created_at":"创建时间","column_last_login":"最近登录",
					"column_actions":"操作","role_admin":"管理员","role_user":"普通用户",
					"status_active":"活跃","status_inactive":"停用","edit":"编辑",
					"activate":"启用","deactivate":"停用",
					"self_deactivate":"不能停用自己","delete_user":"删除用户",
					"self_delete":"不能删除自己","create_title":"新增用户",
					"edit_title":"编辑用户","create_description":"创建用户",
					"edit_description":"修改用户","username_label":"用户名",
					"username_placeholder":"至少 {minimum} 个字符",
					"password_label":"密码","new_password_label":"新密码",
					"password_placeholder":"至少 {minimum} 个字符",
					"new_password_placeholder":"留空不修改，至少 {minimum} 位",
					"name_label":"姓名","name_placeholder":"显示名称",
					"email_label":"邮箱","email_placeholder":"可选",
					"admin_label":"管理员权限","admin_description":"可管理用户",
					"cancel":"取消","save":"保存","saving":"保存中",
					"delete_title":"确认删除","delete_description":"确定删除吗",
					"delete_confirm":"确认删除","deleting":"删除中",
					"password_too_short":"密码至少 {minimum} 位",
					"update_failed":"更新失败","create_failed":"创建失败",
					"operation_failed":"操作失败","delete_failed":"删除失败",
					"updated":"已更新","created":"已创建",
					"active_changed":"已{action}用户 {user}",
					"deleted":"已删除用户 {user}"
				}
			}
		},
		"audit_logs":{
			"api":{
				"fields":{
					"data":"data","error":"error","total":"total","page":"page",
					"page_size":"pageSize","id":"id","action":"action",
					"user_id":"userId","user_name":"userName",
					"resource_type":"resourceType","resource_id":"resourceId",
					"detail":"detail","ip_address":"ipAddress",
					"user_agent":"userAgent","created_at":"createdAt"
				},
				"query":{
					"page":"page","page_size":"pageSize","user_id":"userId",
					"action":"action","from":"from","to":"to"
				}
			},
			"audit":{"read_action":"access_audit_logs","resource_type":"audit_log"},
			"permission":"audit.read",
			"defaults":{"id_prefix":"audit_"},
			"request_headers":{
				"forwarded_for":"X-Forwarded-For",
				"real_ip":"X-Real-IP",
				"user_agent":"User-Agent"
			},
			"write_policy":{
				"maximum_attempts":2,"retry_delay_milliseconds":1000
			},
			"limits":{
				"default_page":1,"default_page_size":50,"maximum_page_size":100
			},
			"messages":{"internal_error":"内部错误"},
			"presentation":{
				"default_recent_days":3,"loading_rows":5,
				"user_id_preview_characters":8,
				"resource_id_preview_characters":12,
				"preview_suffix":"…","empty_placeholder":"—",
				"date_locale":"zh-CN",
				"date_time_options":{
					"year":"numeric","month":"2-digit","day":"2-digit",
					"hour":"2-digit","minute":"2-digit","second":"2-digit"
				},
				"action_badge_classes":{"login":"login"},
				"action_prefix_badge_classes":{"admin_":"admin"},
				"default_badge_class":"default",
				"text":{
					"forbidden_title":"无权访问",
					"forbidden_description":"仅管理员可访问",
					"filter_action":"操作类型","all_actions":"所有操作",
					"filter_user":"用户","all_users":"全部用户",
					"unnamed_user":"未命名",
					"user_option_label":"{name} — {identifier}",
					"filter_from":"开始日期","filter_to":"结束日期",
					"filter_submit":"查询","title":"操作日志",
					"total_summary":"共 {total} 条","empty":"暂无审计日志",
					"column_time":"时间","column_user":"用户",
					"column_action":"操作","column_resource_type":"资源类型",
					"column_resource_id":"资源ID",
					"column_ip_address":"IP地址","column_actions":"操作",
					"detail_button":"详情","pagination_previous":"上一页",
					"pagination_summary":"第 {page} / {totalPages} 页",
					"pagination_next":"下一页","detail_title":"操作日志详情",
					"detail_id":"日志 ID","detail_created_at":"操作时间",
					"detail_user_name":"用户名","detail_user_id":"用户 ID",
					"detail_action":"操作类型",
					"detail_resource_type":"资源类型",
					"detail_resource_id":"资源 ID",
					"detail_ip_address":"IP 地址",
					"detail_user_agent":"User-Agent",
					"detail_payload":"操作详情"
				}
			}
		},
		"generations":{
			"api":{
				"fields":{
					"data":"data","error":"error","total":"total","page":"page",
					"page_size":"pageSize","id":"id","user_id":"userId",
					"user_name":"userName","user_email":"userEmail",
					"task_type":"taskType","task_id":"taskId",
					"estimated_credits":"estimatedCredits",
					"task_status":"taskStatus","params":"params",
					"result_urls":"resultUrls","thumbnail_url":"thumbnailUrl",
					"created_at":"createdAt","finished_at":"finishedAt",
					"ledger":"ledger","task_cache":"taskCache","status":"status",
					"provider_id":"providerId","cache_data":"data",
					"cached_at":"cachedAt","updated_at":"updatedAt",
					"redirect":"redirect"
				},
				"query":{
					"page":"page","page_size":"pageSize","user_id":"userId",
					"task_type":"taskType","status":"status",
					"from":"from","to":"to"
				},
				"path_parameters":{"task_id":"taskID"}
			},
			"permission":"generations.manage",
			"limits":{
				"default_page":1,"default_page_size":50,
				"maximum_page_size":100,"default_recent_days":3
			},
			"assets":{
				"thumbnail_role":"thumbnail",
				"download_filename_template":"{task_id}_assets.zip",
				"asset_filename_template":"{role}{variant_segment}_{ordinal}{extension}",
				"variant_separator":"_","duplicate_separator":"_",
				"ordinal_base":1,"archive_media_type":"application/zip",
				"default_media_type":"application/octet-stream",
				"unknown_extension":".bin",
				"maximum_package_bytes":1073741824,
				"media_type_extensions":{
					"image/png":".png","image/jpeg":".jpg",
					"application/zip":".zip",
					"application/octet-stream":".bin"
				},
				"download_excluded_roles":["thumbnail","*"],
				"legacy_cache_root_setting":"legacy_asset_cache_root",
				"legacy_cache_prefix_characters":2,
				"legacy_cache_metadata_suffix":".meta.json",
				"legacy_cache_content_type_field":"contentType",
				"remote_download_timeout_milliseconds":300000,
				"maximum_remote_redirects":5,
				"remote_thumbnail_url_query":"url",
				"remote_thumbnail_task_id_query":"taskId",
				"preview_image_extensions":["png","jpg","jpeg","webp","gif"]
			},
			"audit":{
				"read_action":"access_generations",
				"download_action":"access_asset","resource_type":"admin_task"
			},
			"messages":{
				"task_not_found":"任务不存在","cache_not_found":"缓存不存在",
				"no_downloadable_assets":"无可下载文件",
				"all_downloads_failed":"所有下载失败",
				"invalid_request":"请求无效","internal_error":"内部错误",
				"download_failed":"下载失败",
				"unknown_download_response":"未知下载响应"
			},
			"presentation":{
				"loading_rows":5,"user_id_preview_characters":8,"task_id_preview_characters":12,
				"preview_suffix":"…","empty_placeholder":"—",
				"date_locale":"zh-CN",
				"date_time_options":{
					"year":"numeric","month":"2-digit","day":"2-digit",
					"hour":"2-digit","minute":"2-digit","second":"2-digit"
				},
				"text":{
					"forbidden_title":"无权访问",
					"forbidden_description":"仅管理员可访问",
					"filter_user":"用户","all_users":"全部用户",
					"unnamed_user":"未命名",
					"user_option_label":"{name} — {identifier}",
					"filter_task_type":"任务类型",
					"all_task_types":"全部","legacy_animation":"旧动画",
					"filter_status":"状态","all_statuses":"全部",
					"filter_from":"开始日期","filter_to":"结束日期",
					"filter_submit":"查询","title":"生成记录",
					"total_summary":"共 {total} 条","empty":"暂无生成记录",
					"column_preview":"预览","column_time":"时间",
					"column_user":"用户","column_task_type":"任务类型",
					"column_model_params":"模型/参数","column_task_id":"任务ID",
					"column_status":"状态",
					"column_estimated_credits":"预估消耗",
					"column_actions":"操作",
					"estimated_credits":"{credits} 点",
					"detail_button":"详情","open_task":"打开任务",
					"download":"下载","downloading":"下载中",
					"download_failed":"下载失败",
					"package_download_tooltip":"打包下载全部文件",
					"package":"打包","download_all_package":"全部打包下载",
					"download_all":"下载全部",
					"result_count":"生成结果 ({count})",
					"detail_cache_full":"TaskCache 完整数据",
					"pagination_previous":"上一页",
					"pagination_summary":"第 {page} / {totalPages} 页",
					"pagination_next":"下一页","detail_title":"生成记录详情",
					"detail_loading":"加载中","detail_empty":"未找到详情",
					"detail_basic":"基本信息","detail_user":"用户",
					"detail_task_type":"任务类型","detail_task_id":"任务 ID",
					"detail_status":"状态","detail_created_at":"创建时间",
					"detail_finished_at":"完成时间",
					"detail_estimated_credits":"预估消耗",
					"detail_params":"请求参数","detail_result_urls":"结果资产",
					"detail_no_result_urls":"暂无结果资产",
					"detail_cache":"任务缓存",
					"detail_cache_status":"缓存状态",
					"detail_cache_provider":"Provider",
					"detail_cache_data":"缓存数据"
				}
			}
		},
		"usage":{
			"api":{
				"balance_fields":{
					"current_balance":"current_balance","history":"history",
					"balance":"balance","captured_at":"captured_at"
				},
				"fields":{
					"data":"data","error":"error","user_id":"userId",
					"user_name":"userName","user_email":"userEmail",
					"total_tasks":"totalTasks",
					"succeeded_tasks":"succeededTasks",
					"failed_tasks":"failedTasks",
					"estimated_credits":"estimatedCredits",
					"last_active_at":"lastActiveAt","by_type":"byType","total":"total"
				},
				"query":{
					"days":"days","start_date":"startDate","end_date":"endDate"
				}
			},
			"balance":{
				"account_id":"meshy","history_days":7,
				"snapshot_id_prefix":"balance",
				"snapshot_write_policy":"best-effort"
			},
			"audit":{"read_action":"access_usage","resource_type":"usage_record"},
			"permission":"generations.manage",
			"defaults":{"days":7,"custom_range_days":7},
			"validation":{
				"date_pattern":"^\\d{4}-\\d{2}-\\d{2}$",
				"date_layout":"2006-01-02","timezone":"UTC","maximum_days":3660
			},
			"messages":{
				"invalid_range":"结束日期不得早于开始日期",
				"internal_error":"内部错误"
			},
			"presentation":{
				"quick_ranges":[
					{"days":7,"label":"近 7 天"},
					{"days":30,"label":"近 30 天"},
					{"days":90,"label":"近 90 天"}
				],
				"loading_rows":3,"empty_placeholder":"—",
				"approximation_prefix":"≈","range_separator":" ~ ",
				"date_separator":"至","date_locale":"zh-CN",
				"date_time_options":{
					"month":"2-digit","day":"2-digit",
					"hour":"2-digit","minute":"2-digit"
				},
				"text":{
					"forbidden_title":"无权访问",
					"forbidden_description":"仅管理员可访问",
					"balance_label":"API 当前余额",
					"usage_label":"预估消耗（{range}）",
					"task_count_label":"任务总数（{range}）",
					"disclaimer_prefix":"个人消费数据为",
					"disclaimer_emphasis":"平台内部预估值",
					"disclaimer_suffix":"仅供运营参考。",
					"time_range":"时间范围：","custom_range":"自定义",
					"query":"查询","ranking_title":"用户消费排行",
					"ranking_description":"按预估积分消耗降序排列",
					"empty":"暂无使用记录","column_user":"用户",
					"column_tasks":"任务数","column_succeeded":"成功",
					"column_failed":"失败","column_credits":"预估消耗",
					"column_last_active":"最近活动"
				}
			}
		},
		"user_assets":{
			"api":{
				"envelope_fields":{
					"error":"error","user":"user","summary":"summary",
					"uploads":"uploads","generations":"generations","downloads":"downloads"
				},
				"page_fields":{
					"data":"data","total":"total","page":"page","page_size":"pageSize"
				},
				"user_fields":{
					"id":"id","username":"username","nickname":"nickname",
					"email":"email","name":"name","is_admin":"isAdmin",
					"is_active":"isActive","created_at":"createdAt","last_login":"lastLogin"
				},
				"summary_fields":{
					"total_uploads":"totalUploads",
					"total_generations":"totalGenerations",
					"total_downloads":"totalDownloads",
					"total_storage_bytes":"totalStorageBytes"
				},
				"upload_fields":{
					"id":"id","asset_type":"assetType",
					"original_filename":"originalFilename",
					"stored_filename":"storedFilename","mime_type":"mimeType",
					"file_size":"fileSize","storage_path":"storagePath",
					"created_at":"createdAt"
				},
				"generation_fields":{
					"id":"id","task_type":"taskType","task_id":"taskId",
					"task_status":"taskStatus","result_urls":"resultUrls",
					"params":"params","estimated_credits":"estimatedCredits",
					"thumbnail_url":"thumbnailUrl","created_at":"createdAt",
					"finished_at":"finishedAt"
				},
				"download_fields":{
					"id":"id","action":"action","resource_type":"resourceType",
					"resource_id":"resourceId","detail":"detail","created_at":"createdAt"
				},
				"query":{
					"page_size":"pageSize","uploads_page":"uploadsPage",
					"generations_page":"generationsPage",
					"downloads_page":"downloadsPage","from":"from","to":"to"
				},
				"path_parameters":{"user_id":"userID"}
			},
			"audit":{"read_action":"access_user_assets","resource_type":"user_asset"},
			"permission":"generations.manage",
			"limits":{
				"default_page":1,"default_page_size":50,
				"maximum_page_size":100,"frontend_page_size":20,
				"default_recent_days":3
			},
			"filters":{
				"image_asset_type":"image","model_asset_type":"model",
				"image_media_type_prefix":"image/",
				"generated_image_resource_type":"gpt-image",
				"download_actions":[{
					"action":"access_asset","label":"访问",
					"text_class":"access-text","dot_class":"access-dot"
				}]
			},
			"messages":{
				"user_not_found":"用户不存在","internal_error":"内部错误"
			},
			"presentation":{
				"loading_rows":2,"filename_preview_characters":30,
				"task_id_preview_characters":12,
				"resource_id_preview_characters":40,
				"preview_suffix":"…","empty_placeholder":"—",
				"date_locale":"zh-CN",
				"date_time_options":{"year":"numeric"},
				"bytes_per_unit":1024,"size_decimal_places":1,
				"size_units":["B","KB","MB","GB"],
				"asset_type_labels":{"image":"图片","model":"模型"},
				"text":{
					"select_user_label":"选择用户","unnamed_user":"未命名",
					"user_option_label":"{name} — {identifier}",
					"select_user_placeholder":"选择用户",
					"search_user_placeholder":"搜索用户",
					"no_matching_users":"无匹配用户",
					"start_date":"开始日期","end_date":"结束日期",
					"filter_action":"操作","filter":"筛选",
					"unselected_title":"选择用户",
					"unselected_description":"请选择用户",
					"summary_upload":"上传","summary_generation":"生成",
					"summary_download":"下载","summary_storage":"存储 {size}",
					"tab_uploads":"上传资产","tab_generations":"生成记录",
					"tab_downloads":"下载记录","uploads_title":"上传文件",
					"uploads_empty":"暂无上传","generations_title":"生成内容",
					"generations_empty":"暂无生成",
					"downloads_title":"下载记录","downloads_empty":"暂无下载",
					"column_preview":"预览","column_time":"时间",
					"column_filename":"文件名","column_type":"类型",
					"column_size":"大小","column_task_type":"任务类型",
					"column_task_id":"任务ID","column_status":"状态",
					"column_credits":"消耗","column_result":"结果",
					"column_operation":"操作","column_action":"操作类型",
					"column_resource_type":"资源类型",
					"column_resource_id":"资源ID","view":"查看",
					"result_files":"{count} 个文件","download":"下载",
					"package":"打包","downloading":"下载中",
					"package_download_tooltip":"打包下载",
					"pagination_previous":"上一页",
					"pagination_summary":"第 {page} 页，共 {total} 条",
					"pagination_next":"下一页"
				}
			}
		},
		"backfill_status":{
			"api":{"fields":{
				"error":"error","message":"message","fixed":"fixed",
				"total":"total","sync_fixed":"syncFixed",
				"async_fixed":"asyncFixed","unchanged":"unchanged"
			}},
			"audit":{"trigger_action":"access_backfill","resource_type":"backfill_status"},
			"permission":"generations.manage",
			"statuses":{"pending":"PENDING","succeeded":"SUCCEEDED"},
			"sync_provider_ids":["openai","gemini"],
			"cache_result_fields":{
				"model_urls":"model_urls","thumbnail_url":"thumbnail_url",
				"image_urls":"image_urls","texture_urls":"texture_urls",
				"base_color":"base_color","video_url":"video_url"
			},
			"messages":{
				"no_pending":"没有待处理记录","complete":"处理完成",
				"internal_error":"内部错误"
			}
		}
	},
	"capabilities":[{
		"id":"text-to-image","domain":"image","label":"Text to Image",
		"label_cn":"文生图","order":0
	}],
	"task_states":[
		{"internal_state":"QUEUED","public_state":"PENDING","label":"等待中","color":"queued","terminal":false,"outcome":"pending","progress":{"mode":"reported"}},
		{"internal_state":"RUNNING","public_state":"IN_PROGRESS","label":"进行中","color":"running","terminal":false,"outcome":"running","progress":{"mode":"reported"}},
		{"internal_state":"MATERIALIZING","public_state":"IN_PROGRESS","label":"进行中","color":"materializing","terminal":false,"outcome":"running","progress":{"mode":"reported"}},
		{"internal_state":"SUCCEEDED","public_state":"SUCCEEDED","label":"已完成","color":"succeeded","terminal":true,"outcome":"success","progress":{"mode":"hidden"}},
		{"internal_state":"FAILED","public_state":"FAILED","label":"失败","color":"failed","terminal":true,"outcome":"failure","progress":{"mode":"hidden","show_error":true,"error_color":"error"}},
		{"internal_state":"CANCELED","public_state":"CANCELED","label":"已取消","color":"canceled","terminal":true,"outcome":"canceled","progress":{"mode":"hidden"}},
		{"internal_state":"RETRY_PENDING","public_state":"RETRY_PENDING","label":"等待重试","color":"retry","terminal":false,"outcome":"pending","progress":{"mode":"retry-countdown","fixed_value":100,"retry_badge_color":"retry-badge","track_color":"retry-track","countdown_color":"retry-countdown"}}
	],
	"audit_actions":[
		{"value":"login","label":"登录"},
		{"value":"logout","label":"登出"},
		{"value":"access_asset","label":"访问资产"},
		{"value":"admin_create_user","label":"创建用户"},
		{"value":"admin_update_user","label":"更新用户"},
		{"value":"admin_deactivate_user","label":"停用用户"},
		{"value":"admin_delete_user","label":"删除用户"}
	],
	"authentication":{
		"session":{"legacy_accept_until":"2026-10-04T00:00:00Z"},
		"cookie":{
			"name":"meshyai_session","format":"hmac-session-v2","ttl_seconds":604800,
			"http_only":true,"same_site":"lax","path":"/","secure_mode":"auto"
		},
		"api":{
			"id_field":"id",
			"username_field":"username","password_field":"password",
			"name_field":"name","email_field":"email","error_field":"error",
			"success_field":"success","user_field":"user",
			"is_admin_field":"isAdmin","mock_id_prefix":"mock_",
			"password_hash_algorithm":"bcrypt","password_hash_cost":10,
			"session_secret_missing_policy":"ephemeral-non-production",
			"login_audit_action":"login","logout_audit_action":"logout",
			"audit_resource_type":"user"
		},
		"public_paths":["/login"],
		"session_retry":{"initial_delay_ms":1000,"max_backoff_ms":30000,"max_retry_after_ms":120000},
		"permissions":[
			{"key":"tasks.read.own","label":"查看本人任务"},
			{"key":"users.manage","label":"管理用户"},
			{"key":"audit.read","label":"查看审计日志"},
			{"key":"generations.manage","label":"管理生成任务"}
		],
		"roles":[
			{"key":"user","label":"用户","permissions":["tasks.read.own"]},
			{"key":"admin","label":"管理员","permissions":["tasks.read.own","users.manage","audit.read","generations.manage"]}
		],
		"default_role":"user","admin_role":"admin","directory_errors":"allow",
		"messages":{
			"unauthenticated":"需要有效身份","forbidden":"无权执行此操作",
			"user_deactivated":"用户已停用","missing_credentials":"请输入用户名和密码",
			"invalid_credentials":"用户名或密码错误","login_failed":"登录失败","rate_limited":"尝试过于频繁",
			"network_error":"网络错误，请重试",
			"internal_error":"服务内部错误，请稍后重试",
			"mock_disabled":"Mock auth is disabled",
			"mock_user_missing":"No user info provided and DEV_MOCK_USER is not set",
			"mock_user_invalid":"DEV_MOCK_USER is not valid JSON"
		},
		"login":{
			"title":"Plinth","description":"平台","username_label":"用户名",
			"username_placeholder":"请输入用户名","password_label":"密码",
			"password_placeholder":"请输入密码","submit_label":"登录",
			"submitting_label":"登录中...","loading_label":"加载中...",
			"default_redirect":"/","redirect_query":"redirect"
		}
	}
}`

func testProductDocument(offerings string) []byte {
	// The legacy fixtures intentionally keep a small custom Application, but it
	// still needs a valid Harness contract now that Chat.Harness is required.
	// Reuse the production declarations for its prerequisite plan tool, the
	// Harness presentation and required processing capability identities. The
	// fixture's placeholder operation stays explicitly unavailable, so these
	// offering-validation fixtures do not pretend to test tool wiring.
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"version":1,`+minimalApplication+`}`), &document); err != nil {
		panic(err)
	}
	var application catalog.ApplicationSchema
	if err := json.Unmarshal(document["application"], &application); err != nil {
		panic(err)
	}
	defaults, err := catalog.LoadDefault()
	if err != nil {
		panic(err)
	}
	defaultChat := defaults.Application().Chat

	var listOfferings catalog.ChatAgentToolSchema
	for _, tool := range defaultChat.AgentTools {
		if tool.Name == "list_offerings" {
			listOfferings = tool
			break
		}
	}
	application.Chat.AgentTools = append(application.Chat.AgentTools, listOfferings)
	for _, tool := range defaultChat.Tools {
		if tool.Name == defaultChat.Harness.PrerequisiteTool {
			application.Chat.Tools = append(application.Chat.Tools, tool)
			break
		}
	}
	for _, capability := range defaults.Application().Capabilities {
		if capability.Domain == "processing" {
			// The minimal fixture needs the processing capability identity for
			// validating its plan tool, but not the default catalog's unrelated
			// legacy HTTP contract for that capability.
			capability.LegacyAPI = nil
			application.Capabilities = append(application.Capabilities, capability)
		}
	}
	// The reused plan tool asks for a service on a choice card (#321), so the
	// fixture carries the card's declarations too.
	application.Chat.Choice = defaultChat.Choice
	application.Chat.API.Fields.ChoiceAnswer = defaultChat.API.Fields.ChoiceAnswer
	application.Chat.Presentation.Messages.ChoiceAnswered = defaultChat.Presentation.Messages.ChoiceAnswered
	application.Chat.Presentation.Messages.ChoiceRejected = defaultChat.Presentation.Messages.ChoiceRejected
	harness := defaultChat.Harness
	harness.BaseTools = []string{"list_offerings"}
	harness.Operations = []catalog.HarnessOperationSchema{{
		PublicHarnessOperation: catalog.PublicHarnessOperation{
			ID: "fixture-image-generation", Label: "Fixture image generation",
			Description: "Fixture operation used while validating Offering schema.",
			Group:       "model", Capability: "text-to-image", ModelIDs: []string{"fixture-model"},
			UnavailableReason: "Fixture Harness operation is intentionally not wired.",
		},
	}}
	harness.DefaultEnabledOperations = []string{"fixture-image-generation"}
	application.Chat.Harness = harness
	// Image limits for references (#276) are required runtime declarations.
	defaultInputs := defaults.Application().Runtime.InputAssets
	application.Runtime.InputAssets.GeneratedReferenceMaxBytes = defaultInputs.GeneratedReferenceMaxBytes
	application.Runtime.InputAssets.Messages = defaultInputs.Messages
	application.Runtime.InputAssets.ImageFitting = defaultInputs.ImageFitting

	encodedApplication, err := json.Marshal(application)
	if err != nil {
		panic(err)
	}
	document["application"] = encodedApplication
	document["offerings"] = json.RawMessage(offerings)
	encodedDocument, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return encodedDocument
}

func TestDefaultCatalogCoversCurrentProviderInventory(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offerings := taskCatalog.List("")
	// Four narrow conversions (#264 adds the Task-output glTF package), five
	// qualified Meshy extensions and the qualified Tripo Rig Check report plus
	// complete Rig delivery are enabled; #186 route B (2026-10-20) also enables
	// Rig v3, Retarget walk and Texture, and #191 route B enables Tripo
	// image-to-multiview and edit-multiview, all pending public acceptance on
	// the dev-test environment. #188 route B enables Rodin Gen-2.5
	// text/image-to-3D; #187 enables Bang (Rodin source and local model) and
	// Hyper3D Texture after their isolated public-Task acceptance on
	// 2026-10-21. Legacy Tripo six-format
	// conversion and unqualified Repair stay disabled. Splat and Meshy
	// multiview have independent identities. #187 enables Bang and Texture.
	// #189 enables the first Creative Lab batch (four prototype/build pairs and
	// the single-stage collapsible fidget) after isolated public acceptance; the
	// second batch (keychain, lampshade, pixel fidget and keycap pairs) followed
	// the same acceptance. #185 enables the six Tripo geometry Offerings that
	// passed qualification (Decimate triangle, Segment, Complete, Quick Cap,
	// Smart Segmentation image/model); Decimate quad stays disabled. #329 adds
	// Meshy T2.5 (Smart Topology) text and image to 3D. #330 adds Meshy Convert.
	// #100: the 17 Meshy Creative Lab Offerings stay defined but are not offered
	// (enabled=false), so only the other 52 are listed.
	if len(offerings) != 52 {
		t.Fatalf("offering count = %d", len(offerings))
	}
	for _, id := range []generation.OfferingID{
		"meshy/nano-banana-pro/text-to-image",
		"meshy/nano-banana-2/image-to-image",

		"openai/gpt-image-2/text-to-image",
		"gemini/gemini-3-pro-image/image-to-image",
		"jimeng/seedream-4-5/text-to-image",
		"jimeng/seedream-4-5/image-to-image",
		"meshy/meshy-7/text-to-3d",
		"meshy/meshy-7/image-to-3d",
		"tripo/tripo-v3.1-full/image-to-3d",
		"meshy/processing/animation",
		"tripo/processing/rigging",
		"tripo/processing/rigging-v3",
		"tripo/processing/animation",
		"tripo/processing/texture",
		"matting/default/background-removal",
		"tripo/tripo-v3.1/format-conversion",
		"local/static-glb/to-gltf",
		"local/static-gltf/to-glb",
		"local/generated-glb/to-gltf",
		"tripo/format-conversion/uploaded-glb-to-stl",
		"hyper3d/rodin-gen-2.5/text-to-3d",
		"hyper3d/rodin-gen-2.5/image-to-3d",
		"tripo/geometry/decimate-triangle",
		"tripo/geometry/decimate-quad",
		"tripo/geometry/segment",
		"tripo/geometry/complete",
		"tripo/geometry/quick-cap",
		"tripo/geometry/smartsegment-image",
		"tripo/geometry/smartsegment-model",
		"meshy/creative-lab/fridge-magnet/prototype",
		"meshy/creative-lab/fridge-magnet/build",
		"meshy/creative-lab/figure/prototype",
		"meshy/creative-lab/figure/build",
		"meshy/creative-lab/vinyl-figure/prototype",
		"meshy/creative-lab/vinyl-figure/build",
		"meshy/creative-lab/brick-figure/prototype",
		"meshy/creative-lab/brick-figure/build",
		"meshy/creative-lab/fidget-collapsible/generate",
		"meshy/creative-lab/keychain/prototype",
		"meshy/creative-lab/keychain/build",
		"meshy/creative-lab/lamp/prototype",
		"meshy/creative-lab/lamp/build",
		"meshy/creative-lab/fidget-pixel/prototype",
		"meshy/creative-lab/fidget-pixel/build",
		"meshy/creative-lab/keycap/prototype",
		"meshy/creative-lab/keycap/build",
	} {
		if _, exists := taskCatalog.Get(id); !exists {
			t.Errorf("missing offering %q", id)
		}
	}
	conversion, _ := taskCatalog.Get("tripo/tripo-v3.1/format-conversion")
	if conversion.Enabled {
		t.Error("legacy Tripo six-format conversion must stay disabled")
	}
	for _, offering := range offerings {
		if offering.ID == conversion.ID {
			t.Errorf("disabled offering %q listed", offering.ID)
		}
	}
	if _, _, err := taskCatalog.Normalize(generation.FormatConversion, conversion.ID,
		json.RawMessage(`{"source_task":"task-source-1","format":"FBX"}`)); err == nil {
		t.Error("disabled format conversion accepted a submission")
	}
}

func TestDefaultCatalogOwnsCapabilityRoutesLabelsAndRuntimePresentation(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	if len(application.Capabilities) != 25 {
		t.Fatalf("capability count = %d", len(application.Capabilities))
	}
	legacyPaths := map[string]generation.Capability{}
	for _, capability := range application.Capabilities {
		if capability.Label == "" || capability.LabelCN == "" {
			t.Fatalf("capability presentation = %#v", capability)
		}
		if capability.LegacyAPI != nil {
			legacyPaths[capability.LegacyAPI.Path] = capability.ID
		}
	}
	if len(legacyPaths) != 10 ||
		legacyPaths["/api/meshy/text-to-image"] != generation.TextToImage ||
		legacyPaths["/api/meshy/text-to-3d"] != generation.TextTo3D ||
		legacyPaths["/api/meshy/image-to-image"] != generation.ImageToImage ||
		legacyPaths["/api/meshy/image-to-3d"] != generation.ImageTo3D ||
		legacyPaths["/api/meshy/animations"] != generation.Animation ||
		legacyPaths["/api/meshy/remesh"] != generation.Remesh ||
		legacyPaths["/api/meshy/retexture"] != generation.Retexture ||
		legacyPaths["/api/meshy/rigging"] != generation.Rigging ||
		legacyPaths["/api/meshy/background-removal"] !=
			generation.BackgroundRemove {
		t.Fatalf("legacy paths = %#v", legacyPaths)
	}
	// 能力不再声明指向已下线页面的详情导航键：任务详情由工作区承载，
	// 导航真源只剩 content.navigation 里真实可达的条目。
	navigationKeys := map[string]struct{}{}
	for _, item := range application.Content.Navigation {
		navigationKeys[item.Key] = struct{}{}
	}
	if len(navigationKeys) != len(application.Content.Navigation) {
		t.Fatalf("navigation keys 重复: %#v", application.Content.Navigation)
	}
	if _, exists := navigationKeys["workspace"]; !exists {
		t.Fatalf("navigation 缺少工作台入口: %#v", application.Content.Navigation)
	}
	routes := application.Runtime.HTTP.LegacyRoutes
	if routes.Health != "/api/health" ||
		routes.Providers != "/api/providers" ||
		routes.Tasks != "/api/tasks" ||
		routes.Version != "/api/version" ||
		routes.AuthLogin != "/api/auth/login" ||
		routes.AuthMock != "/api/auth/mock" ||
		routes.AuthSession != "/api/auth/session" ||
		routes.CacheStatus != "/api/cache-status" ||
		routes.GeneratedImage != "/api/gpt-images/{filename}" {
		t.Fatalf("legacy HTTP routes = %#v", routes)
	}
	proxy := application.Runtime.HTTP.CandidateProxy
	if proxy.TaskPrefix != "/api/go" ||
		proxy.CorePrefix != "/api/v1" {
		t.Fatalf("candidate proxy = %#v", proxy)
	}
	expectedProxyRoutes := map[string]bool{
		"health": true, "providers": true, "tasks": true,
		"version": true, "asset_upload": true, "asset_item": true,
		"auth_login": true, "auth_mock": true, "auth_session": true,
		"cache_status": true, "generated_image": true,
		"admin_usage":           true,
		"admin_balance_summary": true,
	}
	for _, key := range proxy.LegacyRouteKeys {
		delete(expectedProxyRoutes, key)
	}
	if len(expectedProxyRoutes) != 0 {
		t.Fatalf("candidate proxy missing routes = %#v", expectedProxyRoutes)
	}
}

func TestDefaultCatalogOwnsCacheStatusRuntimeContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cache := taskCatalog.Application().Runtime.Cache
	if cache.MillisecondsPerSecond != 1000 ||
		cache.BytesPerMegabyte != 1_000_000 ||
		cache.SizeDecimalPlaces != 1 ||
		cache.Fields.ActiveDownloads != "activeDownloads" ||
		cache.Fields.ActiveItems != "activeItems" ||
		cache.Fields.Filename != "filename" ||
		cache.Fields.Elapsed != "elapsed" ||
		cache.Fields.TaskID != "taskId" ||
		cache.Fields.TaskType != "taskType" ||
		cache.Fields.ProviderID != "providerId" ||
		cache.Fields.AssetKind != "assetKind" ||
		cache.Fields.TotalCachedFiles != "totalCachedFiles" ||
		cache.Fields.TotalCacheSizeMB != "totalCacheSizeMB" {
		t.Fatalf("cache runtime = %#v", cache)
	}
}

func TestDefaultCatalogOwnsLegacyGeneratedImageContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	contract := application.Runtime.AssetDelivery.LegacyGeneratedImage
	if application.Runtime.HTTP.LegacyRoutes.GeneratedImage !=
		"/api/gpt-images/{filename}" ||
		contract.FilenameSeparator != "_" ||
		contract.OrdinalBase != 0 ||
		len(contract.AssetRoles) != 1 ||
		contract.AssetRoles[0] != "image" ||
		contract.ExtensionMediaTypes["jpg"] != "image/jpeg" ||
		contract.ExtensionMediaTypes["jpeg"] != "image/jpeg" ||
		contract.ExtensionMediaTypes["webp"] != "image/webp" ||
		contract.DefaultMediaType != "image/png" ||
		contract.CacheControl != "private, no-store" ||
		contract.NotFoundMessageKey != "error.legacy_generated_image_not_found" ||
		contract.AuditAction != "access_asset" ||
		contract.AuditResourceType != "gpt-image" {
		t.Fatalf("legacy generated image = %#v", contract)
	}

	application.Runtime.AssetDelivery.LegacyGeneratedImage.
		FilenameSeparator = ""
	if err := catalog.ValidateApplicationSchema(application); err == nil {
		t.Fatal("缺少文件名分隔符的 Product Schema 被接受")
	}
}

func TestDefaultCatalogOwnsAuthenticationAPIContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	api := schema.Authentication.API
	if api.UsernameField != "username" ||
		api.PasswordField != "password" ||
		api.NameField != "name" ||
		api.EmailField != "email" ||
		api.ErrorField != "error" ||
		api.SuccessField != "success" ||
		api.UserField != "user" ||
		api.IsAdminField != "isAdmin" ||
		api.MockIDPrefix != "mock_" ||
		api.PasswordHashAlgorithm != "bcrypt" ||
		api.PasswordHashCost != 10 ||
		api.SessionSecretMissingPolicy != "ephemeral-non-production" ||
		api.LoginAuditAction != "login" ||
		api.LogoutAuditAction != "logout" ||
		api.AuditResourceType != "user" {
		t.Fatalf("authentication API = %#v", api)
	}
	messages := schema.Authentication.Messages
	if messages.InternalError != "服务内部错误，请稍后重试" ||
		messages.MockDisabled != "Mock auth is disabled" ||
		messages.MockUserMissing !=
			"No user info provided and DEV_MOCK_USER is not set" ||
		messages.MockUserInvalid != "DEV_MOCK_USER is not valid JSON" {
		t.Fatalf("authentication messages = %#v", messages)
	}

	api.UsernameField = ""
	schema.Authentication.API = api
	if err := catalog.ValidateApplicationSchema(schema); err == nil {
		t.Fatal("缺少认证请求字段名的 Product Schema 被接受")
	}
}

func TestDefaultCatalogOwnsRuntimeConfigurationBindings(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	settings := schema.Runtime.Configuration.Settings
	for _, name := range []string{
		"application_environment",
		"address",
		"database_path",
		"storage_root",
		"legacy_generated_images_root",
		"catalog_file",
		"provider_mode",
		"legacy_backend_startup_enabled",
		"openai_base_url",
		"gemini_base_url",
		"meshy_base_url",
		"tripo_base_url",
		"matting_base_url",
		"auth_mode",
		"auth_tokens_json",
		"session_secret",
		"session_cookie_name",
		"frontend_public_app_url",
		"dev_mock_auth",
		"dev_mock_user_json",
		"admin_allowlist_emails",
		"admin_username",
		"admin_password",
		"admin_display_name",
		"admin_email",
	} {
		if _, exists := settings[name]; !exists {
			t.Errorf("runtime setting %q missing", name)
		}
	}
	if !settings["openai_api_key"].Secret ||
		!settings["auth_tokens_json"].Secret ||
		!settings["session_secret"].Secret ||
		!settings["admin_password"].Secret {
		t.Fatal("credential settings must be marked secret")
	}
	if settings["session_cookie_name"].Secret ||
		settings["session_cookie_name"].Default != "" ||
		len(settings["session_cookie_name"].Environment) != 1 ||
		settings["session_cookie_name"].Environment[0].Name != "SESSION_COOKIE_NAME" {
		t.Fatalf("session_cookie_name setting = %#v", settings["session_cookie_name"])
	}
	geminiBaseURL := settings["gemini_base_url"]
	if len(geminiBaseURL.Environment) != 1 || geminiBaseURL.Environment[0].Name != "GEMINI_BASE_URL" {
		t.Fatalf("Gemini base URL sources = %#v; LLM_BASE_URL belongs to the Agent only", geminiBaseURL.Environment)
	}
	if geminiBaseURL.Default != "" {
		t.Fatalf("Gemini base URL default = %q; want none", geminiBaseURL.Default)
	}
}

func TestDefaultCatalogOwnsLegacyInputAssetContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	runtime := application.Runtime.InputAssets
	if runtime.UploadFieldName != "file" ||
		runtime.UploadMaxBytes != 50<<20 ||
		runtime.DefaultMediaType != "application/octet-stream" ||
		runtime.DefaultAssetType != "image" ||
		runtime.StoredContentPolicy != "preserve-source" ||
		runtime.JSONFormatParameter != "format" ||
		runtime.JSONFormatValue != "json" ||
		runtime.BinaryCacheControl != "private, no-store" {
		t.Fatalf("input asset runtime = %#v", runtime)
	}
	if len(runtime.AllowedUploadMediaTypes) != 6 ||
		len(runtime.InlineMediaTypes) != 4 {
		t.Fatalf("input asset media type policy = %#v / %#v",
			runtime.AllowedUploadMediaTypes, runtime.InlineMediaTypes)
	}
	if len(runtime.TypeRules) != 2 ||
		runtime.TypeRules[0].AssetType != "image" ||
		runtime.TypeRules[1].AssetType != "model" {
		t.Fatalf("input asset type rules = %#v", runtime.TypeRules)
	}
	routes := application.Runtime.HTTP.LegacyRoutes
	if routes.AssetUpload != "/api/assets/upload" ||
		routes.AssetItem != "/api/assets/{assetID}" {
		t.Fatalf("asset routes = %#v", routes)
	}
}

func TestDefaultCatalogOwnsTaskDeletionPolicy(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	policy := taskCatalog.Application().Runtime.TaskDeletion
	if policy.LocalRecordPolicy != "tombstone" ||
		policy.UpstreamPolicy != "when-supported" ||
		policy.UpstreamFailurePolicy != "abort" ||
		policy.GeneratedAssetPolicy != "retain" ||
		policy.AuditEventType != "task.deleted" {
		t.Fatalf("task deletion policy = %#v", policy)
	}
}

func TestDefaultCatalogOwnsLegacyAnimationTaskAlias(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for _, capability := range application.Capabilities {
		if capability.ID != generation.Animation {
			continue
		}
		if capability.LegacyAPI == nil ||
			capability.LegacyAPI.Path != "/api/meshy/animations" ||
			len(capability.LegacyAPI.TaskAliases) != 1 ||
			capability.LegacyAPI.ResultField != "result" ||
			len(capability.LegacyAPI.ResultAssets) != 2 {
			t.Fatalf("legacy animation contract = %#v", capability.LegacyAPI)
		}
		alias := capability.LegacyAPI.TaskAliases[0]
		if alias.Name != "rig_task_id" ||
			alias.TargetParameter != "source_task" ||
			!alias.Restore {
			t.Fatalf("animation task alias = %#v", alias)
		}
		glb := capability.LegacyAPI.ResultAssets[0]
		if glb.Name != "animation_glb_url" ||
			glb.Variant != "animation_glb_url" ||
			glb.Role != "model" ||
			len(glb.MediaTypes) != 1 ||
			glb.MediaTypes[0] != "model/gltf-binary" {
			t.Fatalf("animation glb projection = %#v", glb)
		}
		return
	}
	t.Fatal("animation capability is missing")
}

func TestDefaultCatalogOwnsLegacyRiggingSourcesAndProjection(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for _, capability := range application.Capabilities {
		if capability.ID != generation.Rigging {
			continue
		}
		if capability.LegacyAPI == nil ||
			capability.LegacyAPI.Path != "/api/meshy/rigging" ||
			capability.LegacyAPI.ResultField != "result" ||
			len(capability.LegacyAPI.ResultAssets) != 2 {
			t.Fatalf("legacy rigging contract = %#v", capability.LegacyAPI)
		}
		break
	}

	for name, input := range map[string]json.RawMessage{
		"task":  json.RawMessage(`{"source_task":"task-model"}`),
		"url":   json.RawMessage(`{"source_url":"https://assets.test/model.glb"}`),
		"asset": json.RawMessage(`{"source_asset":"asset-model"}`),
	} {
		_, normalized, normalizeErr := taskCatalog.Normalize(
			generation.Rigging,
			"meshy/processing/rigging",
			input,
		)
		if normalizeErr != nil {
			t.Errorf("%s source: %v", name, normalizeErr)
			continue
		}
		if len(normalized) == 0 {
			t.Errorf("%s source normalized to empty input", name)
		}
	}
	for name, input := range map[string]json.RawMessage{
		"missing":  json.RawMessage(`{}`),
		"multiple": json.RawMessage(`{"source_task":"task-model","source_url":"https://assets.test/model.glb"}`),
	} {
		if _, _, normalizeErr := taskCatalog.Normalize(
			generation.Rigging,
			"meshy/processing/rigging",
			input,
		); normalizeErr == nil {
			t.Errorf("%s sources unexpectedly accepted", name)
		}
	}
}

func TestDefaultCatalogOwnsLegacyRemeshSourcesAndMultipartProtocol(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	var legacyAPI *catalog.LegacyTaskAPISchema
	for index := range application.Capabilities {
		if application.Capabilities[index].ID == generation.Remesh {
			legacyAPI = application.Capabilities[index].LegacyAPI
			break
		}
	}
	if legacyAPI == nil ||
		legacyAPI.Path != "/api/meshy/remesh" ||
		len(legacyAPI.TaskAliases) != 1 ||
		legacyAPI.Multipart == nil {
		t.Fatalf("legacy remesh contract = %#v", legacyAPI)
	}
	multipartProtocol := legacyAPI.Multipart
	if multipartProtocol.FileField != "model_file" ||
		multipartProtocol.ParamsField != "params" ||
		multipartProtocol.TargetParameter != "source_asset" ||
		multipartProtocol.MissingFileMessageKey == "" ||
		multipartProtocol.InvalidExtensionMessageKey == "" ||
		multipartProtocol.InvalidParamsMessageKey == "" {
		t.Fatalf("remesh multipart protocol = %#v", multipartProtocol)
	}

	offering, exists := taskCatalog.Get("meshy/processing/remesh")
	if !exists {
		t.Fatal("remesh offering is missing")
	}
	sourceParameters := map[string]generation.Parameter{}
	for _, parameter := range offering.Parameters {
		if parameter.Name == "source_task" ||
			parameter.Name == "source_url" ||
			parameter.Name == "source_asset" {
			sourceParameters[parameter.Name] = parameter
		}
	}
	if len(sourceParameters) != 3 ||
		sourceParameters["source_task"].WireName != "input_task_id" ||
		sourceParameters["source_url"].WireName != "model_url" ||
		sourceParameters["source_asset"].WireName != "model_url" ||
		len(sourceParameters["source_asset"].Extensions) != 5 ||
		sourceParameters["source_asset"].MaxBytes != 70*1024*1024 {
		t.Fatalf("remesh source schema = %#v", sourceParameters)
	}

	for name, input := range map[string]json.RawMessage{
		"task":  json.RawMessage(`{"source_task":"task-model"}`),
		"url":   json.RawMessage(`{"source_url":"https://assets.test/model.glb"}`),
		"asset": json.RawMessage(`{"source_asset":"asset-model"}`),
	} {
		if _, _, normalizeErr := taskCatalog.Normalize(
			generation.Remesh,
			"meshy/processing/remesh",
			input,
		); normalizeErr != nil {
			t.Errorf("%s source: %v", name, normalizeErr)
		}
	}
	for name, input := range map[string]json.RawMessage{
		"missing":  json.RawMessage(`{}`),
		"multiple": json.RawMessage(`{"source_task":"task-model","source_url":"https://assets.test/model.glb"}`),
	} {
		if _, _, normalizeErr := taskCatalog.Normalize(
			generation.Remesh,
			"meshy/processing/remesh",
			input,
		); normalizeErr == nil {
			t.Errorf("%s sources unexpectedly accepted", name)
		}
	}
}

func TestDefaultCatalogOwnsLegacyRetextureInputsOutputsAndMultipartProtocol(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var legacyAPI *catalog.LegacyTaskAPISchema
	application := taskCatalog.Application()
	for index := range application.Capabilities {
		capability := &application.Capabilities[index]
		if capability.ID == generation.Retexture {
			legacyAPI = capability.LegacyAPI
			break
		}
	}
	if legacyAPI == nil ||
		legacyAPI.Path != "/api/meshy/retexture" ||
		legacyAPI.Multipart == nil ||
		len(legacyAPI.TaskAliases) != 1 ||
		len(legacyAPI.AssetAliases) != 1 ||
		len(legacyAPI.OutputProjections) != 4 {
		t.Fatalf("legacy retexture contract = %#v", legacyAPI)
	}
	if legacyAPI.Multipart.TargetParameter != "source_asset" {
		t.Fatalf("retexture multipart = %#v", legacyAPI.Multipart)
	}
	for _, key := range []string{
		"ui.retexture.title",
		"ui.retexture.description",
		"ui.retexture.input_section",
		"ui.retexture.style_section",
		"ui.retexture.parameters_section",
		"ui.retexture.upload_failed_status",
		"ui.retexture.task_created",
		"ui.retexture.progress",
		"ui.retexture.preview",
		"ui.retexture.download",
		"ui.retexture.empty",
	} {
		if application.Content.Messages[key] == "" {
			t.Errorf("retexture UI message %q missing", key)
		}
	}

	offering, exists := taskCatalog.Get("meshy/processing/retexture")
	if !exists {
		t.Fatal("retexture offering is missing")
	}
	parameters := make(map[string]generation.Parameter, len(offering.Parameters))
	for _, parameter := range offering.Parameters {
		parameters[parameter.Name] = parameter
	}
	if len(offering.InputRules) != 2 ||
		parameters["source_task"].WireName != "input_task_id" ||
		parameters["source_url"].WireName != "model_url" ||
		parameters["source_asset"].WireName != "model_url" ||
		len(parameters["source_asset"].Extensions) != 5 ||
		parameters["source_asset"].MaxBytes != 70*1024*1024 ||
		parameters["prompt"].WireName != "text_style_prompt" ||
		parameters["style_asset"].WireName != "image_style_url" ||
		parameters["style_asset"].Semantic != generation.SemanticReferenceImage {
		t.Fatalf("retexture offering schema = %#v", offering)
	}
	for name, input := range map[string]json.RawMessage{
		"task and prompt": json.RawMessage(
			`{"source_task":"task-model","prompt":"painted stone"}`,
		),
		"url and image": json.RawMessage(
			`{"source_url":"https://assets.test/model.glb","style_asset":"asset-style"}`,
		),
		"asset and prompt": json.RawMessage(
			`{"source_asset":"asset-model","prompt":"painted stone"}`,
		),
	} {
		if _, _, normalizeErr := taskCatalog.Normalize(
			generation.Retexture,
			"meshy/processing/retexture",
			input,
		); normalizeErr != nil {
			t.Errorf("%s: %v", name, normalizeErr)
		}
	}
	for name, input := range map[string]json.RawMessage{
		"missing source": json.RawMessage(`{"prompt":"painted stone"}`),
		"multiple sources": json.RawMessage(
			`{"source_task":"task-model","source_url":"https://assets.test/model.glb","prompt":"painted stone"}`,
		),
		"missing style": json.RawMessage(`{"source_task":"task-model"}`),
		"multiple styles": json.RawMessage(
			`{"source_task":"task-model","prompt":"painted stone","style_asset":"asset-style"}`,
		),
	} {
		if _, _, normalizeErr := taskCatalog.Normalize(
			generation.Retexture,
			"meshy/processing/retexture",
			input,
		); normalizeErr == nil {
			t.Errorf("%s unexpectedly accepted", name)
		}
	}
}

func TestDefaultCatalogOwnsLegacyBackgroundRemovalBatchAndDownloadProtocol(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	var legacyAPI *catalog.LegacyTaskAPISchema
	for index := range application.Capabilities {
		if application.Capabilities[index].ID == generation.BackgroundRemove {
			legacyAPI = application.Capabilities[index].LegacyAPI
			break
		}
	}
	if legacyAPI == nil ||
		legacyAPI.Path != "/api/meshy/background-removal" ||
		legacyAPI.Multipart == nil ||
		legacyAPI.Multipart.Cardinality != "many" ||
		legacyAPI.Multipart.FileField != "files" ||
		legacyAPI.Multipart.ParamsField != "pipeline_params" ||
		legacyAPI.Multipart.TargetParameter != "source_assets" ||
		legacyAPI.AssetDownload == nil ||
		legacyAPI.AssetDownload.Path !=
			"/api/meshy/background-removal/download/{assetToken}" ||
		legacyAPI.AssetDownload.TokenParameter != "assetToken" {
		t.Fatalf("legacy background-removal contract = %#v", legacyAPI)
	}
	recordProjectionFound := false
	for _, projection := range legacyAPI.OutputProjections {
		if projection.Name != "tasks" || projection.Mode != "records" {
			continue
		}
		recordProjectionFound = true
		if projection.Role != "image" ||
			projection.RecordSource != "provider-data" ||
			len(projection.RecordFields) != 7 {
			t.Fatalf("background-removal task projection = %#v", projection)
		}
		semantics := map[string]bool{}
		for _, field := range projection.RecordFields {
			semantics[field.Semantic] = true
		}
		for _, semantic := range []string{
			"task-id",
			"status",
			"downloadable",
			"download-path",
			"source-name",
			"error-message",
			"invalid-reason",
		} {
			if !semantics[semantic] {
				t.Errorf("record semantic %q missing", semantic)
			}
		}
	}
	if !recordProjectionFound {
		t.Fatal("background-removal records output projection missing")
	}
	offering, exists := taskCatalog.Get("matting/default/background-removal")
	if !exists {
		t.Fatal("background-removal offering missing")
	}
	if len(offering.ProviderOptions) == 0 ||
		offering.Revision != "2026-07-26.2" {
		t.Fatalf("background-removal offering protocol = %#v", offering)
	}
	for _, key := range []string{
		"ui.background_removal.title",
		"ui.background_removal.description",
		"ui.background_removal.upload_section",
		"ui.background_removal.parameters_section",
		"ui.background_removal.submit",
		"ui.background_removal.results",
		"ui.background_removal.download",
		"ui.background_removal.empty",
		"ui.background_removal.loading",
		"ui.background_removal.upload_description",
		"ui.background_removal.drop_prompt",
		"ui.background_removal.selected_count",
		"ui.background_removal.invalid_files",
		"ui.background_removal.parameters_description",
		"ui.background_removal.task_created",
		"ui.background_removal.create_missing_id",
		"ui.background_removal.create_failed",
		"ui.background_removal.cooldown",
		"ui.background_removal.resubmit",
		"ui.background_removal.new_task",
		"ui.background_removal.progress",
		"ui.background_removal.task_id",
		"ui.background_removal.summary",
		"ui.background_removal.summary_failed",
		"ui.background_removal.downloadable_summary",
		"ui.background_removal.batch_download",
		"ui.background_removal.download_started",
		"ui.background_removal.preview_alt",
		"ui.background_removal.history",
		"ui.background_removal.history_summary",
		"ui.background_removal.history_downloadable",
		"ui.background_removal.download_filename",
	} {
		if application.Content.Messages[key] == "" {
			t.Errorf("background-removal UI message %q missing", key)
		}
	}
	frontend := application.Runtime.Frontend
	if frontend.TaskSubmissionCooldownSeconds <= 0 ||
		frontend.TaskQueueMaximum <= 0 ||
		frontend.BatchDownloadIntervalMilliseconds <= 0 ||
		frontend.TaskIdentifierPreviewCharacters <= 0 ||
		frontend.CountdownTickMilliseconds <= 0 ||
		frontend.TaskPollingIntervalMilliseconds <= 0 ||
		frontend.DisplayLocale == "" {
		t.Fatalf("background-removal frontend runtime = %#v", frontend)
	}
	if frontend.TaskProgress.InitialState == "" ||
		frontend.TaskProgress.NotFoundState == "" {
		t.Fatalf("task progress runtime states = %#v", frontend.TaskProgress)
	}
}

func TestDefaultCatalogOwnsTaskStateRuntimeBehavior(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	states := taskCatalog.Application().TaskStates
	if len(states) == 0 {
		t.Fatal("task states missing")
	}
	requiredPresentations := map[string]string{
		"PENDING":            "reported",
		"IN_PROGRESS":        "reported",
		"PROCESSING":         "reported",
		"UPSTREAM_COMPLETED": "fixed",
		"SUCCEEDED":          "hidden",
		"FAILED":             "hidden",
		"CANCELED":           "hidden",
		"EXPIRED":            "hidden",
		"RETRY_PENDING":      "retry-countdown",
	}
	for _, state := range states {
		if state.Outcome == "" {
			t.Errorf("task state %q outcome missing", state.InternalState)
		}
		if expected, exists := requiredPresentations[state.InternalState]; exists {
			if state.Progress.Mode != expected {
				t.Errorf(
					"task state %q progress mode = %q, want %q",
					state.InternalState,
					state.Progress.Mode,
					expected,
				)
			}
			delete(requiredPresentations, state.InternalState)
		}
		switch generation.TaskState(state.InternalState) {
		case generation.TaskSucceeded,
			generation.TaskFailed,
			generation.TaskCanceled:
			if !state.Terminal {
				t.Errorf("task state %q must be terminal", state.InternalState)
			}
		default:
			if generation.TaskState(state.InternalState).Valid() && state.Terminal {
				t.Errorf("task state %q must not be terminal", state.InternalState)
			}
		}
	}
	if len(requiredPresentations) != 0 {
		t.Errorf("task state presentations missing: %#v", requiredPresentations)
	}
	for _, key := range []string{
		"ui.task_progress.retry_badge",
		"ui.task_progress.retry_imminent",
		"ui.task_progress.retry_seconds",
		"ui.task_progress.retry_minutes",
		"ui.task_progress.retry_hours",
	} {
		if taskCatalog.Application().Content.Messages[key] == "" {
			t.Errorf("task progress message %q missing", key)
		}
	}
}

func TestDefaultCatalogOwnsHeaderAndAssetKindPresentation(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	if schema.Runtime.Frontend.CacheStatusRefreshMilliseconds <= 0 {
		t.Fatal("cache status refresh interval missing")
	}
	if len(schema.Content.AssetKinds) < 10 {
		t.Fatalf("asset kind labels = %d", len(schema.Content.AssetKinds))
	}
	for _, key := range []string{
		"ui.header.menu_toggle",
		"ui.header.failures_show_title",
		"ui.header.failures_hide_title",
		"ui.header.cache_count",
		"ui.header.logout",
		"ui.header.logout_success",
		"ui.header.logout_failed",
	} {
		if schema.Content.Messages[key] == "" {
			t.Errorf("message %q missing", key)
		}
	}
}

func TestTextToImageParameterDependenciesComeFromOfferingSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	for _, offering := range taskCatalog.List(generation.Capability("text-to-image")) {
		var hasMultiView bool
		var aspectRatio generation.Parameter
		for _, parameter := range offering.Parameters {
			switch parameter.Name {
			case "generate_multi_view":
				hasMultiView = true
			case "aspect_ratio":
				aspectRatio = parameter
			}
		}
		if !hasMultiView {
			continue
		}
		checked++
		if aspectRatio.Presentation == nil ||
			len(aspectRatio.Presentation.DisabledWhen) != 1 ||
			aspectRatio.Presentation.DisabledWhen[0].Parameter != "generate_multi_view" {
			t.Errorf("%s aspect_ratio dependency = %#v", offering.ID, aspectRatio.Presentation)
		}
	}
	if checked != 2 {
		t.Fatalf("multi-view text-to-image offerings checked = %d", checked)
	}
}

func TestImageToImageBehaviorAndNoticesComeFromProductSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	messages := taskCatalog.Application().Content.Messages
	for _, key := range []string{
		"behavior.image_to_image.multi_reference_prompt",
		"ui.image_to_image.aspect_ratio_warning",
		"ui.image_to_image.reference_required",
	} {
		if messages[key] == "" {
			t.Errorf("missing Product Schema message %q", key)
		}
	}

	var checked int
	for _, offering := range taskCatalog.List(generation.Capability("image-to-image")) {
		var hasMultiView bool
		var aspectRatio generation.Parameter
		for _, parameter := range offering.Parameters {
			switch parameter.Name {
			case "generate_multi_view":
				hasMultiView = true
			case "aspect_ratio":
				aspectRatio = parameter
			}
		}
		if !hasMultiView {
			continue
		}
		checked++
		if aspectRatio.Presentation == nil ||
			len(aspectRatio.Presentation.DisabledWhen) != 1 ||
			aspectRatio.Presentation.DisabledWhen[0].Parameter != "generate_multi_view" {
			t.Errorf("%s aspect_ratio dependency = %#v", offering.ID, aspectRatio.Presentation)
			continue
		}
		if len(aspectRatio.Presentation.Notices) != 1 ||
			aspectRatio.Presentation.Notices[0].MessageKey !=
				"ui.image_to_image.aspect_ratio_warning" {
			t.Errorf("%s aspect_ratio notices = %#v", offering.ID, aspectRatio.Presentation.Notices)
		}
	}
	if checked != 2 {
		t.Fatalf("multi-view image-to-image offerings checked = %d", checked)
	}
}

func TestEveryImageToImageOfferingDeclaresReferenceImageParameter(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offerings := taskCatalog.List(generation.Capability("image-to-image"))
	if len(offerings) == 0 {
		t.Fatal("catalog has no image-to-image offerings")
	}
	for _, offering := range offerings {
		var reference generation.Parameter
		var declared int
		for _, parameter := range offering.Parameters {
			if parameter.Type == "asset-array" &&
				parameter.Semantic == generation.SemanticReferenceImage {
				declared++
				reference = parameter
			}
		}
		if declared != 1 {
			t.Errorf(
				"%s 声明了 %d 个 reference-image asset-array 参数，图生图输入资产将无法解析",
				offering.ID,
				declared,
			)
			continue
		}
		if !reference.Required {
			t.Errorf("%s reference_assets 应为必填", offering.ID)
		}
		if reference.MinItems < 1 || reference.MaxItems < reference.MinItems {
			t.Errorf(
				"%s reference_assets items = [%d, %d]",
				offering.ID,
				reference.MinItems,
				reference.MaxItems,
			)
		}
		if reference.MaxBytes <= 0 {
			t.Errorf("%s reference_assets 缺少大小上限", offering.ID)
		}
	}
}

func TestApplicationRuntimeConfigurationIsDefensivelyClonedAndValidated(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	setting := first.Runtime.Configuration.Settings["address"]
	setting.Default = "mutated"
	first.Runtime.Configuration.Settings["address"] = setting

	second := taskCatalog.Application()
	if second.Runtime.Configuration.Settings["address"].Default == "mutated" {
		t.Fatal("runtime configuration map leaked through Application()")
	}

	delete(second.Runtime.Configuration.Settings, "address")
	if err := catalog.ValidateApplicationSchema(second); err == nil {
		t.Fatal("missing required runtime setting was accepted")
	}

	third := taskCatalog.Application()
	third.Runtime.InputAssets.TypeRules[0].MediaTypes[0] = "mutated/type"
	third.TaskStream.Aliases[0].ExternalType = "mutated-stream-type"
	for index := range third.Capabilities {
		if third.Capabilities[index].PromptComposition == nil {
			continue
		}
		third.Capabilities[index].PromptComposition.MinimumReferences = 99
	}
	fourth := taskCatalog.Application()
	if fourth.Runtime.InputAssets.TypeRules[0].MediaTypes[0] == "mutated/type" {
		t.Fatal("input asset type rules leaked through Application()")
	}
	if fourth.TaskStream.Aliases[0].ExternalType == "mutated-stream-type" {
		t.Fatal("task stream aliases leaked through Application()")
	}
	for _, capability := range fourth.Capabilities {
		if capability.ID == generation.ImageToImage &&
			capability.PromptComposition != nil &&
			capability.PromptComposition.MinimumReferences == 99 {
			t.Fatal("prompt composition pointer leaked through Application()")
		}
	}
	fourth.Runtime.InputAssets.DefaultAssetType = "unknown"
	if err := catalog.ValidateApplicationSchema(fourth); err == nil {
		t.Fatal("unknown default input asset type was accepted")
	}
}

func TestProviderDiscoveryCompatibilityIsDefensivelyClonedAndValidated(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	modelIndex := -1
	for index, model := range first.ProviderDiscovery.LegacyModels {
		if model.ModelID == "meshy-7.1" {
			modelIndex = index
			break
		}
	}
	if modelIndex < 0 {
		t.Fatal("meshy-7.1 legacy model is missing")
	}
	model := first.ProviderDiscovery.LegacyModels[modelIndex]
	parameterProjection := model.ParameterProjections[generation.TextTo3D]
	parameterProjection.Order[0] = "mutated"
	texturePrompt := parameterProjection.Fields["texture_prompt"]
	*texturePrompt.Required = true
	texturePrompt.Omit = append(texturePrompt.Omit, "description")
	parameterProjection.Fields["texture_prompt"] = texturePrompt
	model.ParameterProjections[generation.TextTo3D] = parameterProjection
	constraintProjection := model.ConstraintProjections[generation.ImageTo3D]
	*constraintProjection.Overrides.PromptMaxLength = 999
	model.ConstraintProjections[generation.ImageTo3D] = constraintProjection
	// meshy-7.1 配置本身 recommended=true；翻转初始值以验证克隆隔离而非具体取值
	initialRecommended := *model.Recommended
	*model.Recommended = !initialRecommended
	first.ProviderDiscovery.LegacyModels[modelIndex] = model

	second := taskCatalog.Application()
	secondModel := second.ProviderDiscovery.LegacyModels[modelIndex]
	secondParameters := secondModel.ParameterProjections[generation.TextTo3D]
	if secondParameters.Order[0] == "mutated" ||
		*secondParameters.Fields["texture_prompt"].Required ||
		len(secondParameters.Fields["texture_prompt"].Omit) != 0 {
		t.Fatalf("parameter compatibility projection leaked: %#v", secondParameters)
	}
	secondConstraints := secondModel.ConstraintProjections[generation.ImageTo3D]
	if *secondConstraints.Overrides.PromptMaxLength != 600 {
		t.Fatalf("constraint compatibility projection leaked: %#v", secondConstraints)
	}
	if *secondModel.Recommended != initialRecommended {
		t.Fatal("recommended pointer leaked through Application()")
	}

	invalid := taskCatalog.Application()
	invalidModel := invalid.ProviderDiscovery.LegacyModels[modelIndex]
	invalidProjection := invalidModel.ParameterProjections[generation.TextTo3D]
	invalidTexturePrompt := invalidProjection.Fields["texture_prompt"]
	invalidTexturePrompt.Omit = []string{"unknown-field"}
	invalidProjection.Fields["texture_prompt"] = invalidTexturePrompt
	invalidModel.ParameterProjections[generation.TextTo3D] = invalidProjection
	invalid.ProviderDiscovery.LegacyModels[modelIndex] = invalidModel
	if err := catalog.ValidateApplicationSchema(invalid); err == nil {
		t.Fatal("unknown compatibility field was accepted")
	}
}

func TestDefaultCatalogOwnsEveryMeshyWireRoute(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, offering := range taskCatalog.List("") {
		if offering.ProviderID != "meshy" {
			continue
		}
		var options struct {
			Endpoint   string `json:"endpoint"`
			APIVersion string `json:"api_version"`
		}
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatalf("%s provider_options: %v", offering.ID, err)
		}
		if options.Endpoint == "" || options.APIVersion == "" {
			t.Errorf("%s wire route = %#v", offering.ID, options)
		}
	}
}

func TestDefaultCatalogOwnsPublicErrorAndFailureMessages(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	messages := taskCatalog.Application().Content.Messages
	for _, key := range []string{
		"error.internal",
		"error.invalid_capability",
		"error.invalid_input",
		"error.unknown_offering",
		"error.no_offering",
		"error.idempotency_conflict",
		"error.not_found",
		"error.conflict",
		"error.unavailable",
		"failure.invalid_input",
		"failure.authentication",
		"failure.rate_limited",
		"failure.transport",
		"failure.timed_out",
		"failure.rejected",
		"failure.insufficient_balance",
		"failure.credential_quota",
		"failure.upstream_unavailable",
		"failure.submission_unknown",
		"failure.integrity_failure",
		"failure.protocol",
		"failure.internal",
		"failure.source_expired",
	} {
		if messages[key] == "" {
			t.Errorf("missing Product Schema message %q", key)
		}
	}
}

func TestDefaultCatalogOwnsBrandAndNavigation(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	content := taskCatalog.Application().Content
	if content.Brand.Title != "Plinth" ||
		content.Brand.MetadataTitle == "" ||
		content.Brand.MetadataDescription == "" {
		t.Fatalf("brand = %#v", content.Brand)
	}
	// 导航只保留真实可达的目的地：工作台与管理后台。旧的 per-capability 表单页、
	// chat/canvas/tasks 页面已整体下线，声明它们只会让 UI 出现指向 404 的死链。
	wantNavigation := map[string]struct {
		href      string
		placement string
		adminOnly bool
	}{
		"workspace": {href: "/", placement: "main"},
		"account":   {href: "/account", placement: "main"},
		"admin":     {href: "/admin", placement: "bottom", adminOnly: true},
	}
	if len(content.Navigation) != len(wantNavigation) {
		t.Fatalf(
			"navigation = %#v; want %d 条真实可达目的地",
			content.Navigation,
			len(wantNavigation),
		)
	}
	seen := make(map[string]struct{}, len(content.Navigation))
	for index, item := range content.Navigation {
		if item.Order != index {
			t.Errorf("navigation[%d].order = %d", index, item.Order)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			t.Errorf("navigation key 重复: %q", item.Key)
		}
		seen[item.Key] = struct{}{}
		expected, exists := wantNavigation[item.Key]
		if !exists {
			t.Errorf("navigation[%d] 指向已下线或未登记的目的地: %#v", index, item)
			continue
		}
		if item.Href != expected.href ||
			item.Placement != expected.placement ||
			item.AdminOnly != expected.adminOnly {
			t.Errorf("navigation[%d] = %#v", index, item)
		}
	}
}

func TestDefaultCatalogOwnsCanvasPermissionRedirect(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got := taskCatalog.Application().Canvas.Frontend.PermissionRedirectPath; got != "/tasks" {
		t.Fatalf("canvas.frontend.permission_redirect_path = %q", got)
	}
}

func TestNormalizeUsesExplicitPriorityAndCanonicalDefaults(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, normalized, err := taskCatalog.Normalize(
		generation.TextToImage,
		"",
		[]byte(`{"prompt":"owl"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if offering.ID != "openai/gpt-image-2/text-to-image" {
		t.Fatalf("default offering = %q", offering.ID)
	}
	var input map[string]any
	if err := json.Unmarshal(normalized, &input); err != nil {
		t.Fatal(err)
	}
	// quality 在 Schema 声明为 hidden 且默认最高（画质不给选不给看），
	// normalize 以 Schema default 注入。
	// #267: the output size is an explicit target, never "auto".
	if input["size"] != "2048x2048" || input["quality"] != "max" {
		t.Fatalf("normalized = %s", normalized)
	}
}

func TestNormalizeRejectsUnknownParametersAndAssetCountOverflow(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = taskCatalog.Normalize(
		generation.TextToImage,
		"openai/gpt-image-2/text-to-image",
		[]byte(`{"prompt":"owl","private_flag":true}`),
	)
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) || domainErr.Code != generation.ErrInvalidInput {
		t.Fatalf("error = %#v", err)
	}
	_, _, err = taskCatalog.Normalize(
		generation.ImageTo3D,
		"tripo/tripo-v3.1-full/image-to-3d",
		[]byte(`{"reference_assets":["1","2"]}`),
	)
	if !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("error = %#v", err)
	}
}

func TestInputPresenceRulesAreLoadedAndEnforcedFromOfferingSchema(t *testing.T) {
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"M","capability":"text-to-image",
				"domain":"image",
				"display_order":0,
				"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,
				"poll_interval_seconds":1,
				"retry":{"max_attempts":1},
				"materialize_attempts":1,
				"parameters":[
					{"name":"source_a","label":"来源 A","type":"text"},
					{"name":"source_b","label":"来源 B","type":"text"}
				],
				"input_rules":[{
					"parameters":["source_a","source_b"],
					"minimum":1,
					"maximum":1
				}],
				"outputs":[{"role":"image","minimum":1}]
			}
		]`)
	taskCatalog, err := catalog.Load(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := taskCatalog.Normalize(
		generation.TextToImage,
		"one",
		json.RawMessage(`{"source_a":"a"}`),
	); err != nil {
		t.Fatalf("one source rejected: %v", err)
	}
	for name, input := range map[string]json.RawMessage{
		"none": json.RawMessage(`{}`),
		"two":  json.RawMessage(`{"source_a":"a","source_b":"b"}`),
	} {
		if _, _, err := taskCatalog.Normalize(
			generation.TextToImage,
			"one",
			input,
		); err == nil {
			t.Errorf("%s source count accepted", name)
		}
	}
}

func TestLoadRejectsDuplicateOfferingFeatures(t *testing.T) {
	document := testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,
			"features":["skills","skills"],
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[],
			"outputs":[{"role":"image","minimum":1}]
		}
	]`)
	_, err := catalog.Load(bytes.NewReader(document))
	if err == nil || !strings.Contains(err.Error(), "feature") {
		t.Fatalf("load error = %v", err)
	}
}

func TestLoadRejectsInconsistentProviderCreditsMetered(t *testing.T) {
	offering := func(id string, model string, enabled bool, order int, metered string) string {
		return fmt.Sprintf(`{
			"id":%q,"revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":%q,"model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":%d,"capability_order":%d,
			"adapter_key":"a","enabled":%t,%s
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[],
			"outputs":[{"role":"image","minimum":1}]
		}`, id, model, order, order, enabled, metered)
	}
	mixed := testProductDocument("[" +
		offering("one", "m1", true, 0, `"credits_metered":true,`) + "," +
		offering("two", "m2", false, 1, "") + "]")
	_, err := catalog.Load(bytes.NewReader(mixed))
	if err == nil || !strings.Contains(err.Error(), "credits_metered") {
		t.Fatalf("load error = %v", err)
	}
	consistent := testProductDocument("[" +
		offering("one", "m1", true, 0, `"credits_metered":true,`) + "," +
		offering("two", "m2", false, 1, `"credits_metered":true,`) + "]")
	// Other document-level checks may still reject this minimal fixture; only
	// the credits_metered consistency check is under test.
	_, err = catalog.Load(bytes.NewReader(consistent))
	if err != nil && strings.Contains(err.Error(), "credits_metered") {
		t.Fatalf("consistent provider rejected: %v", err)
	}
}

func TestLoadRequiresConsistentShortProviderLabel(t *testing.T) {
	offering := func(id, model, label string, order int) string {
		return fmt.Sprintf(`{
			"id":%q,"revision":"1","provider_id":"p","provider_name":"P","provider_short_label":%q,
			"model_id":%q,"model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":%d,"capability_order":%d,
			"adapter_key":"a","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[],
			"outputs":[{"role":"image","minimum":1}]
		}`, id, label, model, order, order)
	}
	for name, label := range map[string]string{"empty": "", "too long": "ABCDEFGHI"} {
		_, err := catalog.Load(bytes.NewReader(testProductDocument("[" + offering("one", "m1", label, 0) + "]")))
		if err == nil || !strings.Contains(err.Error(), "provider_short_label") {
			t.Fatalf("%s label: load error = %v", name, err)
		}
	}
	mixed := testProductDocument("[" + offering("one", "m1", "AAA", 0) + "," + offering("two", "m2", "BBB", 1) + "]")
	if _, err := catalog.Load(bytes.NewReader(mixed)); err == nil || !strings.Contains(err.Error(), "provider_short_label 不一致") {
		t.Fatalf("mixed labels: load error = %v", err)
	}
}

// #100: the model tables sort by provider; the rank is declared on the Offerings, once per provider is enough.
func TestLoadValidatesProviderOrder(t *testing.T) {
	offering := func(id, provider, model string, rank string, order int) string {
		return fmt.Sprintf(`{
			"id":%q,"revision":"1","provider_id":%q,"provider_name":"P","provider_short_label":"P",%s
			"model_id":%q,"model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":%d,"capability_order":0,
			"adapter_key":"a","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[],
			"outputs":[{"role":"image","minimum":1}]
		}`, id, provider, rank, model, order)
	}
	load := func(offerings ...string) error {
		_, err := catalog.Load(bytes.NewReader(testProductDocument("[" + strings.Join(offerings, ",") + "]")))
		return err
	}
	for name, tt := range map[string]struct {
		offerings []string
		want      string
	}{
		"one provider disagrees": {[]string{offering("a", "p", "m1", `"provider_order":1,`, 0), offering("b", "p", "m2", `"provider_order":2,`, 1)}, "provider_order 不一致"},
		"two providers share":    {[]string{offering("a", "p", "m1", `"provider_order":1,`, 0), offering("b", "q", "m2", `"provider_order":1,`, 1)}, "同时使用"},
		"negative":               {[]string{offering("a", "p", "m1", `"provider_order":-1,`, 0)}, "provider_order 不能为负数"},
	} {
		if err := load(tt.offerings...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: load error = %v", name, err)
		}
	}
	if err := load(offering("a", "p", "m1", `"provider_order":1,`, 0), offering("b", "p", "m2", "", 1)); err != nil && strings.Contains(err.Error(), "provider_order") {
		t.Fatalf("a provider that declares its rank once was rejected: %v", err)
	}
}

func TestDefaultCatalogDeclaresProviderOrderForEveryOffering(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rank := map[string]int{}
	for _, offering := range c.List("") {
		if offering.ProviderOrder < 1 {
			t.Fatalf("%s has no provider_order", offering.ID)
		}
		if previous, seen := rank[offering.ProviderID]; seen && previous != offering.ProviderOrder {
			t.Fatalf("provider %s ranks %d and %d", offering.ProviderID, previous, offering.ProviderOrder)
		}
		rank[offering.ProviderID] = offering.ProviderOrder
	}
	if !(rank["meshy"] < rank["tripo"] && rank["tripo"] < rank["hyper3d"]) {
		t.Fatalf("3D provider ranks = %v", rank)
	}
}

func TestLoadValidatesMultipartImageProtocolDeclaration(t *testing.T) {
	document := testProductDocument(`[
		{
			"id":"openai/images","revision":"1","provider_id":"openai","provider_name":"OpenAI","provider_short_label":"GPT",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"openai-images.v1","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[
				{"name":"prompt","label":"P","type":"text","required":true},
				{"name":"reference_assets","label":"R","type":"asset-array","required":true,"max_bytes":10,"wire_name":"image[]"},
				{"name":"mask","label":"M","type":"asset","max_bytes":4,"wire_name":"mask"}
			],
			"outputs":[{"role":"image","minimum":1}],
			"provider_options":{
				"endpoint":"images/edits",
				"request_encoding":"multipart",
				"request_template":{"model":"$model"},
				"response":{"images_path":"data.*","base64_field":"b64_json","role":"image","default_media_type":"image/png"},
				"max_response_bytes":1024
			}
		}
	]`)
	if _, err := catalog.Load(bytes.NewReader(document)); err != nil {
		t.Fatalf("valid multipart protocol rejected: %v", err)
	}
	mutations := map[string]string{
		"unknown encoding": `"request_encoding":"multipart"`,
		"nested field":     `"wire_name":"image[]"`,
		"object template":  `{"name":"mask","label":"M","type":"asset","max_bytes":4,"wire_name":"mask"}`,
	}
	replacements := map[string]string{
		"unknown encoding": `"request_encoding":"binary"`,
		"nested field":     `"wire_name":"images.0.image_url"`,
		"object template":  `{"name":"mask","label":"M","type":"asset","max_bytes":4,"wire_name":"mask","wire_template":{"image_url":"$data_uri"}}`,
	}
	for name, target := range mutations {
		mutated := bytes.Replace(
			document,
			[]byte(target),
			[]byte(replacements[name]),
			1,
		)
		if _, err := catalog.Load(bytes.NewReader(mutated)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDefaultOpenAIImageIdentityAndParametersFollowDeclaration(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []generation.OfferingID{
		"openai/gpt-image-2/text-to-image",
		"openai/gpt-image-2/image-to-image",
	} {
		offering, exists := taskCatalog.Get(id)
		if !exists {
			t.Fatalf("missing offering %q", id)
		}
		if offering.ModelID != "gpt-image-2.5-sunburst" {
			t.Errorf("%s model_id = %q", id, offering.ModelID)
		}
		var quality generation.Parameter
		for _, parameter := range offering.Parameters {
			if parameter.Name == "quality" {
				quality = parameter
			}
		}
		values := make([]string, 0, len(quality.Options))
		for _, option := range quality.Options {
			values = append(values, option.Value)
		}
		for _, required := range []string{"auto", "low", "medium", "high", "xhigh", "max"} {
			if !slices.Contains(values, required) {
				t.Errorf("%s quality options %v missing %q", id, values, required)
			}
		}
	}
	edit, _ := taskCatalog.Get("openai/gpt-image-2/image-to-image")
	var protocol generation.ImageProtocol
	if err := json.Unmarshal(edit.ProviderOptions, &protocol); err != nil {
		t.Fatal(err)
	}
	if protocol.RequestEncoding != "multipart" {
		t.Fatalf("edit encoding = %q", protocol.RequestEncoding)
	}
	generation_, _ := taskCatalog.Get("openai/gpt-image-2/text-to-image")
	var generateProtocol generation.ImageProtocol
	if err := json.Unmarshal(generation_.ProviderOptions, &generateProtocol); err != nil {
		t.Fatal(err)
	}
	if generateProtocol.RequestEncoding != "" {
		t.Fatalf("generation encoding = %q", generateProtocol.RequestEncoding)
	}
}

func TestLoadRejectsInvalidAdvancedModelDefaultMode(t *testing.T) {
	document := testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[],
			"outputs":[{"role":"image","minimum":1}]
		}
	]`)
	document = bytes.Replace(
		document,
		[]byte(`"advanced_model_default_mode":"regular"`),
		[]byte(`"advanced_model_default_mode":"unknown"`),
		1,
	)
	_, err := catalog.Load(bytes.NewReader(document))
	if err == nil ||
		!strings.Contains(err.Error(), "advanced_model_default_mode") {
		t.Fatalf("load error = %v", err)
	}
}

func TestDefaultCatalogDeclaresSameProviderOnTaskDependencyParameters(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	taskParameters := map[string]generation.Parameter{}
	for _, id := range []generation.OfferingID{
		"meshy/meshy-7/text-to-3d",
		"meshy/processing/remesh",
		"meshy/processing/retexture",
		"meshy/processing/rigging",
		"meshy/processing/animation",
	} {
		offering, exists := taskCatalog.Get(id)
		if !exists {
			t.Fatalf("offering %q is missing", id)
		}
		for _, parameter := range offering.Parameters {
			if parameter.Type == "task" {
				taskParameters[string(id)] = parameter
			}
		}
	}
	if len(taskParameters) != 5 {
		t.Fatalf("task parameters = %#v", taskParameters)
	}
	for id, parameter := range taskParameters {
		if !parameter.SameProvider {
			t.Errorf("offering %q task parameter %q lacks same_provider", id, parameter.Name)
		}
	}
}

func TestLoadRejectsSameProviderOnNonTaskParameter(t *testing.T) {
	document := testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[{
				"name":"source_url","label":"来源","type":"text",
				"same_provider":true
			}],
			"outputs":[{"role":"image","minimum":1}]
		}
	]`)
	_, err := catalog.Load(bytes.NewReader(document))
	if err == nil ||
		!strings.Contains(err.Error(), "非 task 参数") ||
		!strings.Contains(err.Error(), "same_provider") {
		t.Fatalf("load error = %v", err)
	}
}

func TestLoadStrictlyRejectsUnknownFields(t *testing.T) {
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"M","capability":"text-to-image",
				"domain":"image",
				"display_order":0,
				"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,
				"poll_interval_seconds":1,
				"retry":{"max_attempts":1},
				"materialize_attempts":1,
				"parameters":[],
				"outputs":[{"role":"image","minimum":1}],
				"unknown":true
			}
		]`)
	if _, err := catalog.Load(bytes.NewReader(document)); err == nil {
		t.Fatal("strict decoder accepted unknown field")
	}
}

func TestLoadPreservesSchemaDrivenPresentation(t *testing.T) {
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"模型 M","capability":"text-to-image",
				"domain":"image",
				"display_order":0,
				"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,
				"poll_interval_seconds":1,
				"retry":{"max_attempts":1},
				"materialize_attempts":1,
				"parameters":[{
					"name":"quality","label":"生成质量","type":"select",
					"default":"high",
					"options":[{"value":"high","label":"高质量"}],
					"presentation":{"surface":"provider","control":"select"}
				}],
				"outputs":[{"role":"image","minimum":1}]
			}
		]`)
	taskCatalog, err := catalog.Load(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("one")
	if !exists {
		t.Fatal("offering missing")
	}
	presentation := offering.Parameters[0].Presentation
	if presentation == nil ||
		presentation.Surface != generation.SurfaceProvider ||
		presentation.Control != generation.ControlSelect {
		t.Fatalf("presentation = %#v", presentation)
	}
}

func TestLoadAcceptsSchemaDrivenSliderControl(t *testing.T) {
	// W2：slider 是合法控件，但要求数值参数显式 minimum/maximum。
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"M","capability":"text-to-image",
				"domain":"image",
				"display_order":0,
				"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,
				"poll_interval_seconds":1,
				"retry":{"max_attempts":1},
				"materialize_attempts":1,
				"parameters":[{
					"name":"quality","label":"质量","type":"number",
					"minimum":0,"maximum":100,
					"presentation":{"surface":"provider","control":"slider","step":5}
				}],
				"outputs":[{"role":"image","minimum":1}]
			}
		]`)
	taskCatalog, err := catalog.Load(bytes.NewReader(document))
	if err != nil {
		t.Fatalf("legal slider rejected: %v", err)
	}
	offering, _ := taskCatalog.Get("one")
	if control := offering.Parameters[0].Presentation.Control; control != generation.ControlSlider {
		t.Fatalf("control = %q", control)
	}
}

func TestLoadRejectsInvalidSchemaDrivenPresentation(t *testing.T) {
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"M","capability":"text-to-image",
				"domain":"image",
				"display_order":0,
				"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,
				"poll_interval_seconds":1,
				"retry":{"max_attempts":1},
				"materialize_attempts":1,
				"parameters":[{
					"name":"quality","label":"","type":"select",
					"presentation":{"surface":"provider","control":"slider"}
				}],
				"outputs":[{"role":"image","minimum":1}]
			}
		]`)
	if _, err := catalog.Load(bytes.NewReader(document)); err == nil {
		t.Fatal("invalid presentation accepted")
	}
}

func TestLoadRejectsParameterNoticeWithUnknownMessage(t *testing.T) {
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"M","capability":"text-to-image",
				"domain":"image",
				"display_order":0,
				"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,
				"poll_interval_seconds":1,
				"retry":{"max_attempts":1},
				"materialize_attempts":1,
				"parameters":[{
					"name":"quality","label":"质量","type":"select",
					"default":"high",
					"options":[
						{"value":"high","label":"高"},
						{"value":"low","label":"低"}
					],
					"presentation":{
						"surface":"provider","control":"select",
						"notices":[{
							"when":{
								"parameter":"quality",
								"operator":"not-equals",
								"value":"high"
							},
							"message_key":"missing.notice",
							"tone":"warning"
						}]
					}
				}],
				"outputs":[{"role":"image","minimum":1}]
			}
		]`)
	if _, err := catalog.Load(bytes.NewReader(document)); err == nil {
		t.Fatal("notice with unknown Product Schema message was accepted")
	}
}

func TestDefaultCatalogOwnsChatThinkingAndModelDefaults(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	settings := application.Runtime.Configuration.Settings
	model := settings[application.Chat.Agent.ModelSetting]
	if application.Chat.Agent.ModelSetting != "llm_model" ||
		model.Default != "deepseek" {
		t.Fatalf(
			"chat model setting = %q default %q",
			application.Chat.Agent.ModelSetting,
			model.Default,
		)
	}
	thinking := application.Chat.Agent.Thinking
	// #253: no generic thinking fields; DeepSeek, Qwen and GLM are vendor
	// profiles and only DeepSeek reads DSML tool calls (global chat.dsml is off).
	if thinking == nil || !thinking.Enabled || len(thinking.ModelProfiles) != 4 ||
		!thinking.ModelProfiles[0].DSMLToolCalls ||
		thinking.ModelProfiles[2].ModelPrefixes[0] != "qwen" ||
		string(thinking.ModelProfiles[2].RequestFields["enable_thinking"]) != "true" ||
		application.Chat.DSML.Enabled {
		t.Fatalf("chat agent thinking = %#v, dsml = %v", thinking, application.Chat.DSML.Enabled)
	}
	// DeepSeek thinking mode, default effort "low" (user 2026-10-20; the API
	// accepts low/high/max), reasoning passed back in tool loops
	// (https://api-docs.deepseek.com/guides/thinking_mode). Since #322 the
	// level is the session's choice written to effort_field.
	if thinking.ModelProfiles[0].ModelPrefixes[0] != "deepseek" ||
		thinking.ModelProfiles[0].EffortField != "reasoning_effort" ||
		thinking.ModelProfiles[0].DefaultEffort != "low" ||
		!strings.Contains(strings.Join(strings.Fields(string(thinking.ModelProfiles[0].RequestFields["thinking"])), ""), `{"type":"enabled"}`) ||
		!thinking.ModelProfiles[0].ReasoningPassback {
		t.Fatalf("deepseek thinking profile = %#v", thinking.ModelProfiles)
	}
	application.Chat.Agent.Thinking.Enabled = false
	application.Chat.Agent.Thinking.ModelProfiles[0].ModelPrefixes[0] = "mutated"
	application.Chat.Agent.Thinking.ModelProfiles[0].EffortLevels[0] = "mutated"
	application.Chat.Agent.Thinking.ModelProfiles[0].RequestFields["thinking"][1] = 'X'
	*application.Chat.Agent.Thinking.ModelProfiles[3].StreamIncludeUsage = true
	defensive := taskCatalog.Application()
	if defensive.Chat.Agent.Thinking == nil ||
		!defensive.Chat.Agent.Thinking.Enabled ||
		defensive.Chat.Agent.Thinking.ModelProfiles[0].ModelPrefixes[0] != "deepseek" ||
		defensive.Chat.Agent.Thinking.ModelProfiles[0].EffortLevels[0] != "low" ||
		*defensive.Chat.Agent.Thinking.ModelProfiles[3].StreamIncludeUsage ||
		!strings.Contains(strings.Join(strings.Fields(string(defensive.Chat.Agent.Thinking.ModelProfiles[0].RequestFields["thinking"])), ""), `{"type":"enabled"}`) {
		t.Fatal("Application() did not defensively clone thinking config")
	}
}

func TestLoadValidatesChatThinkingDeclaration(t *testing.T) {
	document := testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[],
			"outputs":[{"role":"image","minimum":1}]
		}
	]`)
	if _, err := catalog.Load(bytes.NewReader(document)); err != nil {
		t.Fatalf("baseline document rejected: %v", err)
	}
	const anchor = `"mock_summary":"configured mock summary"`
	tests := []struct {
		name        string
		declaration string
		wantError   string
	}{
		{
			name:        "enabled",
			declaration: `"thinking":{"enabled":true}`,
		},
		{
			name:        "disabled explicit",
			declaration: `"thinking":{"enabled":false}`,
		},
		{
			// #253: Qwen's own switch is an ordinary vendor profile field now.
			name:        "qwen profile",
			declaration: `"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["qwen"],"request_fields":{"enable_thinking":true,"reasoning_effort":"xhigh"}}]}`,
		},
		{
			name:        "vendor profile",
			declaration: `"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["deepseek"],"request_fields":{"thinking":{"type":"enabled"},"reasoning_effort":"max"},"reasoning_passback":true}]}`,
		},
		{
			name:        "profile overrides a reserved request field",
			declaration: `"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["deepseek"],"request_fields":{"model":"other"}}]}`,
			wantError:   "model",
		},
		{
			name:        "profile prefix not lowercase",
			declaration: `"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["DeepSeek"],"request_fields":{"thinking":{"type":"enabled"}}}]}`,
			wantError:   "DeepSeek",
		},
		{
			name:        "profile prefix declared twice",
			declaration: `"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["deepseek"],"request_fields":{"a":1}},{"model_prefixes":["deepseek"],"request_fields":{"b":1}}]}`,
			wantError:   "deepseek",
		},
		{
			name:        "profile without request fields",
			declaration: `"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["deepseek"]}]}`,
			wantError:   "request_fields",
		},
		{
			name:        "profiles on disabled thinking",
			declaration: `"thinking":{"enabled":false,"model_profiles":[{"model_prefixes":["deepseek"],"request_fields":{"a":1}}]}`,
			wantError:   "model_profiles",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			mutated := bytes.Replace(
				document,
				[]byte(anchor),
				[]byte(anchor+","+testCase.declaration),
				1,
			)
			_, err := catalog.Load(bytes.NewReader(mutated))
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf(
						"valid thinking declaration rejected: %v",
						err,
					)
				}
				return
			}
			if err == nil ||
				!strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf(
					"load error = %v, want mention of %q",
					err,
					testCase.wantError,
				)
			}
		})
	}
	mutated := bytes.Replace(
		document,
		[]byte(anchor),
		[]byte(anchor+`,"thinking":{"enabled":true,"model_profiles":[{"model_prefixes":["deepseek"],"request_fields":{"a":1},"dsml_tool_calls":true}]}`),
		1,
	)
	taskCatalog, err := catalog.Load(bytes.NewReader(mutated))
	if err != nil {
		t.Fatal(err)
	}
	thinking := taskCatalog.Application().Chat.Agent.Thinking
	if thinking == nil ||
		!thinking.Enabled ||
		len(thinking.ModelProfiles) != 1 || !thinking.ModelProfiles[0].DSMLToolCalls {
		t.Fatalf("typed thinking = %#v", thinking)
	}
}

func TestLoadRejectsMultipleActionSemanticParameters(t *testing.T) {
	document := testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"animation",
			"domain":"processing","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,
			"timeout_seconds":1,"poll_interval_seconds":1,
			"retry":{"max_attempts":1},"materialize_attempts":1,
			"parameters":[
				{"name":"source_task","label":"来源","type":"task","semantic":"source-task"},
				{"name":"action_id","label":"动作","type":"number","semantic":"action"},
				{"name":"extra_action","label":"额外动作","type":"number","semantic":"action"}
			],
			"outputs":[{"role":"model","minimum":1}]
		}
	]`)
	if _, err := catalog.Load(bytes.NewReader(document)); err == nil ||
		!strings.Contains(err.Error(), "action 语义参数") {
		t.Fatalf("load error = %v", err)
	}
}

// #274: inpainting uploads the whole artboard, losslessly, at the delivery size, through the
// generic input-asset upload. That upload must not be tighter than the picture the inpaint
// offering itself accepts, or a 4K board is refused before the provider ever sees it.
func TestInputAssetUploadCoversTheInpaintPictureLimit(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	uploadMax := taskCatalog.Application().Runtime.InputAssets.UploadMaxBytes
	var checked int
	for _, offering := range taskCatalog.List(generation.Capability("image-to-image")) {
		var mask bool
		var picture generation.Parameter
		for _, parameter := range offering.Parameters {
			switch parameter.Semantic {
			case "mask":
				mask = true
			case "reference-image":
				picture = parameter
			}
		}
		if !mask {
			continue
		}
		checked++
		if picture.MaxBytes <= 0 || picture.MaxBytes > uploadMax {
			t.Fatalf("%s accepts a %d-byte picture but uploads stop at %d bytes", offering.ID, picture.MaxBytes, uploadMax)
		}
	}
	if checked == 0 {
		t.Fatal("no inpaint offering with a mask parameter was checked")
	}
}
