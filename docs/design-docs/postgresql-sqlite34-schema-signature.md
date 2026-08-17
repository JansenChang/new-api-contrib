# PostgreSQL P-M2：SQLite-34 源 Schema 签名

**Author:** Codex
**Date:** 2026-08-18
**Status:** FACTUAL_BASELINE — 只读元数据采集，不代表生产数据、日志库或切换就绪

## Context（背景）

- 采集时间：2026-08-18（Asia/Shanghai；会话未保留秒级时间）。
- 采集来源：SSH 158 上已确认的生产主 SQLite；仅以 SQLite `mode=ro` 打开，并执行 `PRAGMA table_list`、`table_info`、`index_list`、`index_info`。
- 未读取业务行、DSN、环境变量、配置、卷内容或任何列值；未执行 DDL/DML、事务写入或文件修改。
- 本签名只供 P-M2 的**编译期固定** `TableSpec` 与 `SQLite-34-pre-enterprise` profile 比对。它不是动态表发现协议：未知/缺失表、列或索引均必须失败关闭，不能据本文件扩大复制范围。

记法：`列名:声明类型:nn<notnull>:pk<主键序号>`；`索引名[u<唯一>,<origin>]:列`。SQLite `origin` 为 `c`（显式创建）、`u`（UNIQUE 约束）、`pk`（主键）。

## Functional Requirements（功能需求）

- FR-1: P-M2 的 `SQLite-34-pre-enterprise` profile MUST 只接受本文件列出的 34 张表、全部列与索引签名。
- FR-2: 表、列、主键序号、声明类型、`notnull`、索引名、唯一性或索引列的任一不一致 MUST 停止预检；不得动态补齐、忽略或扩展。
- FR-3: 预检报告 MUST 只记录对象名和差异类别，不得携带任何来源列值。

## Non-Functional Requirements（非功能需求）

- NFR-1：签名比对及报告不得读取或输出业务记录、连接串、配置值或秘密。
- NFR-2：本基线仅描述采集时 SQLite 元数据；它不替代一致性快照、WAL 检查、数据值校验或 PostgreSQL 目标验证。

## 34 表签名

