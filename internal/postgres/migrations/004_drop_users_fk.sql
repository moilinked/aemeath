-- conversations.user_id 只保存 site 用户 ID，不再引用本库 users 表。
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_user_id_fkey;
