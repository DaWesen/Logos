-- Migration: 000000_init
-- Description: Initialize Logos database schema
-- Version: 0.0.0

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Starting initial database setup...'; END $$;

-- Create extensions
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating extensions...'; END $$;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ====================================================
-- User Module (platform/user)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: users'; END $$;
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    email VARCHAR(100),
    phone VARCHAR(20),
    avatar VARCHAR(255),
    preferences JSONB DEFAULT '{}',
    interests JSONB DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);

-- ====================================================
-- Contact Module (messaging/contact)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: friendships'; END $$;
CREATE TABLE IF NOT EXISTS friendships (
    id VARCHAR(100) PRIMARY KEY,
    user_id BIGINT NOT NULL,
    friend_id BIGINT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    remark VARCHAR(100),
    group_id VARCHAR(100),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    UNIQUE(user_id, friend_id)
);
CREATE INDEX IF NOT EXISTS idx_friendships_user_id ON friendships(user_id);
CREATE INDEX IF NOT EXISTS idx_friendships_friend_id ON friendships(friend_id);
CREATE INDEX IF NOT EXISTS idx_friendships_status ON friendships(status);
CREATE INDEX IF NOT EXISTS idx_friendships_user_friend ON friendships(user_id, friend_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: friend_requests'; END $$;
CREATE TABLE IF NOT EXISTS friend_requests (
    id VARCHAR(100) PRIMARY KEY,
    from_user_id BIGINT NOT NULL,
    to_user_id BIGINT NOT NULL,
    remark VARCHAR(100),
    message VARCHAR(200),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(from_user_id, to_user_id)
);
CREATE INDEX IF NOT EXISTS idx_friend_requests_from ON friend_requests(from_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_to ON friend_requests(to_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_status ON friend_requests(status);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: friend_groups'; END $$;
CREATE TABLE IF NOT EXISTS friend_groups (
    id VARCHAR(100) PRIMARY KEY,
    user_id BIGINT NOT NULL,
    name VARCHAR(50),
    sort INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_friend_groups_user_id ON friend_groups(user_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: friend_group_members'; END $$;
CREATE TABLE IF NOT EXISTS friend_group_members (
    id VARCHAR(100) PRIMARY KEY,
    user_id BIGINT NOT NULL,
    friend_id BIGINT NOT NULL,
    group_id VARCHAR(100),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_friend_group_members_user_id ON friend_group_members(user_id);
CREATE INDEX IF NOT EXISTS idx_friend_group_members_friend_id ON friend_group_members(friend_id);
CREATE INDEX IF NOT EXISTS idx_friend_group_members_group_id ON friend_group_members(group_id);

-- ====================================================
-- Chat Module (messaging/chat)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: conversations'; END $$;
CREATE TABLE IF NOT EXISTS conversations (
    id VARCHAR(100) PRIMARY KEY,
    chat_id VARCHAR(100) NOT NULL,
    chat_type INTEGER NOT NULL,
    name VARCHAR(255),
    avatar VARCHAR(255),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_conversations_chat_id ON conversations(chat_id);
CREATE INDEX IF NOT EXISTS idx_conversations_type ON conversations(chat_type);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: conversation_participants'; END $$;
CREATE TABLE IF NOT EXISTS conversation_participants (
    id BIGSERIAL PRIMARY KEY,
    conversation_id VARCHAR(100) NOT NULL,
    user_id BIGINT NOT NULL,
    last_read_at TIMESTAMPTZ,
    joined_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_conversation_participants_conversation ON conversation_participants(conversation_id);
CREATE INDEX IF NOT EXISTS idx_conversation_participants_user ON conversation_participants(user_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: messages'; END $$;
CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(64) PRIMARY KEY DEFAULT uuid_generate_v4(),
    request_id VARCHAR(64),
    conversation_id VARCHAR(100),
    chat_id VARCHAR(100),
    chat_type INTEGER,
    sender_id BIGINT NOT NULL,
    message_type INTEGER DEFAULT 1,
    content TEXT,
    media_url VARCHAR(500),
    media_meta JSONB DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    mention_user_ids JSONB DEFAULT '[]',
    reply_to_message VARCHAR(100),
    status VARCHAR(50) DEFAULT 'sent',
    is_read BOOLEAN DEFAULT false,
    channel VARCHAR(50) DEFAULT 'web',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_chat_id ON messages(chat_id);
CREATE INDEX IF NOT EXISTS idx_messages_chat_type ON messages(chat_type);
CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_request_id ON messages(request_id);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_deleted_at ON messages(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: groups'; END $$;
CREATE TABLE IF NOT EXISTS groups (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    owner_id BIGINT NOT NULL,
    avatar VARCHAR(255),
    description TEXT,
    announcement TEXT,
    max_members INTEGER DEFAULT 500,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_groups_owner ON groups(owner_id);
CREATE INDEX IF NOT EXISTS idx_groups_deleted_at ON groups(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: group_members'; END $$;
CREATE TABLE IF NOT EXISTS group_members (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    role INTEGER NOT NULL DEFAULT 3,
    nickname VARCHAR(50),
    mute_type INTEGER NOT NULL DEFAULT 1,
    mute_until TIMESTAMPTZ,
    joined_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON group_members(user_id);
CREATE INDEX IF NOT EXISTS idx_group_members_role ON group_members(role);

-- ====================================================
-- IM Module (messaging/im)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: online_records'; END $$;
CREATE TABLE IF NOT EXISTS online_records (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    device_id VARCHAR(100),
    session_id VARCHAR(100) NOT NULL UNIQUE,
    online BOOLEAN DEFAULT false,
    last_seen TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    platform VARCHAR(50),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_online_records_user ON online_records(user_id);
CREATE INDEX IF NOT EXISTS idx_online_records_session ON online_records(session_id);
CREATE INDEX IF NOT EXISTS idx_online_records_online ON online_records(online);

-- ====================================================
-- Message Module (messaging/message)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: queue_messages'; END $$;
CREATE TABLE IF NOT EXISTS queue_messages (
    id VARCHAR(64) PRIMARY KEY,
    topic VARCHAR(64) NOT NULL,
    content TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 2,
    headers TEXT,
    correlation_id VARCHAR(128),
    timestamp BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_queue_messages_topic ON queue_messages(topic);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: message_subscriptions'; END $$;
CREATE TABLE IF NOT EXISTS message_subscriptions (
    id VARCHAR(64) PRIMARY KEY,
    topic VARCHAR(64) NOT NULL,
    consumer_group VARCHAR(128) NOT NULL,
    config TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_message_subscriptions_topic ON message_subscriptions(topic);
CREATE INDEX IF NOT EXISTS idx_message_subscriptions_consumer_group ON message_subscriptions(consumer_group);

-- ====================================================
-- Bot Module (ai/bot)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: bots'; END $$;
CREATE TABLE IF NOT EXISTS bots (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar TEXT,
    type VARCHAR(50),
    provider VARCHAR(50),
    model VARCHAR(255),
    api_key TEXT,
    base_url TEXT,
    embedding_model VARCHAR(255),
    system_prompt TEXT,
    config JSONB DEFAULT '{}',
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_bots_user_id ON bots(user_id);
CREATE INDEX IF NOT EXISTS idx_bots_name ON bots(name);
CREATE INDEX IF NOT EXISTS idx_bots_deleted_at ON bots(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: bot_conversations'; END $$;
CREATE TABLE IF NOT EXISTS bot_conversations (
    id VARCHAR(36) PRIMARY KEY,
    bot_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    title TEXT,
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_bot_conversations_bot_id ON bot_conversations(bot_id);
CREATE INDEX IF NOT EXISTS idx_bot_conversations_user_id ON bot_conversations(user_id);
CREATE INDEX IF NOT EXISTS idx_bot_conversations_deleted_at ON bot_conversations(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: bot_messages'; END $$;
CREATE TABLE IF NOT EXISTS bot_messages (
    id VARCHAR(36) PRIMARY KEY,
    bot_id VARCHAR(36),
    conversation_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_bot_messages_bot_id ON bot_messages(bot_id);
CREATE INDEX IF NOT EXISTS idx_bot_messages_conversation_id ON bot_messages(conversation_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: prompts'; END $$;
CREATE TABLE IF NOT EXISTS prompts (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    content TEXT NOT NULL,
    type VARCHAR(50),
    is_preset BOOLEAN DEFAULT false,
    is_public BOOLEAN DEFAULT false,
    config JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_prompts_user_id ON prompts(user_id);
CREATE INDEX IF NOT EXISTS idx_prompts_name ON prompts(name);
CREATE INDEX IF NOT EXISTS idx_prompts_deleted_at ON prompts(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: user_memories'; END $$;
CREATE TABLE IF NOT EXISTS user_memories (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    bot_id VARCHAR(36) NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT NOT NULL,
    category VARCHAR(50),
    source VARCHAR(50),
    confidence DECIMAL(3,2) DEFAULT 0.8,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_user_memories_user_id ON user_memories(user_id);
CREATE INDEX IF NOT EXISTS idx_user_memories_bot_id ON user_memories(bot_id);
CREATE INDEX IF NOT EXISTS idx_user_memories_category ON user_memories(category);
CREATE INDEX IF NOT EXISTS idx_user_memories_deleted_at ON user_memories(deleted_at);

-- ====================================================
-- Moderation Module (ai/moderation)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: moderation_records'; END $$;
CREATE TABLE IF NOT EXISTS moderation_records (
    id VARCHAR(64) PRIMARY KEY,
    content TEXT NOT NULL,
    content_id VARCHAR(64),
    content_type VARCHAR(32),
    result VARCHAR(32) NOT NULL,
    categories TEXT,
    scores TEXT,
    action_taken VARCHAR(64),
    moderator_id VARCHAR(64),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_moderation_records_content_id ON moderation_records(content_id);
CREATE INDEX IF NOT EXISTS idx_moderation_records_result ON moderation_records(result);
CREATE INDEX IF NOT EXISTS idx_moderation_records_deleted_at ON moderation_records(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: translation_records'; END $$;
CREATE TABLE IF NOT EXISTS translation_records (
    id VARCHAR(64) PRIMARY KEY,
    content TEXT NOT NULL,
    translated_content TEXT NOT NULL,
    source_lang VARCHAR(16) NOT NULL,
    target_lang VARCHAR(16) NOT NULL,
    content_id VARCHAR(64),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_translation_records_content_id ON translation_records(content_id);

-- ====================================================
-- MCP Module (ai/mcp)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: mcp_tools'; END $$;
CREATE TABLE IF NOT EXISTS mcp_tools (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL UNIQUE,
    description TEXT,
    type INTEGER NOT NULL,
    config TEXT,
    parameters TEXT,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_mcp_tools_deleted_at ON mcp_tools(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: mcp_tool_call_logs'; END $$;
CREATE TABLE IF NOT EXISTS mcp_tool_call_logs (
    id VARCHAR(64) PRIMARY KEY,
    tool_id VARCHAR(64) NOT NULL,
    tool_name VARCHAR(128) NOT NULL,
    params TEXT,
    result TEXT,
    status VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_mcp_tool_call_logs_tool_id ON mcp_tool_call_logs(tool_id);

-- ====================================================
-- Summary Module (ai/summary)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: summary_records'; END $$;
CREATE TABLE IF NOT EXISTS summary_records (
    id VARCHAR(64) PRIMARY KEY,
    chat_id VARCHAR(64) NOT NULL,
    chat_type VARCHAR(32) NOT NULL,
    summary TEXT NOT NULL,
    key_points TEXT,
    participants TEXT,
    todos TEXT,
    message_ids TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_summary_records_chat_id ON summary_records(chat_id);
CREATE INDEX IF NOT EXISTS idx_summary_records_deleted_at ON summary_records(deleted_at);

-- ====================================================
-- Knowledge Module (ai/knowledge)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: entities'; END $$;
CREATE TABLE IF NOT EXISTS entities (
    id VARCHAR(36) PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    properties JSONB DEFAULT '{}',
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_entities_type ON entities(type);
CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(name);
CREATE INDEX IF NOT EXISTS idx_entities_deleted_at ON entities(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: relations'; END $$;
CREATE TABLE IF NOT EXISTS relations (
    id VARCHAR(36) PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    source_id VARCHAR(36) NOT NULL,
    target_id VARCHAR(36) NOT NULL,
    properties JSONB DEFAULT '{}',
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_relations_type ON relations(type);
CREATE INDEX IF NOT EXISTS idx_relations_source_id ON relations(source_id);
CREATE INDEX IF NOT EXISTS idx_relations_target_id ON relations(target_id);
CREATE INDEX IF NOT EXISTS idx_relations_deleted_at ON relations(deleted_at);

-- ====================================================
-- Process Module (ai/process)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: documents'; END $$;
CREATE TABLE IF NOT EXISTS documents (
    id VARCHAR(36) PRIMARY KEY,
    file_name VARCHAR(255) NOT NULL,
    file_type VARCHAR(50),
    file_size BIGINT,
    file_url TEXT,
    file_hash VARCHAR(64),
    status INTEGER,
    content TEXT,
    metadata JSONB DEFAULT '{}',
    error_msg TEXT,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_documents_file_type ON documents(file_type);
CREATE INDEX IF NOT EXISTS idx_documents_file_hash ON documents(file_hash);
CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(status);
CREATE INDEX IF NOT EXISTS idx_documents_deleted_at ON documents(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: document_chunks'; END $$;
CREATE TABLE IF NOT EXISTS document_chunks (
    id VARCHAR(36) PRIMARY KEY,
    document_id VARCHAR(36),
    chunk_index INTEGER,
    chunk_type VARCHAR(50),
    content TEXT,
    vector_id VARCHAR(36),
    parent_id VARCHAR(36),
    image_info TEXT,
    is_enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_document_chunks_document_id ON document_chunks(document_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_chunk_index ON document_chunks(chunk_index);
CREATE INDEX IF NOT EXISTS idx_document_chunks_chunk_type ON document_chunks(chunk_type);
CREATE INDEX IF NOT EXISTS idx_document_chunks_vector_id ON document_chunks(vector_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_parent_id ON document_chunks(parent_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_deleted_at ON document_chunks(deleted_at);

-- ====================================================
-- Recommend Module (ai/recommend)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: recommendation_items'; END $$;
CREATE TABLE IF NOT EXISTS recommendation_items (
    id VARCHAR(64) PRIMARY KEY,
    type VARCHAR(32) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    score DECIMAL(10,4) NOT NULL,
    entity_id VARCHAR(64),
    image_url VARCHAR(512),
    created_at BIGINT NOT NULL,
    db_created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_recommendation_items_type ON recommendation_items(type);
CREATE INDEX IF NOT EXISTS idx_recommendation_items_entity_id ON recommendation_items(entity_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: recommendation_histories'; END $$;
CREATE TABLE IF NOT EXISTS recommendation_histories (
    id VARCHAR(64) PRIMARY KEY,
    item_id VARCHAR(64) NOT NULL,
    item_type VARCHAR(32) NOT NULL,
    title VARCHAR(255),
    action VARCHAR(32) NOT NULL,
    user_id BIGINT NOT NULL,
    timestamp BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_recommendation_histories_item_id ON recommendation_histories(item_id);
CREATE INDEX IF NOT EXISTS idx_recommendation_histories_user_id ON recommendation_histories(user_id);

-- ====================================================
-- Collection Module (ai/collection)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: data_sources'; END $$;
CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    type INTEGER NOT NULL,
    url VARCHAR(512),
    config TEXT,
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: collection_tasks'; END $$;
CREATE TABLE IF NOT EXISTS collection_tasks (
    id VARCHAR(64) PRIMARY KEY,
    data_source_id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    format INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    schedule VARCHAR(64),
    last_run_time TIMESTAMPTZ,
    next_run_time TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_collection_tasks_data_source_id ON collection_tasks(data_source_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: collection_results'; END $$;
CREATE TABLE IF NOT EXISTS collection_results (
    id VARCHAR(64) PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'RUNNING',
    collected_count BIGINT NOT NULL DEFAULT 0,
    processed_count BIGINT NOT NULL DEFAULT 0,
    error_msg TEXT,
    start_time BIGINT NOT NULL,
    end_time BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_collection_results_task_id ON collection_results(task_id);

-- ====================================================
-- Question Module (ai/question)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: qa_records'; END $$;
CREATE TABLE IF NOT EXISTS qa_records (
    id VARCHAR(64) PRIMARY KEY,
    question TEXT NOT NULL,
    answer TEXT NOT NULL,
    confidence DECIMAL(5,4) DEFAULT 0,
    user_id BIGINT NOT NULL,
    timestamp BIGINT NOT NULL,
    feedback TEXT,
    rating INTEGER,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_qa_records_user_id ON qa_records(user_id);

-- ====================================================
-- Extraction Module (ai/extraction)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: extraction_tasks'; END $$;
CREATE TABLE IF NOT EXISTS extraction_tasks (
    id VARCHAR(64) PRIMARY KEY,
    type INTEGER NOT NULL,
    data_id VARCHAR(128) NOT NULL,
    data_type VARCHAR(32) NOT NULL,
    status INTEGER NOT NULL DEFAULT 1,
    parameters TEXT,
    scheduled_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_extraction_tasks_data_id ON extraction_tasks(data_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: extraction_results'; END $$;
CREATE TABLE IF NOT EXISTS extraction_results (
    id VARCHAR(64) PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL,
    status INTEGER NOT NULL DEFAULT 1,
    entities TEXT,
    relations TEXT,
    triples TEXT,
    summary TEXT,
    keyphrases TEXT,
    error_msg TEXT,
    start_time BIGINT NOT NULL,
    end_time BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_extraction_results_task_id ON extraction_results(task_id);

-- ====================================================
-- Billing Module (platform/billing)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: accounts'; END $$;
CREATE TABLE IF NOT EXISTS accounts (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36),
    balance DOUBLE PRECISION NOT NULL DEFAULT 0,
    credit_limit DOUBLE PRECISION NOT NULL DEFAULT 0,
    usage JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: transactions'; END $$;
CREATE TABLE IF NOT EXISTS transactions (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    account_id VARCHAR(36) NOT NULL,
    type INTEGER NOT NULL,
    item INTEGER NOT NULL,
    amount DOUBLE PRECISION NOT NULL,
    balance_before DOUBLE PRECISION NOT NULL,
    balance_after DOUBLE PRECISION NOT NULL,
    description TEXT,
    status INTEGER NOT NULL DEFAULT 1,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_transactions_user_id ON transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_transactions_account_id ON transactions(account_id);

-- ====================================================
-- Monitoring Module (platform/monitoring)
-- ====================================================
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: metrics'; END $$;
CREATE TABLE IF NOT EXISTS metrics (
    id VARCHAR(64) PRIMARY KEY,
    service_name VARCHAR(128) NOT NULL,
    type INTEGER NOT NULL,
    value DOUBLE PRECISION NOT NULL,
    unit VARCHAR(32),
    tags TEXT,
    timestamp BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_metrics_service_name ON metrics(service_name);
CREATE INDEX IF NOT EXISTS idx_metrics_timestamp ON metrics(timestamp);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: alerts'; END $$;
CREATE TABLE IF NOT EXISTS alerts (
    id VARCHAR(64) PRIMARY KEY,
    service_name VARCHAR(128) NOT NULL,
    level INTEGER NOT NULL,
    message TEXT NOT NULL,
    metric_name VARCHAR(128),
    metric_value DOUBLE PRECISION,
    threshold DOUBLE PRECISION,
    timestamp BIGINT NOT NULL,
    resolved BOOLEAN NOT NULL DEFAULT false,
    resolution_time VARCHAR(32),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_alerts_service_name ON alerts(service_name);
CREATE INDEX IF NOT EXISTS idx_alerts_timestamp ON alerts(timestamp);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: logs'; END $$;
CREATE TABLE IF NOT EXISTS logs (
    id VARCHAR(64) PRIMARY KEY,
    service_name VARCHAR(128) NOT NULL,
    level VARCHAR(16) NOT NULL,
    message TEXT NOT NULL,
    fields TEXT,
    timestamp BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_logs_service_name ON logs(service_name);
CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: service_statuses'; END $$;
CREATE TABLE IF NOT EXISTS service_statuses (
    id VARCHAR(64) PRIMARY KEY,
    service_name VARCHAR(128) NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT 'UP',
    last_check_time BIGINT NOT NULL,
    error_message TEXT,
    metadata TEXT,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Initialization completed!'; END $$;