```text
abilities|group:varchar(64):nn0:pk1;model:varchar(255):nn0:pk2;channel_id:INTEGER:nn0:pk3;enabled:numeric:nn0:pk0;priority:INTEGER:nn0:pk0;weight:INTEGER:nn0:pk0;tag:TEXT:nn0:pk0|idx_abilities_priority[u0,c]:priority;idx_abilities_weight[u0,c]:weight;idx_abilities_tag[u0,c]:tag;idx_abilities_channel_id[u0,c]:channel_id;sqlite_autoindex_abilities_1[u1,pk]:group,model,channel_id
auth_flows|id:INTEGER:nn0:pk1;token_hash:char(64):nn1:pk0;purpose:varchar(32):nn1:pk0;provider:varchar(64):nn0:pk0;intent:varchar(16):nn0:pk0;user_id:INTEGER:nn0:pk0;session_id:varchar(64):nn0:pk0;payload:TEXT:nn0:pk0;created_at:datetime:nn0:pk0;expires_at:datetime:nn1:pk0;consumed_at:datetime:nn0:pk0|idx_auth_flows_token_hash[u1,c]:token_hash;idx_auth_flow_purpose_expiry[u0,c]:purpose,expires_at;idx_auth_flows_user_id[u0,c]:user_id;idx_auth_flows_session_id[u0,c]:session_id;idx_auth_flows_consumed_at[u0,c]:consumed_at
authz_roles|id:INTEGER:nn0:pk1;key:TEXT:nn1:pk0;name:TEXT:nn1:pk0;description:TEXT:nn0:pk0;built_in:numeric:nn0:pk0;enabled:numeric:nn0:pk0;sort:INTEGER:nn0:pk0;created_at:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0|idx_authz_roles_key[u1,c]:key
casbin_rule|id:INTEGER:nn0:pk1;ptype:TEXT:nn0:pk0;v0:TEXT:nn0:pk0;v1:TEXT:nn0:pk0;v2:TEXT:nn0:pk0;v3:TEXT:nn0:pk0;v4:TEXT:nn0:pk0;v5:TEXT:nn0:pk0|idx_casbin_rule[u0,c]:ptype,v0,v1,v2,v3,v4,v5;idx_casbin_rule_unique[u1,c]:ptype,v0,v1,v2,v3,v4,v5
channels|id:INTEGER:nn0:pk1;type:INTEGER:nn0:pk0;key:TEXT:nn1:pk0;open_ai_organization:TEXT:nn0:pk0;test_model:TEXT:nn0:pk0;status:INTEGER:nn0:pk0;name:TEXT:nn0:pk0;weight:INTEGER:nn0:pk0;created_time:INTEGER:nn0:pk0;test_time:INTEGER:nn0:pk0;response_time:INTEGER:nn0:pk0;base_url:TEXT:nn0:pk0;other:TEXT:nn0:pk0;balance:REAL:nn0:pk0;balance_updated_time:INTEGER:nn0:pk0;models:TEXT:nn0:pk0;group:varchar(64):nn0:pk0;used_quota:INTEGER:nn0:pk0;model_mapping:TEXT:nn0:pk0;status_code_mapping:varchar(1024):nn0:pk0;priority:INTEGER:nn0:pk0;auto_ban:INTEGER:nn0:pk0;other_info:TEXT:nn0:pk0;tag:TEXT:nn0:pk0;setting:TEXT:nn0:pk0;param_override:TEXT:nn0:pk0;header_override:TEXT:nn0:pk0;remark:varchar(255):nn0:pk0;channel_info:json:nn0:pk0;settings:TEXT:nn0:pk0|idx_channels_tag[u0,c]:tag;idx_channels_name[u0,c]:name
checkins|id:INTEGER:nn0:pk1;user_id:INTEGER:nn1:pk0;checkin_date:varchar(10):nn1:pk0;quota_awarded:INTEGER:nn1:pk0;created_at:INTEGER:nn0:pk0|idx_user_checkin_date[u1,c]:user_id,checkin_date
custom_oauth_providers|id:INTEGER:nn0:pk1;name:varchar(64):nn1:pk0;slug:varchar(64):nn1:pk0;icon:varchar(128):nn0:pk0;enabled:numeric:nn0:pk0;client_id:varchar(256):nn0:pk0;client_secret:varchar(512):nn0:pk0;authorization_endpoint:varchar(512):nn0:pk0;token_endpoint:varchar(512):nn0:pk0;user_info_endpoint:varchar(512):nn0:pk0;scopes:varchar(256):nn0:pk0;user_id_field:varchar(128):nn0:pk0;username_field:varchar(128):nn0:pk0;display_name_field:varchar(128):nn0:pk0;email_field:varchar(128):nn0:pk0;well_known:varchar(512):nn0:pk0;auth_style:INTEGER:nn0:pk0;access_policy:TEXT:nn0:pk0;access_denied_message:varchar(512):nn0:pk0;created_at:datetime:nn0:pk0;updated_at:datetime:nn0:pk0|idx_custom_oauth_providers_slug[u1,c]:slug
external_identity_claims|id:INTEGER:nn0:pk1;provider:varchar(32):nn1:pk0;subject:varchar(128):nn1:pk0;user_id:INTEGER:nn1:pk0;created_at:datetime:nn0:pk0|idx_external_identity_claims_user_id[u0,c]:user_id;idx_external_identity_user[u1,c]:provider,user_id;idx_external_identity_subject[u1,c]:provider,subject
logs|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;created_at:INTEGER:nn0:pk0;type:INTEGER:nn0:pk0;content:TEXT:nn0:pk0;username:TEXT:nn0:pk0;token_name:TEXT:nn0:pk0;model_name:TEXT:nn0:pk0;quota:INTEGER:nn0:pk0;prompt_tokens:INTEGER:nn0:pk0;completion_tokens:INTEGER:nn0:pk0;use_time:INTEGER:nn0:pk0;is_stream:numeric:nn0:pk0;channel_id:INTEGER:nn0:pk0;channel_name:TEXT:nn0:pk0;token_id:INTEGER:nn0:pk0;group:TEXT:nn0:pk0;ip:TEXT:nn0:pk0;request_id:varchar(64):nn0:pk0;upstream_request_id:varchar(128):nn0:pk0;other:TEXT:nn0:pk0|idx_created_at_id[u0,c]:created_at,id;idx_user_id_id[u0,c]:user_id,id;idx_created_at_type[u0,c]:created_at,type;idx_logs_username[u0,c]:username;idx_logs_token_name[u0,c]:token_name;idx_logs_token_id[u0,c]:token_id;idx_logs_upstream_request_id[u0,c]:upstream_request_id;idx_logs_user_id[u0,c]:user_id;index_username_model_name[u0,c]:model_name,username;idx_logs_model_name[u0,c]:model_name;idx_logs_channel_id[u0,c]:channel_id;idx_logs_group[u0,c]:group;idx_logs_ip[u0,c]:ip;idx_logs_request_id[u0,c]:request_id
midjourneys|id:INTEGER:nn0:pk1;code:INTEGER:nn0:pk0;user_id:INTEGER:nn0:pk0;action:varchar(40):nn0:pk0;mj_id:TEXT:nn0:pk0;prompt:TEXT:nn0:pk0;prompt_en:TEXT:nn0:pk0;description:TEXT:nn0:pk0;state:TEXT:nn0:pk0;submit_time:INTEGER:nn0:pk0;start_time:INTEGER:nn0:pk0;finish_time:INTEGER:nn0:pk0;image_url:TEXT:nn0:pk0;video_url:TEXT:nn0:pk0;video_urls:TEXT:nn0:pk0;status:varchar(20):nn0:pk0;progress:varchar(30):nn0:pk0;fail_reason:TEXT:nn0:pk0;channel_id:INTEGER:nn0:pk0;quota:INTEGER:nn0:pk0;buttons:TEXT:nn0:pk0;properties:TEXT:nn0:pk0;token_id:INTEGER:nn0:pk0;billing_channel_id:INTEGER:nn0:pk0|idx_midjourneys_user_id[u0,c]:user_id;idx_midjourneys_action[u0,c]:action;idx_midjourneys_mj_id[u0,c]:mj_id;idx_midjourneys_submit_time[u0,c]:submit_time;idx_midjourneys_start_time[u0,c]:start_time;idx_midjourneys_finish_time[u0,c]:finish_time;idx_midjourneys_status[u0,c]:status;idx_midjourneys_progress[u0,c]:progress
models|id:INTEGER:nn0:pk1;model_name:TEXT:nn1:pk0;description:TEXT:nn0:pk0;icon:varchar(128):nn0:pk0;tags:varchar(255):nn0:pk0;vendor_id:INTEGER:nn0:pk0;endpoints:TEXT:nn0:pk0;status:INTEGER:nn0:pk0;sync_official:INTEGER:nn0:pk0;created_time:INTEGER:nn0:pk0;updated_time:INTEGER:nn0:pk0;deleted_at:datetime:nn0:pk0;name_rule:INTEGER:nn0:pk0|uk_model_name_delete_at[u1,c]:model_name,deleted_at;idx_models_deleted_at[u0,c]:deleted_at;idx_models_vendor_id[u0,c]:vendor_id
options|key:TEXT:nn0:pk1;value:TEXT:nn0:pk0|sqlite_autoindex_options_1[u1,pk]:key
passkey_credentials|id:INTEGER:nn0:pk1;user_id:INTEGER:nn1:pk0;credential_id:varchar(512):nn1:pk0;public_key:TEXT:nn1:pk0;attestation_type:varchar(255):nn0:pk0;aa_guid:varchar(512):nn0:pk0;sign_count:INTEGER:nn0:pk0;clone_warning:numeric:nn0:pk0;user_present:numeric:nn0:pk0;user_verified:numeric:nn0:pk0;backup_eligible:numeric:nn0:pk0;backup_state:numeric:nn0:pk0;transports:TEXT:nn0:pk0;attachment:varchar(32):nn0:pk0;last_used_at:datetime:nn0:pk0;created_at:datetime:nn0:pk0;updated_at:datetime:nn0:pk0;deleted_at:datetime:nn0:pk0|idx_passkey_credentials_credential_id[u1,c]:credential_id;idx_passkey_credentials_deleted_at[u0,c]:deleted_at;idx_passkey_credentials_user_id[u1,c]:user_id
perf_metrics|id:INTEGER:nn0:pk1;model_name:TEXT:nn0:pk0;group:TEXT:nn0:pk0;bucket_ts:INTEGER:nn0:pk0;request_count:INTEGER:nn0:pk0;success_count:INTEGER:nn0:pk0;total_latency_ms:INTEGER:nn0:pk0;ttft_sum_ms:INTEGER:nn0:pk0;ttft_count:INTEGER:nn0:pk0;output_tokens:INTEGER:nn0:pk0;generation_ms:INTEGER:nn0:pk0|idx_perf_bucket_ts[u0,c]:bucket_ts;idx_perf_model_group_bucket[u1,c]:model_name,group,bucket_ts
prefill_groups|id:INTEGER:nn0:pk1;name:TEXT:nn1:pk0;type:TEXT:nn1:pk0;items:json:nn0:pk0;description:varchar(255):nn0:pk0;created_time:INTEGER:nn0:pk0;updated_time:INTEGER:nn0:pk0;deleted_at:datetime:nn0:pk0|idx_prefill_groups_deleted_at[u0,c]:deleted_at;uk_prefill_name[u1,c]:name;idx_prefill_groups_type[u0,c]:type
quota_data|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;username:TEXT:nn0:pk0;model_name:TEXT:nn0:pk0;created_at:INTEGER:nn0:pk0;use_group:TEXT:nn0:pk0;token_id:INTEGER:nn0:pk0;channel_id:INTEGER:nn0:pk0;node_name:TEXT:nn0:pk0;token_used:INTEGER:nn0:pk0;count:INTEGER:nn0:pk0;quota:INTEGER:nn0:pk0|idx_quota_data_channel_id[u0,c]:channel_id;idx_quota_data_node_name[u0,c]:node_name;idx_quota_data_user_id[u0,c]:user_id;idx_qdt_model_user_name[u0,c]:model_name,username;idx_qdt_created_at[u0,c]:created_at;idx_quota_data_use_group[u0,c]:use_group;idx_quota_data_token_id[u0,c]:token_id
redemptions|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;key:char(32):nn0:pk0;status:INTEGER:nn0:pk0;name:TEXT:nn0:pk0;quota:INTEGER:nn0:pk0;created_time:INTEGER:nn0:pk0;redeemed_time:INTEGER:nn0:pk0;used_user_id:INTEGER:nn0:pk0;deleted_at:datetime:nn0:pk0;expired_time:INTEGER:nn0:pk0|idx_redemptions_key[u1,c]:key;idx_redemptions_name[u0,c]:name;idx_redemptions_deleted_at[u0,c]:deleted_at
setups|id:INTEGER:nn0:pk1;version:varchar(50):nn1:pk0;initialized_at:bigint:nn1:pk0|-
subscription_orders|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;plan_id:INTEGER:nn0:pk0;money:REAL:nn0:pk0;trade_no:varchar(255):nn0:pk0;payment_method:varchar(50):nn0:pk0;payment_provider:varchar(50):nn0:pk0;status:TEXT:nn0:pk0;create_time:INTEGER:nn0:pk0;complete_time:INTEGER:nn0:pk0;provider_payload:TEXT:nn0:pk0|idx_subscription_orders_trade_no[u0,c]:trade_no;idx_subscription_orders_plan_id[u0,c]:plan_id;idx_subscription_orders_user_id[u0,c]:user_id;sqlite_autoindex_subscription_orders_1[u1,u]:trade_no
subscription_plans|id:INTEGER:nn0:pk1;title:varchar(128):nn1:pk0;subtitle:varchar(255):nn0:pk0;price_amount:decimal(10,6):nn1:pk0;currency:varchar(8):nn1:pk0;duration_unit:varchar(16):nn1:pk0;duration_value:INTEGER:nn1:pk0;custom_seconds:bigint:nn1:pk0;enabled:numeric:nn0:pk0;sort_order:INTEGER:nn0:pk0;allow_balance_pay:numeric:nn0:pk0;allow_wallet_overflow:numeric:nn0:pk0;stripe_price_id:varchar(128):nn0:pk0;creem_product_id:varchar(128):nn0:pk0;waffo_pancake_product_id:varchar(128):nn0:pk0;max_purchase_per_user:INTEGER:nn0:pk0;upgrade_group:varchar(64):nn0:pk0;downgrade_group:varchar(64):nn0:pk0;total_amount:bigint:nn1:pk0;quota_reset_period:varchar(16):nn0:pk0;quota_reset_custom_seconds:bigint:nn0:pk0;created_at:bigint:nn0:pk0;updated_at:bigint:nn0:pk0|-
subscription_pre_consume_records|id:INTEGER:nn0:pk1;request_id:varchar(64):nn0:pk0;user_id:INTEGER:nn0:pk0;user_subscription_id:INTEGER:nn0:pk0;pre_consumed:bigint:nn1:pk0;status:varchar(32):nn0:pk0;created_at:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0|idx_subscription_pre_consume_records_updated_at[u0,c]:updated_at;idx_subscription_pre_consume_records_request_id[u1,c]:request_id;idx_subscription_pre_consume_records_user_id[u0,c]:user_id;idx_subscription_pre_consume_records_user_subscription_id[u0,c]:user_subscription_id;idx_subscription_pre_consume_records_status[u0,c]:status
system_instances|node_name:varchar(128):nn0:pk1;info:TEXT:nn0:pk0;started_at:INTEGER:nn0:pk0;last_seen_at:INTEGER:nn0:pk0;created_at:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0|idx_system_instances_started_at[u0,c]:started_at;idx_system_instances_last_seen_at[u0,c]:last_seen_at;idx_system_instances_created_at[u0,c]:created_at;idx_system_instances_updated_at[u0,c]:updated_at;sqlite_autoindex_system_instances_1[u1,pk]:node_name
system_task_locks|type:varchar(64):nn0:pk1;task_id:varchar(64):nn0:pk0;locked_by:varchar(128):nn0:pk0;locked_until:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0|idx_system_task_locks_task_id[u0,c]:task_id;idx_system_task_locks_locked_by[u0,c]:locked_by;idx_system_task_locks_locked_until[u0,c]:locked_until;idx_system_task_locks_updated_at[u0,c]:updated_at;sqlite_autoindex_system_task_locks_1[u1,pk]:type
system_tasks|id:INTEGER:nn0:pk1;task_id:varchar(64):nn0:pk0;type:varchar(64):nn0:pk0;status:varchar(32):nn0:pk0;active_key:varchar(64):nn0:pk0;payload:TEXT:nn0:pk0;state:TEXT:nn0:pk0;result:TEXT:nn0:pk0;error:TEXT:nn0:pk0;locked_by:varchar(128):nn0:pk0;created_at:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0|idx_system_tasks_type[u0,c]:type;idx_system_tasks_status[u0,c]:status;idx_system_tasks_active_key[u1,c]:active_key;idx_system_tasks_locked_by[u0,c]:locked_by;idx_system_tasks_created_at[u0,c]:created_at;idx_system_tasks_updated_at[u0,c]:updated_at;idx_system_tasks_task_id[u1,c]:task_id
tasks|id:INTEGER:nn0:pk1;created_at:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0;task_id:varchar(191):nn0:pk0;platform:varchar(30):nn0:pk0;user_id:INTEGER:nn0:pk0;group:varchar(50):nn0:pk0;channel_id:INTEGER:nn0:pk0;quota:INTEGER:nn0:pk0;action:varchar(40):nn0:pk0;status:varchar(20):nn0:pk0;fail_reason:TEXT:nn0:pk0;submit_time:INTEGER:nn0:pk0;start_time:INTEGER:nn0:pk0;finish_time:INTEGER:nn0:pk0;progress:varchar(20):nn0:pk0;properties:json:nn0:pk0;private_data:json:nn0:pk0;data:json:nn0:pk0|idx_tasks_submit_time[u0,c]:submit_time;idx_tasks_start_time[u0,c]:start_time;idx_tasks_progress[u0,c]:progress;idx_tasks_created_at[u0,c]:created_at;idx_tasks_task_id[u0,c]:task_id;idx_tasks_user_id[u0,c]:user_id;idx_tasks_channel_id[u0,c]:channel_id;idx_tasks_action[u0,c]:action;idx_tasks_status[u0,c]:status;idx_tasks_finish_time[u0,c]:finish_time;idx_tasks_platform[u0,c]:platform
tokens|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;key:varchar(128):nn0:pk0;status:INTEGER:nn0:pk0;name:TEXT:nn0:pk0;created_time:INTEGER:nn0:pk0;accessed_time:INTEGER:nn0:pk0;expired_time:INTEGER:nn0:pk0;remain_quota:INTEGER:nn0:pk0;unlimited_quota:numeric:nn0:pk0;model_limits_enabled:numeric:nn0:pk0;model_limits:TEXT:nn0:pk0;allow_ips:TEXT:nn0:pk0;used_quota:INTEGER:nn0:pk0;group:TEXT:nn0:pk0;cross_group_retry:numeric:nn0:pk0;auto_groups:TEXT:nn0:pk0;deleted_at:datetime:nn0:pk0|idx_tokens_user_id[u0,c]:user_id;idx_tokens_key[u1,c]:key;idx_tokens_name[u0,c]:name;idx_tokens_deleted_at[u0,c]:deleted_at
top_ups|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;amount:INTEGER:nn0:pk0;money:REAL:nn0:pk0;trade_no:varchar(255):nn0:pk0;payment_method:varchar(50):nn0:pk0;payment_provider:varchar(50):nn0:pk0;create_time:INTEGER:nn0:pk0;complete_time:INTEGER:nn0:pk0;status:TEXT:nn0:pk0|idx_top_ups_trade_no[u0,c]:trade_no;idx_top_ups_user_id[u0,c]:user_id;sqlite_autoindex_top_ups_1[u1,u]:trade_no
two_fa_backup_codes|id:INTEGER:nn0:pk1;user_id:INTEGER:nn1:pk0;code_hash:varchar(255):nn1:pk0;is_used:numeric:nn0:pk0;used_at:datetime:nn0:pk0;created_at:datetime:nn0:pk0;deleted_at:datetime:nn0:pk0|idx_two_fa_backup_codes_user_id[u0,c]:user_id;idx_two_fa_backup_codes_deleted_at[u0,c]:deleted_at
two_fas|id:INTEGER:nn0:pk1;user_id:INTEGER:nn1:pk0;secret:varchar(255):nn1:pk0;is_enabled:numeric:nn0:pk0;failed_attempts:INTEGER:nn0:pk0;locked_until:datetime:nn0:pk0;last_used_at:datetime:nn0:pk0;created_at:datetime:nn0:pk0;updated_at:datetime:nn0:pk0;deleted_at:datetime:nn0:pk0|idx_two_fas_deleted_at[u0,c]:deleted_at;idx_two_fas_user_id[u0,c]:user_id;sqlite_autoindex_two_fas_1[u1,u]:user_id
user_oauth_bindings|id:INTEGER:nn0:pk1;user_id:INTEGER:nn1:pk0;provider_id:INTEGER:nn1:pk0;provider_user_id:varchar(256):nn1:pk0;created_at:datetime:nn0:pk0|ux_provider_userid[u1,c]:provider_id,provider_user_id;ux_user_provider[u1,c]:user_id,provider_id
user_sessions|sid:varchar(64):nn0:pk1;user_id:INTEGER:nn1:pk0;version:bigint:nn1:pk0;user_auth_version:bigint:nn1:pk0;status:varchar(16):nn1:pk0;refresh_hash:char(64):nn1:pk0;previous_refresh_hash:varchar(64):nn0:pk0;previous_valid_until:bigint:nn1:pk0;login_method:varchar(32):nn1:pk0;ip:varchar(64):nn0:pk0;user_agent:TEXT:nn0:pk0;created_at:INTEGER:nn0:pk0;last_active_at:bigint:nn1:pk0;expires_at:bigint:nn1:pk0;revoked_at:bigint:nn1:pk0;revoked_reason:varchar(64):nn0:pk0|idx_user_sessions_user_status_expiry[u0,c]:user_id,status,expires_at;idx_user_sessions_user_created[u0,c]:user_id,created_at;idx_user_sessions_status_revoked[u0,c]:status,revoked_at;idx_user_sessions_expires_at[u0,c]:expires_at;sqlite_autoindex_user_sessions_1[u1,pk]:sid
user_subscriptions|id:INTEGER:nn0:pk1;user_id:INTEGER:nn0:pk0;plan_id:INTEGER:nn0:pk0;amount_total:bigint:nn1:pk0;amount_used:bigint:nn1:pk0;start_time:INTEGER:nn0:pk0;end_time:INTEGER:nn0:pk0;status:varchar(32):nn0:pk0;source:varchar(32):nn0:pk0;last_reset_time:bigint:nn0:pk0;next_reset_time:bigint:nn0:pk0;upgrade_group:varchar(64):nn0:pk0;prev_user_group:varchar(64):nn0:pk0;downgrade_group:varchar(64):nn0:pk0;allow_wallet_overflow:numeric:nn0:pk0;created_at:INTEGER:nn0:pk0;updated_at:INTEGER:nn0:pk0|idx_user_subscriptions_status[u0,c]:status;idx_user_subscriptions_next_reset_time[u0,c]:next_reset_time;idx_user_subscriptions_user_id[u0,c]:user_id;idx_user_sub_active[u0,c]:user_id,status,end_time;idx_user_subscriptions_plan_id[u0,c]:plan_id;idx_user_subscriptions_end_time[u0,c]:end_time
users|id:INTEGER:nn0:pk1;username:TEXT:nn0:pk0;password:TEXT:nn1:pk0;display_name:TEXT:nn0:pk0;role:INTEGER:nn0:pk0;status:INTEGER:nn0:pk0;email:TEXT:nn0:pk0;github_id:TEXT:nn0:pk0;discord_id:TEXT:nn0:pk0;oidc_id:TEXT:nn0:pk0;wechat_id:TEXT:nn0:pk0;telegram_id:TEXT:nn0:pk0;access_token:char(32):nn0:pk0;quota:INTEGER:nn0:pk0;used_quota:INTEGER:nn0:pk0;request_count:INTEGER:nn0:pk0;group:varchar(64):nn0:pk0;aff_code:varchar(32):nn0:pk0;aff_count:INTEGER:nn0:pk0;aff_quota:INTEGER:nn0:pk0;aff_history:INTEGER:nn0:pk0;inviter_id:INTEGER:nn0:pk0;deleted_at:datetime:nn0:pk0;linux_do_id:TEXT:nn0:pk0;setting:TEXT:nn0:pk0;remark:varchar(255):nn0:pk0;stripe_customer:varchar(64):nn0:pk0;created_at:INTEGER:nn0:pk0;last_login_at:INTEGER:nn0:pk0;auth_version:bigint:nn1:pk0|idx_users_stripe_customer[u0,c]:stripe_customer;idx_users_linux_do_id[u0,c]:linux_do_id;idx_users_aff_code[u1,c]:aff_code;idx_users_access_token[u1,c]:access_token;idx_users_git_hub_id[u0,c]:github_id;idx_users_display_name[u0,c]:display_name;idx_users_deleted_at[u0,c]:deleted_at;idx_users_inviter_id[u0,c]:inviter_id;idx_users_telegram_id[u0,c]:telegram_id;idx_users_we_chat_id[u0,c]:wechat_id;idx_users_oidc_id[u0,c]:oidc_id;idx_users_discord_id[u0,c]:discord_id;idx_users_email[u0,c]:email;idx_users_username[u0,c]:username;sqlite_autoindex_users_1[u1,u]:username
vendors|id:INTEGER:nn0:pk1;name:TEXT:nn1:pk0;description:TEXT:nn0:pk0;icon:varchar(128):nn0:pk0;status:INTEGER:nn0:pk0;created_time:INTEGER:nn0:pk0;updated_time:INTEGER:nn0:pk0;deleted_at:datetime:nn0:pk0|idx_vendors_deleted_at[u0,c]:deleted_at;uk_vendor_name_delete_at[u1,c]:name,deleted_at
```

