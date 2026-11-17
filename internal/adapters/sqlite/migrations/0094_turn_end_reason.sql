-- 0094: 聊天轮次的结束原因（#328）。轮次结束时由编排器写入 Schema
-- application.chat.state.turn_end_reasons 声明的字符串（completed、tool_limit、
-- awaiting_approval、stopped、background、error 对应的值）；Schema 没有声明时写空串。
-- 轮次重新进入 running 时清空。历史行与进行中的轮次为空串，表示未知，零行为变化。
ALTER TABLE go_chat_turns ADD COLUMN end_reason TEXT NOT NULL DEFAULT '';
