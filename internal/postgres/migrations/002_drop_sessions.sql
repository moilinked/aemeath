-- 旧 schema 的 sessions 不再使用，直接丢弃表和数据。
DROP TABLE IF EXISTS sessions CASCADE;