## P-M2 约束

该 profile 的目标 schema 可额外拥有候选版本定义的企业表与列，但源 signature 本身不得被解释为这些对象已存在。`subscription_plans`、`auth_flows`、`external_identity_claims` 与 `authz_roles` 已在这 34 表中，P-M2 不得把它们当作待合成的新表。

## Acceptance Criteria（验收标准）

### AC-1: 精确接受 (FR-1)

Given 一个声明为 `SQLite-34-pre-enterprise` 的来源，When P-M2 执行预检，Then 它只在 34 张表的全部签名精确匹配时接受。

### AC-2: 差异拒绝 (FR-2)

Given 表、列、主键、类型、`notnull` 或索引签名的任一差异，When P-M2 执行预检，Then 在目标 DDL/DML 前拒绝，且报告只含对象名和差异类别。

### AC-3: 非敏感报告 (FR-3)

Given 预检通过或失败，When 生成签名比对报告，Then 报告不含业务列值。

## Edge Cases（边界情况）

- EC-1: `logs` 是否属于主库仍由 manifest 的日志范围决定；本文件列出它仅说明该 SQLite 文件包含该表，不证明日志库拓扑。
- EC-2: 后续候选版本新增表或列时，必须新增已审阅 source profile 或更新本签名；不得把新对象默认为兼容。
- EC-3: 该签名不证明 SQLite 快照一致性；未证明冻结点或 WAL 语义时，P-M2 仍必须拒绝实际复制。

## API Contracts（API 契约）

N/A — 此基线不新增 HTTP、CLI 或运行时数据库接口。

## Data Models（数据模型）

| Field | Type | Constraints |
| --- | --- | --- |
| `SQLite34SchemaSignature.tables` | `[]TableSignature` | 固定 34 项；不接受动态扩展 |
| `TableSignature` | 表名、列签名、索引签名 | 不含业务值、DSN、路径或秘密 |

## Out of Scope（非范围）

- OS-1: 生产业务数据、SQLite 文件路径、DSN、环境变量、WAL、数据规模、日志库身份、备份、UAT 与生产切换。
- OS-2: 对来源或目标执行任何 DDL/DML，或将签名用作数据复制、脱敏或发布成功的证据。
