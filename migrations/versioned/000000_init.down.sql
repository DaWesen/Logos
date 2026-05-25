-- Migration: 000000_init (Downgrade)
-- Description: Rollback initial setup
-- Version: 0.0.0

DO $$ BEGIN RAISE NOTICE '[Migration 000000 Downgrade] Starting...'; END $$;

-- Drop tables in reverse order
-- Monitoring
DROP TABLE IF EXISTS service_statuses;
DROP TABLE IF EXISTS logs;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS metrics;
-- Billing
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS accounts;
-- Extraction
DROP TABLE IF EXISTS extraction_results;
DROP TABLE IF EXISTS extraction_tasks;
-- Question
DROP TABLE IF EXISTS qa_records;
-- Collection
DROP TABLE IF EXISTS collection_results;
DROP TABLE IF EXISTS collection_tasks;
DROP TABLE IF EXISTS data_sources;
-- Recommend
DROP TABLE IF EXISTS recommendation_histories;
DROP TABLE IF EXISTS recommendation_items;
-- Process
DROP TABLE IF EXISTS document_chunks;
DROP TABLE IF EXISTS documents;
-- Knowledge
DROP TABLE IF EXISTS relations;
DROP TABLE IF EXISTS entities;
-- Summary
DROP TABLE IF EXISTS summary_records;
-- MCP
DROP TABLE IF EXISTS mcp_tool_call_logs;
DROP TABLE IF EXISTS mcp_tools;
-- Moderation
DROP TABLE IF EXISTS translation_records;
DROP TABLE IF EXISTS moderation_records;
-- Bot
DROP TABLE IF EXISTS user_memories;
DROP TABLE IF EXISTS prompts;
DROP TABLE IF EXISTS bot_messages;
DROP TABLE IF EXISTS bot_conversations;
DROP TABLE IF EXISTS bots;
-- Message
DROP TABLE IF EXISTS message_subscriptions;
DROP TABLE IF EXISTS queue_messages;
-- IM
DROP TABLE IF EXISTS online_records;
-- Chat
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversation_participants;
DROP TABLE IF EXISTS conversations;
-- Contact
DROP TABLE IF EXISTS friend_group_members;
DROP TABLE IF EXISTS friend_groups;
DROP TABLE IF EXISTS friend_requests;
DROP TABLE IF EXISTS friendships;
-- User
DROP TABLE IF EXISTS users;

DO $$ BEGIN RAISE NOTICE '[Migration 000000 Downgrade] Completed!'; END $$;
